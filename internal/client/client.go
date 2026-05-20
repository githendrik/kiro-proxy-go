package client

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/user"
	"time"

	"github.com/google/uuid"

	"kiro-proxy-go/internal/auth"
)

// Client wraps HTTP calls to the Kiro API with retry logic.
type Client struct {
	httpClient  *http.Client
	authManager *auth.Manager
	apiHost     string
	maxRetries  int
	fingerprint string
}

// NewClient creates a new Kiro API client.
func NewClient(authManager *auth.Manager, apiHost string, maxRetries int, streamTimeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: streamTimeout,
		},
		authManager: authManager,
		apiHost:     apiHost,
		maxRetries:  maxRetries,
		fingerprint: generateFingerprint(),
	}
}

// retryableStatuses are HTTP status codes that warrant a retry.
var retryableStatuses = map[int]bool{
	403: true,
	429: true,
	500: true,
	502: true,
	503: true,
	504: true,
}

// DoStream sends a request to the Kiro API and returns the raw response body
// for streaming. The caller is responsible for closing the body.
// Retries on transient errors with exponential backoff.
func (c *Client) DoStream(method, path string, body io.Reader, getBody func() io.Reader) (*http.Response, error) {
	url := c.apiHost + path

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			slog.Debug("retrying request", "attempt", attempt, "delay", delay)
			time.Sleep(delay)
			// Reset body for retry
			body = getBody()
		}

		token, err := c.authManager.GetAccessToken()
		if err != nil {
			return nil, fmt.Errorf("auth failed: %w", err)
		}

		req, err := http.NewRequest(method, url, body)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		// Set Kiro API headers
		c.setKiroHeaders(req, token)

		// Log request for debugging
		slog.Debug("Kiro API request", "url", url, "method", method)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			slog.Warn("request error, will retry", "attempt", attempt, "error", err)
			continue
		}

		if retryableStatuses[resp.StatusCode] {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
			slog.Warn("retryable status", "attempt", attempt, "status", resp.StatusCode)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
		}

		return resp, nil
	}

	return nil, fmt.Errorf("all %d retries exhausted: %w", c.maxRetries, lastErr)
}

// DoJSON sends a request and returns the response (non-streaming).
// Same retry logic as DoStream.
func (c *Client) DoJSON(method, path string, body io.Reader, getBody func() io.Reader) ([]byte, error) {
	resp, err := c.DoStream(method, path, body, getBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// setKiroHeaders sets the required headers on a request for q.amazonaws.com endpoint.
func (c *Client) setKiroHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("aws-sdk-js/1.0.27 ua/2.1 os/darwin lang/js md/nodejs#22.21.1 api/codewhispererstreaming#1.0.27 m/E KiroIDE-0.7.45-%s", c.fingerprint))
	req.Header.Set("x-amz-user-agent", fmt.Sprintf("aws-sdk-js/1.0.27 KiroIDE-0.7.45-%s", c.fingerprint))
	req.Header.Set("x-amzn-codewhisperer-optout", "true")
	req.Header.Set("x-amzn-kiro-agent-mode", "vibe")
	req.Header.Set("amz-sdk-invocation-id", uuid.New().String())
	req.Header.Set("amz-sdk-request", "attempt=1; max=3")
	req.Header.Set("Connection", "close")
}

// generateFingerprint creates a unique machine fingerprint.
func generateFingerprint() string {
	hostname, _ := os.Hostname()
	u, _ := user.Current()
	username := ""
	if u != nil {
		username = u.Username
	}

	data := fmt.Sprintf("%s-%s-kiro-gateway", hostname, username)
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash[:8])
}
