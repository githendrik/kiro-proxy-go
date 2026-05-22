package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Token refresh threshold - refresh when token has less than this time remaining
// Since access tokens expire after 1 hour, we refresh when 15 minutes remain
const TokenRefreshThreshold = 15 * time.Minute

// CredsFile represents the JSON credentials file written by kiro-cli.
type CredsFile struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    string `json:"expiresAt"`
	Region       string `json:"region"`
	AuthMethod   string `json:"authMethod,omitempty"`
	ClientIDHash string `json:"clientIdHash,omitempty"`
	ProfileARN   string `json:"profileArn,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
}

// Manager handles Kiro API token lifecycle.
// Supports two modes:
//   - File mode: reads credentials from a JSON file (kiro-cli writes this)
//   - Direct mode: uses a refresh token from env var
type Manager struct {
	credsFilePath string // path to JSON credentials file (file mode)
	refreshToken  string // direct refresh token (direct mode)
	clientID      string // OAuth client ID (for OIDC auth)
	clientSecret  string // OAuth client secret (for OIDC auth)
	region        string // auth region (for refresh endpoint)
	fileRegion    string // region from creds file (for API host)
	authType      AuthType

	mu              sync.Mutex
	accessToken     string
	expiresAt       time.Time
	profileARN      string
	lastRefreshTime time.Time
	
	// Background refresh
	stopRefresh chan struct{}
	wg          sync.WaitGroup
}

// AuthType represents the authentication mechanism.
type AuthType int

const (
	AuthTypeUnknown AuthType = iota
	AuthTypeDesktop          // Kiro Desktop Auth
	AuthTypeOIDC             // AWS SSO OIDC (Builder ID)
)

// ProfileARN returns the profile ARN if it should be included in requests.
// For OIDC auth, profileArn is not needed and can cause 403 errors.
func (m *Manager) ProfileARN() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Only return profileArn for Desktop auth
	// OIDC auth (Builder ID) doesn't need it and it causes 403 if sent
	if m.authType == AuthTypeOIDC {
		return ""
	}
	return m.profileARN
}

// NewManagerFromFile creates an auth manager that reads from a kiro-cli credentials file.
// The file is re-read on each refresh to pick up tokens updated by kiro-cli.
// authRegion is used for the refresh endpoint (may differ from the file's region).
func NewManagerFromFile(credsFilePath, authRegion string) *Manager {
	m := &Manager{
		credsFilePath: expandPath(credsFilePath),
		region:        authRegion,
	}
	// Load initial credentials including device registration
	m.loadDeviceRegistration()
	return m
}

// NewManager creates an auth manager with a direct refresh token.
func NewManager(refreshToken, region string) *Manager {
	return &Manager{
		refreshToken: refreshToken,
		region:       region,
	}
}

// Region returns the effective auth region.
func (m *Manager) Region() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.region
}

// FileRegion returns the region from the credentials file (for API host).
func (m *Manager) FileRegion() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fileRegion
}

// refreshRequest is the JSON body sent to the Kiro refresh endpoint.
type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

// oidcRefreshRequest is the JSON body for AWS SSO OIDC refresh.
type oidcRefreshRequest struct {
	GrantType    string `json:"grantType"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	RefreshToken string `json:"refreshToken"`
}

// refreshResponse is the JSON response from the Kiro refresh endpoint.
type refreshResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	ExpiresIn    int    `json:"expiresIn,omitempty"`
	ProfileARN   string `json:"profileArn,omitempty"`
}

// isTokenExpiringSoon checks if the token needs refresh.
// Returns true if token expires within the threshold or if we have no expiration info.
func (m *Manager) isTokenExpiringSoon() bool {
	if m.expiresAt.IsZero() {
		return true
	}
	return time.Until(m.expiresAt) <= TokenRefreshThreshold
}

// isTokenExpired checks if the token is actually expired.
func (m *Manager) isTokenExpired() bool {
	if m.expiresAt.IsZero() {
		return true
	}
	return time.Now().After(m.expiresAt)
}

