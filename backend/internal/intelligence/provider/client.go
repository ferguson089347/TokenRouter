package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/intelligence/remote"
)

// client.go submits and polls Manxue checks through a bounded HTTP client.
// Production construction selects the fixed service origin, while local
// fixtures can supply a validated loopback endpoint. Caller clients are copied
// before their timeout, cookie handling, and redirect policy are adjusted.
//
// Creation carries the saved local run identity as an idempotency key and
// disables request-body replay, because an uncertain POST may already have
// caused a charged gateway call. Responses are read within byte limits and
// checked against the expected task identity before returning. Transport
// errors expose cancellation and timeout classifications while discarding URL-
// bearing network text.

// Client defaults select the production origin and bound each HTTP exchange
// when callers provide no stricter timeout through their source client.
const (
	DefaultBaseURL = "https://manxue.ai"
	defaultTimeout = 45 * time.Second
)

// Identifier expressions constrain task paths and idempotency headers to bounded
// alphabets so remote identifiers cannot alter request routing or header syntax.
var (
	idPattern          = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
	idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

// Client submits and polls checks using bounded, redirect-disabled HTTP
// requests.
type Client struct {
	httpClient *http.Client // bounded HTTP client
	baseURL    string       // fixed service origin
}

// NewClient creates a client for the fixed production Manxue origin. A
// supplied HTTP client is copied; absent timeouts use the bounded default, and
// redirects plus cookie propagation are disabled.
func NewClient(httpClient *http.Client) *Client {
	return newClient(httpClient, DefaultBaseURL)
}

// NewClientWithBaseURL creates a client for validated fixture or HTTPS
// origins. Plain HTTP requires localhost or a loopback IP, and credentials,
// non-root paths, queries, and fragments are rejected.
func NewClientWithBaseURL(httpClient *http.Client, baseURL string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, remote.ErrInvalidRequest
	}
	if u.Scheme != "https" && (u.Scheme != "http" || u.Hostname() != "localhost" && !net.ParseIP(u.Hostname()).IsLoopback()) {
		return nil, remote.ErrInvalidRequest
	}
	return newClient(httpClient, strings.TrimRight(baseURL, "/")), nil
}

// newClient copies caller HTTP options and applies bounded request isolation.
// The returned client shares the caller's transport but owns its client
// configuration, disables cookies, and rejects redirects before credentials
// can be forwarded.
func newClient(source *http.Client, baseURL string) *Client {
	var cloned http.Client
	if source != nil {
		cloned = *source
	}
	if cloned.Timeout <= 0 {
		cloned.Timeout = defaultTimeout
	}
	// Disable cookies and every redirect on the copied client so model credentials
	// remain confined to the configured creation endpoint.
	cloned.Jar = nil
	cloned.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{httpClient: &cloned, baseURL: baseURL}
}

