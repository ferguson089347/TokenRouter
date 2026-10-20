package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// result.go models Manxue requests and classifies remote observations into
// safe local outcomes. Optional token and duration fields retain absence so
// displays can distinguish missing measurements from zero. Candy correctness
// and drawing quality become a shared verdict while remote execution state
// remains separately available.
//
// Remote failures can contain arbitrary nested data, credentials, and
// identifiers. Decoding inspects known fields within a bounded depth, chooses
// local error categories, and retains only fixed messages plus recognized HTTP
// error codes. Quota reasons take precedence over generic rate-limit wrappers.
// Unknown completed assessments remain unknown so missing evaluation cannot
// become a passing result.

// Response limits bound complete remote JSON and drawing documents before
// callers retain observations or serve model-generated content to browsers.
const (
	MaxResponseBytes = 4 << 20
	MaxHTMLBytes     = 2 << 20
)

var (
	// Remote decoding errors identify invalid or unsafe responses through stable
	// sentinels so callers can classify failures without retaining sensitive remote text.
	ErrInvalidRequest   = errors.New("manxue: invalid request")
	ErrInvalidResponse  = errors.New("manxue: invalid response")
	ErrResponseTooLarge = errors.New("manxue: response exceeds size limit")
	ErrRedirect         = errors.New("manxue: redirect refused")
	ErrTransport        = errors.New("manxue: transport failed")

	// remoteHTTPStatusPattern recognizes explicit HTTP or status markers so
	// unrelated digits in task identifiers cannot become displayed failure codes.
	remoteHTTPStatusPattern = regexp.MustCompile(`(?i)\b(?:http(?:/[0-9](?:\.[0-9])?)?(?:\s+(?:error|status(?:\s+code)?))?|status(?:[_ ]+code)?)(?:\s*[:=]\s*|\s+)([45][0-9]{2})\b`)

	// remoteErrorMessages supplies fixed diagnostic templates for recognized failure
	// categories; arbitrary supplier messages are discarded before these are selected.
	remoteErrorMessages = map[string]string{
		"auth":            "模型接口鉴权失败，请检查检测 Key",
		"permission":      "模型接口拒绝访问，请检查 Key 和模型权限",
		"quota":           "模型接口余额或额度不足",
		"rate_limit":      "模型接口请求受限，请稍后重试",
		"timeout":         "模型接口请求超时",
		"invalid_request": "模型接口不接受检测请求，请检查模型、协议和参数",
		"model":           "模型不存在或不可用，请检查模型名称和分组支持范围",
		"transport":       "检测服务无法连接模型接口",
		"upstream":        "模型接口服务异常，请稍后重试",
		"remote_error":    "检测服务报告执行失败，未提供可安全展示的具体原因",
	}
)

// CreateRequest carries one remote model request including its write-only
// gateway credential.
type CreateRequest struct {
	Benchmark string `json:"benchmark"` // candy or drawing benchmark
	BaseURL   string `json:"base_url"`  // public gateway endpoint
	// APIKey is transmitted in the validated creation body. String formatting hides
	// the credential, and callers keep the complete request outside application logs.
	APIKey          string `json:"api_key"`                    // supplied gateway credential
	Model           string `json:"model"`                      // requested model identifier
	Protocol        string `json:"protocol,omitempty"`         // remote model protocol
	ReasoningEffort string `json:"reasoning_effort,omitempty"` // requested reasoning level
	ServiceTier     string `json:"service_tier,omitempty"`     // requested service tier
}

// TestResult retains known remote observation fields before local privacy and
// verdict processing.
type TestResult struct {
	ID              string         `json:"id"`                         // remote polling credential
	Benchmark       string         `json:"benchmark"`                  // candy or drawing benchmark
	Status          string         `json:"status"`                     // job execution state
	Phase           string         `json:"phase"`                      // remote execution phase
	Model           string         `json:"model,omitempty"`            // requested model identifier
	ReasoningEffort string         `json:"reasoning_effort,omitempty"` // requested reasoning level
	ServiceTier     string         `json:"service_tier,omitempty"`     // requested service tier
	CreatedAt       string         `json:"created_at,omitempty"`       // record creation timestamp
	ExpiresAt       string         `json:"expires_at,omitempty"`       // remote expiration timestamp
	UsageReported   *bool          `json:"usage_reported,omitempty"`   // remote usage confirmation
	Candy           *CandyResult   `json:"candy,omitempty"`            // candy benchmark result
	Result          *DrawingResult `json:"result,omitempty"`           // drawing benchmark result
	Assessment      *Assessment    `json:"assessment,omitempty"`       // drawing quality assessment
	Error           *RemoteError   `json:"error,omitempty"`            // classified execution failure
}