// GetAccessToken returns a valid access token, refreshing if needed.
// Thread-safe via mutex.
// Implements silent token refresh - automatically refreshes before expiration.
func (m *Manager) GetAccessToken() (string, error) {
	m.mu.Lock()

	// Check if we have a valid token that's not expiring soon
	if m.accessToken != "" && !m.isTokenExpiringSoon() {
		slog.Debug("using cached access token", "expires_at", m.expiresAt.Format(time.RFC3339))
		m.mu.Unlock()
		return m.accessToken, nil
	}

	// In file mode, try to read fresh credentials from file first
	// This handles the case where kiro-cli or another process refreshed the token
	if m.credsFilePath != "" {
		creds, err := m.loadCredsFile()
		if err == nil {
			// Update API region from file
			if creds.Region != "" {
				m.fileRegion = creds.Region
			}

			// If file has a valid access token that's not expiring soon, use it
			if creds.AccessToken != "" {
				expiresAt, err := parseExpiresAt(creds.ExpiresAt)
				if err == nil && time.Until(expiresAt) > TokenRefreshThreshold {
					m.accessToken = creds.AccessToken
					m.expiresAt = expiresAt
					m.refreshToken = creds.RefreshToken
					if creds.ProfileARN != "" {
						m.profileARN = creds.ProfileARN
					}
					slog.Info("using fresh access token from credentials file", "expires_at", expiresAt.Format(time.RFC3339))
					m.mu.Unlock()
					return m.accessToken, nil
				}
			}

			// Update refresh token from file for the refresh attempt
			if creds.RefreshToken != "" {
				m.refreshToken = creds.RefreshToken
			}
			if creds.ProfileARN != "" {
				m.profileARN = creds.ProfileARN
			}
		}
	}

	m.mu.Unlock()

	// Check if we have a refresh token
	if m.refreshToken == "" {
		return "", fmt.Errorf("no refresh token available (run 'kiro-cli login' or check REFRESH_TOKEN env var)")
	}

	slog.Info("refreshing access token (silent refresh)")

	token, expiresAt, newRefreshToken, profileARN, err := m.doRefresh()

	// If refresh failed, try to reload credentials and retry once
	// This handles the case where the refresh token was invalidated (e.g., kiro-cli re-login)
	if err != nil && m.credsFilePath != "" {
		slog.Warn("token refresh failed, reloading credentials file and retrying", "error", err)
		m.mu.Lock()
		creds, readErr := m.loadCredsFile()
		if readErr == nil {
			slog.Debug("reloaded credentials file", 
				"file", m.credsFilePath,
				"has_refresh_token", creds.RefreshToken != "",
				"refresh_token_changed", creds.RefreshToken != m.refreshToken,
				"expires_at", creds.ExpiresAt)
		}
		if readErr == nil && creds.RefreshToken != "" && creds.RefreshToken != m.refreshToken {
			slog.Info("credentials file has a different refresh token, retrying refresh")
			m.refreshToken = creds.RefreshToken
			if creds.Region != "" {
				m.fileRegion = creds.Region
			}
			if creds.ProfileARN != "" {
				m.profileARN = creds.ProfileARN
			}
			m.mu.Unlock()
			token, expiresAt, newRefreshToken, profileARN, err = m.doRefresh()
		} else {
			m.mu.Unlock()
			if readErr == nil && creds.RefreshToken == m.refreshToken {
				slog.Warn("credentials file has the same (stale) refresh token, cannot retry",
					"file", m.credsFilePath)
			}
		}
	}

	// Graceful degradation: if refresh fails but we have a non-expired access token, use it
	if err != nil {
		m.mu.Lock()
		// Only use expired token as last resort if it's VERY recently expired (< 1 min)
		if m.accessToken != "" && !m.isTokenExpired() {
			slog.Warn("token refresh failed but current access token is still valid, using it temporarily",
				"error", err, "expires_at", m.expiresAt.Format(time.RFC3339))
			m.mu.Unlock()
			return m.accessToken, nil
		}
		// Token is actually expired - don't use it, return the refresh error
		if m.accessToken != "" && m.isTokenExpired() {
			slog.Warn("access token has expired and refresh failed, re-authentication required",
				"error", err, "expired_at", m.expiresAt.Format(time.RFC3339))
		}
		m.mu.Unlock()
		
		return "", fmt.Errorf("token refresh failed (you may need to re-authenticate with 'kiro-cli login'): %w", err)
	}

	// Update cached token
	m.mu.Lock()
	m.accessToken = token
	m.expiresAt = expiresAt
	m.lastRefreshTime = time.Now()
	if newRefreshToken != "" {
		m.refreshToken = newRefreshToken
	}
	if profileARN != "" {
		m.profileARN = profileARN
	}

	// Write back to file if in file mode
	if m.credsFilePath != "" {
		m.mu.Unlock()
		if err := m.saveCredsFile(token, newRefreshToken, expiresAt, profileARN); err != nil {
			slog.Warn("failed to write back credentials file", "error", err)
		}
		m.mu.Lock()
	}

	slog.Info("token refreshed", "expires_at", expiresAt.Format(time.RFC3339))
	m.mu.Unlock()
	return m.accessToken, nil
}

