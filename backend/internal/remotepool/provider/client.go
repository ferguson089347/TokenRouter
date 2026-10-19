package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/remotepool"
)

// client.go exchanges bounded JSON requests with a configured remote pool site.
// Remote credentials travel in the request body or bearer header, so redirects
// are rejected and failures return status information with remote bodies omitted.

// Client implements remote pool JSON exchange through a bounded HTTP client.
type Client struct {
	http *http.Client // bounded source request transport
}

// NewClient constructs a transport with a twenty-second request timeout.
// Redirects are returned as failed responses so bearer credentials cannot be sent
// to a destination different from the configured site.
func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		// Remote responses cannot redirect credentials to another destination.
		// Return the response for normal status validation and sanitization.
		return http.ErrUseLastResponse
	}}}
}

// Request sends JSON to the selected site and decodes its successful envelope.
// Responses are limited to two mebibytes. HTTP and application errors expose
// status and challenge detection through HTTPError while omitting source content.
func (c *Client) Request(ctx context.Context, baseURL string, method, path string, payload any, token string, out any) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+"/api/v1"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TokenRouter-RemoteAccountPool/1.0")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	// Close every source response after reading or validation failure so the
	// transport can reuse connections and release response resources.
	defer func() { _ = resp.Body.Close() }()
	// Read one byte beyond the response limit to distinguish a complete bounded
	// envelope from silently truncated JSON containing incomplete account data.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 2<<20 {
		return errors.New("remote response too large")
	}
	var envelope struct {
		Code    int             `json:"code"`    // source application response code
		Message string          `json:"message"` // source supplied error message
		Reason  string          `json:"reason"`  // source error reason label
		Data    json.RawMessage `json:"data"`    // encoded account or session payload
	}
	parseErr := json.Unmarshal(raw, &envelope)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || envelope.Code != 0 {
		message := strings.ToLower(envelope.Message + " " + envelope.Reason)
		status := resp.StatusCode
		if status >= 200 && status < 300 && envelope.Code >= 400 && envelope.Code <= 599 {
			status = envelope.Code
		}
		return &remotepool.HTTPError{Status: status, Captcha: strings.Contains(message, "turnstile") || strings.Contains(message, "captcha")}
	}
	if parseErr != nil || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("invalid remote response")
	}
	return json.Unmarshal(envelope.Data, out)
}