// Create submits one validated check with the supplied persistent idempotency
// identity. An omitted protocol selects Responses, and the benchmark controls
// the allowed protocol and reasoning values. Request replay is disabled before
// transmission because a connection failure can leave acceptance uncertain.
//
// An accepted response must contain a valid task identifier and a compatible
// benchmark. Omitted response benchmark uses the submitted value, allowing the
// service to interpret later observations consistently.
func (c *Client) Create(ctx context.Context, input remote.CreateRequest, idempotencyKey string) (*remote.TestResult, error) {
	if input.Protocol == "" {
		input.Protocol = "responses"
	}
	if err := validateCreate(input, idempotencyKey); err != nil {
		return nil, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, remote.ErrInvalidRequest
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/tests", bytes.NewReader(body))
	if err != nil {
		return nil, remote.ErrInvalidRequest
	}
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	// Disable body replay before sending because transport recovery could
	// otherwise repeat an accepted creation and cause another charged gateway
	// request.
	req.GetBody = nil
	result, err := c.do(req, http.StatusAccepted)
	if err != nil {
		return nil, err
	}
	if !ValidTaskID(result.ID) || (result.Benchmark != "" && result.Benchmark != input.Benchmark) {
		return nil, remote.ErrInvalidResponse
	}
	if result.Benchmark == "" {
		result.Benchmark = input.Benchmark
	}
	return result, nil
}

// Get polls a validated task identifier and checks response identity before
// returning it. Path separators and query syntax are rejected, and an omitted
// response ID uses the identifier requested by the caller.
func (c *Client) Get(ctx context.Context, id string) (*remote.TestResult, error) {
	if !ValidTaskID(id) {
		return nil, remote.ErrInvalidRequest
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/tests/"+id, nil)
	if err != nil {
		return nil, remote.ErrInvalidRequest
	}
	result, err := c.do(req, http.StatusOK)
	if err != nil {
		return nil, err
	}
	if result.ID != "" && result.ID != id {
		return nil, remote.ErrInvalidResponse
	}
	result.ID = id
	return result, nil
}

// ValidTaskID reports whether a task identifier fits the bounded path-safe
// alphabet.
func ValidTaskID(id string) bool { return idPattern.MatchString(id) }

// validateCreate checks benchmark, protocol, credential, model, URL and
// optional execution parameters. Invalid values return the same request error
// before transmission; credentials remain available solely for the validated
// creation body.
func validateCreate(input remote.CreateRequest, key string) error {
	if input.Benchmark != "candy" && input.Benchmark != "pelican" {
		return remote.ErrInvalidRequest
	}
	if input.Protocol != "responses" && input.Protocol != "chat_completions" {
		return remote.ErrInvalidRequest
	}
	if input.Benchmark == "candy" && input.Protocol != "responses" {
		return remote.ErrInvalidRequest
	}
	if key != "" && !idempotencyPattern.MatchString(key) {
		return remote.ErrInvalidRequest
	}
	// Bound credential and model lengths before serializing remote input.
	if strings.TrimSpace(input.APIKey) == "" || len(input.APIKey) > 8192 || strings.TrimSpace(input.Model) == "" || len(input.Model) > 256 {
		return remote.ErrInvalidRequest
	}
	u, err := url.Parse(input.BaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(input.BaseURL) > 2048 {
		return remote.ErrInvalidRequest
	}
	switch input.ReasoningEffort {
	case "", "low", "medium", "high":
	case "xhigh", "max", "ultra":
		if input.Benchmark != "candy" {
			return remote.ErrInvalidRequest
		}
	default:
		return remote.ErrInvalidRequest
	}
	if input.ServiceTier != "" && input.ServiceTier != "priority" && input.ServiceTier != "ultrafast" {
		return remote.ErrInvalidRequest
	}
	return nil
}

// do sends a request and decodes its bounded successful JSON response.
// Redirects and unexpected status codes become safe errors; response-body and
// HTML limits are checked before returning complete observations to callers.
func (c *Client) do(req *http.Request, expectedStatus int) (*remote.TestResult, error) {
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, safeTransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, remote.ErrRedirect
	}
	if resp.StatusCode != expectedStatus {
		return nil, &remote.HTTPError{StatusCode: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
	// Read one extra byte to distinguish oversized content from a complete
	// response, preventing a truncated document from being accepted as a result.
	body, err := io.ReadAll(io.LimitReader(resp.Body, remote.MaxResponseBytes+1))
	if err != nil {
		return nil, safeTransportError(err)
	}
	if len(body) > remote.MaxResponseBytes {
		return nil, remote.ErrResponseTooLarge
	}
	var result remote.TestResult
	if err := json.Unmarshal(body, &result); err != nil || result.Status == "" {
		return nil, remote.ErrInvalidResponse
	}
	if result.Result != nil && len(result.Result.HTML) > remote.MaxHTMLBytes {
		return nil, remote.ErrResponseTooLarge
	}
	return &result, nil
}

// safeTransportError retains cancellation and timeout classifications from
// network failures. The original error text is discarded because request URLs
// can contain private task identifiers, including failures encountered while
// reading response bodies.
func safeTransportError(err error) *remote.TransportError {
	var timeout net.Error
	return &remote.TransportError{Canceled: errors.Is(err, context.Canceled), Timeout: errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout())}
}

// parseRetryAfter accepts delta seconds or a future HTTP timestamp and returns
// a bounded delay. Invalid, past or nonpositive values yield zero; the maximum
// is one day for caller scheduling.
func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		// Limit supplier delay hints to one day.
		if seconds > 86400 {
			seconds = 86400
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		delay := when.Sub(now)
		if delay > 24*time.Hour {
			return 24 * time.Hour
		}
		return delay
	}
	return 0
}
