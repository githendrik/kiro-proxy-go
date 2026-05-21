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

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
	profileARN  string
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

// GetAccessToken returns a valid access token, refreshing if needed.
// Thread-safe via mutex.
func (m *Manager) GetAccessToken() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// In file mode, first try to read a fresh token from the file
	// (kiro-cli may have refreshed it)
	if m.credsFilePath != "" {
		creds, err := m.loadCredsFile()
		if err != nil {
			return "", fmt.Errorf("failed to read credentials file: %w", err)
		}

		// Update API region from file (but NOT auth region — that stays as configured)
		if creds.Region != "" {
			m.fileRegion = creds.Region
		}

		// Use refresh token from file
		m.refreshToken = creds.RefreshToken

		// Pick up profileArn from file if present
		if creds.ProfileARN != "" {
			m.profileARN = creds.ProfileARN
		}

		// If the file has a valid access token, use it directly
		if creds.AccessToken != "" {
			expiresAt, err := parseExpiresAt(creds.ExpiresAt)
			if err == nil && time.Until(expiresAt) > 10*time.Minute {
				m.accessToken = creds.AccessToken
				m.expiresAt = expiresAt
				slog.Info("using access token from credentials file", "expires_at", expiresAt.Format(time.RFC3339))
				return m.accessToken, nil
			}
		}
	}

	if m.refreshToken == "" {
		return "", fmt.Errorf("no refresh token available (check credentials file or REFRESH_TOKEN env var)")
	}

	slog.Info("refreshing access token")

	token, expiresAt, newRefreshToken, profileARN, err := m.doRefresh()

	// If refresh failed and we're in file mode, re-read the creds file and retry once.
	// This handles the case where another process (e.g. kiro-cli or another proxy)
	// rotated the refresh token, making our in-memory copy stale.
	if err != nil && m.credsFilePath != "" {
		slog.Warn("token refresh failed, re-reading credentials file and retrying once", "error", err)
		creds, readErr := m.loadCredsFile()
		if readErr == nil && creds.RefreshToken != "" && creds.RefreshToken != m.refreshToken {
			slog.Info("credentials file has a newer refresh token, retrying refresh")
			m.refreshToken = creds.RefreshToken
			if creds.Region != "" {
				m.fileRegion = creds.Region
			}
			if creds.ProfileARN != "" {
				m.profileARN = creds.ProfileARN
			}
			token, expiresAt, newRefreshToken, profileARN, err = m.doRefresh()
		}
	}

	// Graceful degradation: if refresh still fails but we have an access token
	// that hasn't actually expired yet, use it as a fallback.
	if err != nil {
		if m.accessToken != "" && time.Until(m.expiresAt) > 0 {
			slog.Warn("token refresh failed but current access token is still valid, using it as fallback",
				"error", err, "expires_at", m.expiresAt.Format(time.RFC3339))
			return m.accessToken, nil
		}
		return "", fmt.Errorf("token refresh failed: %w", err)
	}

	m.accessToken = token
	m.expiresAt = expiresAt
	if profileARN != "" {
		m.profileARN = profileARN
	}

	// Write back to file if in file mode
	if m.credsFilePath != "" {
		if err := m.saveCredsFile(token, newRefreshToken, expiresAt, profileARN); err != nil {
			slog.Warn("failed to write back credentials file", "error", err)
		}
	}

	slog.Info("token refreshed", "expires_at", expiresAt.Format(time.RFC3339))
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
func (m *Manager) doOIDCRefresh() (accessToken string, expiresAt time.Time, newRefreshToken string, profileARN string, err error) {
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