// doRefresh routes to the correct refresh flow based on available credentials.
func (m *Manager) doRefresh() (accessToken string, expiresAt time.Time, newRefreshToken string, profileARN string, err error) {
	if m.clientID != "" && m.clientSecret != "" {
		slog.Debug("using AWS SSO OIDC refresh flow")
		return m.doOIDCRefresh()
	}
	slog.Debug("using Kiro Desktop Auth refresh flow")
	return m.doDesktopRefresh()
}

// doDesktopRefresh calls the Kiro Desktop Auth refresh endpoint.
func (m *Manager) doDesktopRefresh() (accessToken string, expiresAt time.Time, newRefreshToken string, profileARN string, err error) {
	refreshURL := fmt.Sprintf("https://prod.%s.auth.desktop.kiro.dev/refreshToken", m.region)

	body, err := json.Marshal(refreshRequest{RefreshToken: m.refreshToken})
	if err != nil {
		return "", time.Time{}, "", "", err
	}

	req, err := http.NewRequest(http.MethodPost, refreshURL, bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, "", "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", time.Time{}, "", "", fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", time.Time{}, "", "", fmt.Errorf("refresh returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result refreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", time.Time{}, "", "", fmt.Errorf("failed to decode refresh response: %w", err)
	}

	if result.AccessToken == "" {
		return "", time.Time{}, "", "", fmt.Errorf("empty access token in response")
	}

	// Parse expiration time
	expAt := time.Now().Add(1 * time.Hour) // default
	if result.ExpiresAt != "" {
		if parsed, err := parseExpiresAt(result.ExpiresAt); err == nil {
			expAt = parsed
		}
	} else if result.ExpiresIn > 0 {
		expAt = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	}

	// Use new refresh token if provided, otherwise keep the old one
	rt := m.refreshToken
	if result.RefreshToken != "" {
		rt = result.RefreshToken
	}

	return result.AccessToken, expAt, rt, result.ProfileARN, nil
}

// doOIDCRefresh calls the AWS SSO OIDC refresh endpoint.
// On 400 error with file mode, reloads credentials and retries once (matches Python kiro-gateway).
func (m *Manager) doOIDCRefresh() (accessToken string, expiresAt time.Time, newRefreshToken string, profileARN string, err error) {
	return m.doOIDCRefreshInternal(false)
}