// CandyResult contains optional candy answers, execution diagnostics, and
// reported model measurements.
type CandyResult struct {
	Status          string       `json:"status"`                     // job execution state
	Question        string       `json:"question,omitempty"`         // reported benchmark prompt
	Answer          string       `json:"answer,omitempty"`           // reported model answer
	StartedAt       string       `json:"started_at,omitempty"`       // remote start timestamp
	FinishedAt      string       `json:"finished_at,omitempty"`      // terminal observation timestamp
	DurationMS      *int64       `json:"duration_ms,omitempty"`      // reported duration milliseconds
	InputTokens     *int64       `json:"input_tokens,omitempty"`     // reported input tokens
	OutputTokens    *int64       `json:"output_tokens,omitempty"`    // reported output tokens
	ReasoningTokens *int64       `json:"reasoning_tokens,omitempty"` // reported reasoning tokens
	Error           *RemoteError `json:"error,omitempty"`            // classified execution failure
}

// DrawingResult carries untrusted drawing HTML and optional reported model
// measurements.
type DrawingResult struct {
	HTML            string `json:"html,omitempty"`             // untrusted drawing document
	HasHTML         *bool  `json:"has_html,omitempty"`         // reported drawing presence
	Title           string `json:"title,omitempty"`            // remote drawing title
	Prompt          string `json:"prompt,omitempty"`           // reported drawing prompt
	StartedAt       string `json:"started_at,omitempty"`       // remote start timestamp
	FinishedAt      string `json:"finished_at,omitempty"`      // terminal observation timestamp
	DurationMS      *int64 `json:"duration_ms,omitempty"`      // reported duration milliseconds
	InputTokens     *int64 `json:"input_tokens,omitempty"`     // reported input tokens
	OutputTokens    *int64 `json:"output_tokens,omitempty"`    // reported output tokens
	ReasoningTokens *int64 `json:"reasoning_tokens,omitempty"` // reported reasoning tokens
}

// Assessment describes remote drawing quality and the reason supplied by its
// classifier.
type Assessment struct {
	Quality string `json:"quality"`          // remote drawing classification
	Label   string `json:"label,omitempty"`  // remote assessment label
	Reason  string `json:"reason,omitempty"` // remote classifier explanation
	Source  string `json:"source,omitempty"` // remote classifier identity
}

// RemoteError retains allowlisted diagnostics after discarding arbitrary
// remote error text.
type RemoteError struct {
	Code       string `json:"code"`                  // allowlisted diagnostic category
	Message    string `json:"message"`               // fixed local explanation
	HTTPStatus int    `json:"http_status,omitempty"` // recognized failure status
}

// HTTPError describes a remote HTTP failure using its status and retry delay.
type HTTPError struct {
	StatusCode int           // remote HTTP status
	RetryAfter time.Duration // requested poll delay
}

// TransportError identifies network failure, cancellation, or timeout with
// credential-free diagnostics.
type TransportError struct {
	Timeout  bool // observed timeout classification
	Canceled bool // observed cancellation classification
}

// String returns a fixed redacted description for diagnostic formatting.
func (CreateRequest) String() string { return "manxue.CreateRequest{credentials:redacted}" }

// GoString returns the redacted description used by Go-syntax formatting.
func (r CreateRequest) GoString() string { return r.String() }

// String returns a fixed redacted description for diagnostic formatting.
func (TestResult) String() string { return "manxue.TestResult{task_and_content:redacted}" }

// GoString returns the redacted description used by Go-syntax formatting.
func (r TestResult) GoString() string { return r.String() }

// UnmarshalJSON classifies remote error JSON into a safe local diagnostic.
// Empty placeholders represent no failure, while unknown nonempty shapes
// become the generic remote-error category. Known nested code and message
// fields contribute classification, and explicit HTTP markers contribute
// bounded status codes.
//
// Quota causes take precedence over generic rate-limit wrappers so usage
// exhaustion is explained accurately. Arbitrary remote text is discarded after
// classification, keeping later serialization and logging limited to local
// messages and recognized status codes.
func (e *RemoteError) UnmarshalJSON(data []byte) error {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return ErrInvalidResponse
	}
	*e = RemoteError{}
	if !remoteErrorPresent(value, 0) {
		return nil
	}
	// Inspect raw text only during classification; the resulting struct retains
	// fixed local categories and bounded status codes for later display.
	var codes, messages []string
	collectRemoteError(value, 0, &codes, &messages, &e.HTTPStatus)
	for _, code := range codes {
		category := remoteErrorCategory(code)
		// Prefer a recognized quota cause over a generic rate-limit wrapper because
		// daily or monthly usage exhaustion requires a different explanation.
		if e.Code == "" || (category == "quota" && (e.Code == "rate_limit" || e.Code == "remote_error")) {
			e.Code = category
		}
	}
	for _, message := range messages {
		if e.HTTPStatus == 0 {
			if match := remoteHTTPStatusPattern.FindStringSubmatch(message); len(match) > 1 {
				e.HTTPStatus, _ = strconv.Atoi(match[1])
			}
		}
		category := remoteErrorCategory(message)
		if e.Code == "" || (category == "quota" && (e.Code == "rate_limit" || e.Code == "remote_error")) {
			e.Code = category
		}
	}
	if e.Code == "" {
		switch e.HTTPStatus {
		case 401:
			e.Code = "auth"
		case 403:
			e.Code = "permission"
		case 402:
			e.Code = "quota"
		case 408, 504:
			e.Code = "timeout"
		case 429:
			e.Code = "rate_limit"
		default:
			switch {
			case e.HTTPStatus >= 500:
				e.Code = "upstream"
			case e.HTTPStatus >= 400:
				e.Code = "invalid_request"
			default:
				e.Code = "remote_error"
			}
		}
	}
	e.Message = remoteErrorMessages[e.Code]
	return nil
}

// SafeMessage returns an allowlisted diagnostic and an optional HTTP error
// code. Nil receivers and unknown categories use the generic local message,
// even when the struct was constructed manually.
func (e *RemoteError) SafeMessage() string {
	message := remoteErrorMessages["remote_error"]
	if e == nil {
		return message
	}
	if known, ok := remoteErrorMessages[e.Code]; ok {
		message = known
	}
	if e.HTTPStatus >= 400 && e.HTTPStatus <= 599 {
		message += fmt.Sprintf("（HTTP %d）", e.HTTPStatus)
	}
	return message
}