// doOIDCRefreshInternal performs the actual OIDC refresh with retry logic.
func (m *Manager) doOIDCRefreshInternal(retry bool) (accessToken string, expiresAt time.Time, newRefreshToken string, profileARN string, err error) {
	refreshURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/token", m.region)

	// Build JSON payload (NOT form-urlencoded - AWS SSO OIDC requires JSON)
	reqBody := oidcRefreshRequest{
		GrantType:    "refresh_token",
		ClientID:     m.clientID,
		ClientSecret: m.clientSecret,
		RefreshToken: m.refreshToken,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", time.Time{}, "", "", err
	}

	req, err := http.NewRequest(http.MethodPost, refreshURL, bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, "", "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", time.Time{}, "", "", fmt.Errorf("OIDC refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		
		// Handle 400 error (invalid_grant) - reload credentials and retry once
		// This matches the Python kiro-gateway behavior
		if resp.StatusCode == 400 && !retry && m.credsFilePath != "" {
			slog.Warn("OIDC refresh failed with 400, reloading credentials and retrying", 
				"error", string(respBody))
			
			creds, readErr := m.loadCredsFile()
			if readErr == nil && creds.RefreshToken != "" && creds.RefreshToken != m.refreshToken {
				slog.Info("credentials file has a different refresh token, retrying OIDC refresh")
				m.refreshToken = creds.RefreshToken
				if creds.Region != "" {
					m.fileRegion = creds.Region
				}
				if creds.ProfileARN != "" {
					m.profileARN = creds.ProfileARN
				}
				// Retry with new token
				return m.doOIDCRefreshInternal(true)
			}
			slog.Warn("credentials file has the same refresh token, cannot retry")
		}
		
		return "", time.Time{}, "", "", fmt.Errorf("OIDC refresh returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result refreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", time.Time{}, "", "", fmt.Errorf("failed to decode OIDC refresh response: %w", err)
	}

	if result.AccessToken == "" {
		return "", time.Time{}, "", "", fmt.Errorf("empty access token in OIDC response")
	}

	// Parse expiration time
	expAt := time.Now().Add(1 * time.Hour) // default
	if result.ExpiresIn > 0 {
		expAt = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	} else if result.ExpiresAt != "" {
		if parsed, err := parseExpiresAt(result.ExpiresAt); err == nil {
			expAt = parsed
		}
	}

	return result.AccessToken, expAt, result.RefreshToken, result.ProfileARN, nil
}

// loadCredsFile reads and parses the credentials JSON file.
func (m *Manager) loadCredsFile() (*CredsFile, error) {
	data, err := os.ReadFile(m.credsFilePath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", m.credsFilePath, err)
	}

	var creds CredsFile
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse %s: %w", m.credsFilePath, err)
	}

	return &creds, nil
}

// loadDeviceRegistration loads clientId and clientSecret from the device registration file.
func (m *Manager) loadDeviceRegistration() {
	creds, err := m.loadCredsFile()
	if err != nil {
		slog.Debug("could not load credentials file for device registration", "error", err)
		return
	}

	// If clientId/clientSecret are directly in the creds file, use them
	if creds.ClientID != "" && creds.ClientSecret != "" {
		m.clientID = creds.ClientID
		m.clientSecret = creds.ClientSecret
		m.authType = AuthTypeOIDC
		slog.Debug("loaded OIDC credentials from credentials file")
		return
	}

	// Otherwise, try to load from device registration file using clientIdHash
	if creds.ClientIDHash == "" {
		m.authType = AuthTypeDesktop
		return
	}

	// Device registration file is at ~/.aws/sso/cache/{clientIdHash}.json
	cacheDir := filepath.Dir(m.credsFilePath)
	regFile := filepath.Join(cacheDir, creds.ClientIDHash+".json")

	data, err := os.ReadFile(regFile)
	if err != nil {
		slog.Debug("device registration file not found", "file", regFile, "error", err)
		m.authType = AuthTypeDesktop
		return
	}

	var reg struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		slog.Debug("failed to parse device registration", "error", err)
		m.authType = AuthTypeDesktop
		return
	}

	if reg.ClientID != "" && reg.ClientSecret != "" {
		m.clientID = reg.ClientID
		m.clientSecret = reg.ClientSecret
		m.authType = AuthTypeOIDC
		slog.Info("detected auth type: AWS SSO OIDC (kiro-cli)")
	} else {
		m.authType = AuthTypeDesktop
		slog.Info("detected auth type: Kiro Desktop")
	}
}

// saveCredsFile writes updated tokens back to the credentials file.
func (m *Manager) saveCredsFile(accessToken, refreshToken string, expiresAt time.Time, profileARN string) error {
	// Read existing file to preserve fields we don't manage
	data, err := os.ReadFile(m.credsFilePath)
	if err != nil {
		return err
	}

	// Parse into generic map to preserve unknown fields
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		raw = make(map[string]interface{})
	}

	// Update only the fields we manage
	raw["accessToken"] = accessToken
	raw["refreshToken"] = refreshToken
	raw["expiresAt"] = expiresAt.Format(time.RFC3339)
	if profileARN != "" {
		raw["profileArn"] = profileARN
	}

	// Write back
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(m.credsFilePath, out, 0600)
}

// parseExpiresAt tries multiple time formats for the expiresAt field.
func parseExpiresAt(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
	}

	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("cannot parse time: %s", s)
}

// ForceRefresh forces a token refresh regardless of expiration status.
// This is useful when receiving 403 errors from the API.
func (m *Manager) ForceRefresh() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	slog.Info("forcing token refresh")

	// In file mode, reload credentials first
	if m.credsFilePath != "" {
		creds, err := m.loadCredsFile()
		if err == nil {
			if creds.Region != "" {
				m.fileRegion = creds.Region
			}
			if creds.RefreshToken != "" {
				m.refreshToken = creds.RefreshToken
			}
			if creds.ProfileARN != "" {
				m.profileARN = creds.ProfileARN
			}
		}
	}

	if m.refreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}

	token, expiresAt, newRefreshToken, profileARN, err := m.doRefresh()
	if err != nil {
		return fmt.Errorf("forced refresh failed: %w", err)
	}

	m.accessToken = token
	m.expiresAt = expiresAt
	m.lastRefreshTime = time.Now()
	if newRefreshToken != "" {
		m.refreshToken = newRefreshToken
	}
	if profileARN != "" {
		m.profileARN = profileARN
	}

	// Write back to file if in file mode
	if m.credsFilePath != "" {
		if saveErr := m.saveCredsFile(token, newRefreshToken, expiresAt, profileARN); saveErr != nil {
			slog.Warn("failed to write back credentials file", "error", saveErr)
		}
	}

	slog.Info("token forcibly refreshed", "expires_at", expiresAt.Format(time.RFC3339))
	return nil
}

// BackgroundRefreshInterval is how often to refresh tokens in the background.
// Access tokens expire after 1 hour, so we refresh every 20 minutes to stay well ahead.
const BackgroundRefreshInterval = 20 * time.Minute

// StartBackgroundRefresh launches a goroutine that periodically refreshes tokens.
// This prevents token expiration during idle periods.
// Call StopBackgroundRefresh() to stop the goroutine.
func (m *Manager) StartBackgroundRefresh() {
	m.mu.Lock()
	if m.stopRefresh != nil {
		// Already running
		m.mu.Unlock()
		return
	}
	m.stopRefresh = make(chan struct{})
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(BackgroundRefreshInterval)
		defer ticker.Stop()

		slog.Info("background token refresh started", "interval", BackgroundRefreshInterval)

		for {
			select {
			case <-ticker.C:
				// Force a refresh attempt every interval to keep refresh token valid
				// This is important because kiro-cli refresh tokens can expire after periods of inactivity
				err := m.ForceRefresh()
				if err != nil {
					slog.Warn("background token refresh failed", "error", err)
				} else {
					slog.Debug("background token refresh completed")
				}
			case <-m.stopRefresh:
				slog.Info("background token refresh stopped")
				return
			}
		}
	}()
}

// StopBackgroundRefresh stops the background refresh goroutine.
// Call this during shutdown to ensure clean termination.
func (m *Manager) StopBackgroundRefresh() {
	m.mu.Lock()
	if m.stopRefresh == nil {
		m.mu.Unlock()
		return
	}
	close(m.stopRefresh)
	m.stopRefresh = nil
	m.mu.Unlock()

	m.wg.Wait()
}

// expandPath expands ~ to home directory.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