// remoteErrorPresent distinguishes empty remote placeholders from nonempty
// error values. Inspection stops after the bounded nesting depth and treats
// deeper input as an error, avoiding an unbounded traversal of remote JSON.
func remoteErrorPresent(value any, depth int) bool {
	// Bound inspection of arbitrarily nested supplier error values.
	if depth > 8 {
		return true
	}
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(v) != ""
	case map[string]any:
		for _, item := range v {
			if remoteErrorPresent(item, depth+1) {
				return true
			}
		}
		return false
	case []any:
		for _, item := range v {
			if remoteErrorPresent(item, depth+1) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

// collectRemoteError collects known error fields and a bounded HTTP failure
// status. Traversal stops after eight nested levels; arbitrary headers, URLs
// and other fields cannot contribute diagnostic text or classification.
func collectRemoteError(value any, depth int, codes, messages *[]string, status *int) {
	// Bound inspection of arbitrarily nested supplier error values.
	if depth > 8 {
		return
	}
	switch v := value.(type) {
	case string:
		*messages = append(*messages, v)
	case map[string]any:
		for _, field := range []string{"status", "status_code", "http_status", "statusCode"} {
			if *status != 0 {
				break
			}
			var candidate int
			switch raw := v[field].(type) {
			case float64:
				if raw >= 400 && raw <= 599 && raw == float64(int(raw)) {
					candidate = int(raw)
				}
			case string:
				candidate, _ = strconv.Atoi(strings.TrimSpace(raw))
			}
			if candidate >= 400 && candidate <= 599 {
				*status = candidate
			}
		}
		for _, field := range []string{"code", "type"} {
			if code, ok := v[field].(string); ok {
				*codes = append(*codes, code)
			}
		}
		for _, field := range []string{"error", "message", "detail"} {
			collectRemoteError(v[field], depth+1, codes, messages, status)
		}
	}
}

// remoteErrorCategory maps recognized text patterns to local error category
// names. Unknown strings return empty so the caller can use an HTTP
// classification or the generic diagnostic without retaining remote text.
func remoteErrorCategory(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if _, known := remoteErrorMessages[value]; known {
		return value
	}
	for _, category := range []struct {
		code     string   // expected error category
		patterns []string // recognized diagnostic substrings
	}{
		{"quota", []string{"insufficient_quota", "quota_exceeded", "exceeded your current quota", "billing_hard_limit", "insufficient_balance", "insufficient balance", "daily_limit_exceeded", "weekly_limit_exceeded", "monthly_limit_exceeded", "daily usage limit exceeded", "weekly usage limit exceeded", "monthly usage limit exceeded", "余额不足", "额度不足"}},
		{"model", []string{"model_not_found", "model not found", "model does not exist", "model_not_available", "模型不存在", "模型不可用"}},
		{"auth", []string{"invalid_api_key", "invalid api key", "incorrect api key", "authentication_error", "authentication failed", "unauthorized", "鉴权失败", "认证失败"}},
		{"permission", []string{"permission_denied", "permission denied", "permission_error", "forbidden", "权限不足", "无权访问"}},
		{"rate_limit", []string{"rate_limit", "rate limit", "too many requests", "限流", "请求过于频繁"}},
		{"timeout", []string{"timeout", "timed out", "超时"}},
		{"invalid_request", []string{"invalid_request", "invalid request", "unsupported_parameter", "unsupported parameter", "invalid_parameter", "invalid parameter", "参数无效", "不支持的参数"}},
		{"transport", []string{"connection_error", "connection error", "connection refused", "connection reset", "network error", "dns", "tls handshake", "连接失败", "无法连接"}},
		{"upstream", []string{"server_error", "internal server error", "service_unavailable", "service unavailable", "bad gateway", "服务异常"}},
	} {
		for _, pattern := range category.patterns {
			if strings.Contains(value, pattern) {
				return category.code
			}
		}
	}
	return ""
}

// Error returns a credential-free description of the remote failure.
func (e *HTTPError) Error() string { return fmt.Sprintf("manxue: HTTP %d", e.StatusCode) }

// Error returns a credential-free description of the remote failure.
func (e *TransportError) Error() string {
	if e.Canceled {
		return "manxue: request cancelled"
	}
	if e.Timeout {
		return "manxue: request timed out"
	}
	return ErrTransport.Error()
}

// Unwrap exposes cancellation or timeout identity for typed caller decisions.
// Other transport failures unwrap to ErrTransport, keeping the original URL-
// bearing network error outside the observable error chain.
func (e *TransportError) Unwrap() error {
	if e.Canceled {
		return context.Canceled
	}
	if e.Timeout {
		return context.DeadlineExceeded
	}
	return ErrTransport
}

// Terminal reports whether Manxue marks the job succeeded, failed, or
// cancelled. Unknown states remain nonterminal, so callers enforce their own
// observation deadline before deciding that polling should stop.
func (r *TestResult) Terminal() bool {
	return r != nil && (r.Status == "succeeded" || r.Status == "failed" || r.Status == "cancelled")
}

// Outcome combines execution state with the benchmark-specific assessment into
// a display verdict. Pending execution stays pending, failed or cancelled
// execution becomes error, and successful execution still requires a
// recognized assessment before passing. Candy error fields take precedence
// over correctness, while drawing quality determines the drawing verdict.
//
// Missing results and unfamiliar values return unknown. This distinction lets
// consumers display incomplete evaluation separately from a model answering
// incorrectly or a supplier request failing.
func (r *TestResult) Outcome() string {
	if r == nil {
		return "unknown"
	}
	if r.Status == "running" || r.Status == "queued" || r.Status == "pending" {
		return "pending"
	}
	if r.Status == "failed" || r.Status == "cancelled" {
		return "error"
	}
	if r.Status != "succeeded" {
		return "unknown"
	}
	if r.Error != nil && r.Error.Code != "" {
		return "error"
	}
	switch r.Benchmark {
	case "candy":
		if r.Candy == nil {
			return "unknown"
		}
		if r.Candy.Error != nil && r.Candy.Error.Code != "" {
			return "error"
		}
		switch r.Candy.Status {
		case "passed":
			return "passed"
		case "incorrect":
			return "failed"
		case "error":
			return "error"
		}
	case "pelican":
		if r.Assessment == nil {
			return "unknown"
		}
		switch r.Assessment.Quality {
		case "normal":
			return "passed"
		case "degraded", "suspicious":
			return "failed"
		}
	}
	return "unknown"
}
