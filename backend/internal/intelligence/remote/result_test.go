package remote

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestOutcomeNeverTreatsUnknownAsPassed combines recognized, missing and
// unfamiliar execution or assessment values. Unknown observations remain
// unknown and reported execution failures take precedence over otherwise
// passing benchmark fields.
func TestOutcomeNeverTreatsUnknownAsPassed(t *testing.T) {
	cases := []struct {
		name     string     // fixture scenario name
		result   TestResult // remote fixture observation
		outcome  string     // expected normalized verdict
		terminal bool       // expected terminal state
	}{
		{"unknown task", TestResult{Status: "future_status", Benchmark: "candy", Candy: &CandyResult{Status: "passed"}}, "unknown", false},
		{"unknown candy", TestResult{Status: "succeeded", Benchmark: "candy", Candy: &CandyResult{Status: "future_status"}}, "unknown", true},
		{"unknown quality", TestResult{Status: "succeeded", Benchmark: "pelican", Assessment: &Assessment{Quality: "future_status"}}, "unknown", true},
		{"missing candy", TestResult{Status: "succeeded", Benchmark: "candy"}, "unknown", true},
		{"missing assessment", TestResult{Status: "succeeded", Benchmark: "pelican"}, "unknown", true},
		{"incorrect", TestResult{Status: "succeeded", Benchmark: "candy", Candy: &CandyResult{Status: "incorrect"}}, "failed", true},
		{"candy error", TestResult{Status: "succeeded", Benchmark: "candy", Candy: &CandyResult{Status: "error"}}, "error", true},
		{"failed", TestResult{Status: "failed", Benchmark: "candy", Candy: &CandyResult{Status: "passed"}}, "error", true},
		{"cancelled", TestResult{Status: "cancelled"}, "error", true},
		{"normal", TestResult{Status: "succeeded", Benchmark: "pelican", Assessment: &Assessment{Quality: "normal"}}, "passed", true},
		{"remote error overrides pass", TestResult{Status: "succeeded", Benchmark: "candy", Candy: &CandyResult{Status: "passed"}, Error: &RemoteError{Code: "remote_error"}}, "error", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.result.Outcome() != tc.outcome || tc.result.Terminal() != tc.terminal {
				t.Fatal("状态映射错误")
			}
		})
	}
}

// TestRemoteErrorDoesNotRetainCredentials decodes errors containing arbitrary
// credentials, URLs, authentication headers and task identities. Serialization
// and safe formatting remove remote text, including manually constructed
// errors and invalid status codes.
func TestRemoteErrorDoesNotRetainCredentials(t *testing.T) {
	// Include arbitrary bearer tokens, credential URLs and remote task identifiers
	// to exercise redaction beyond recognizable local sk-prefixed credentials.
	private := "Bearer private-token-abc https://u:p@private.example/v1?api_key=private-query task_private_123 sk-test-secret-1234567890"
	for _, input := range []any{
		"HTTP 401 " + private,
		map[string]any{"code": private, "message": private},
		map[string]any{"error": map[string]any{"code": "rate_limit_exceeded", "message": private}, "headers": map[string]string{"Authorization": private}},
	} {
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var remote RemoteError
		if err := json.Unmarshal(body, &remote); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(remote)
		for _, output := range []string{string(encoded), fmt.Sprintf("%+v", remote), remote.SafeMessage()} {
			if strings.Contains(output, "private") || strings.Contains(output, "sk-test") || strings.Contains(output, "Bearer") || strings.Contains(output, "https:") {
				t.Fatal("解析后仍保留敏感错误正文")
			}
		}
	}
	// Construct the diagnostic manually to verify SafeMessage still selects fixed
	// local text rather than exposing arbitrary assigned message values.
	manual := &RemoteError{Code: private, Message: private, HTTPStatus: 401}
	if message := manual.SafeMessage(); strings.Contains(message, "private") || !strings.Contains(message, "HTTP 401") {
		t.Fatal("手工构造的错误未被安全格式化")
	}
	manual.HTTPStatus = 12345
	if strings.Contains(manual.SafeMessage(), "12345") {
		t.Fatal("非法状态码不应回显")
	}
}

// TestEmptyRemoteErrorsDoNotOverrideSuccessfulResults decodes null, empty
// strings and nested empty error placeholders for both benchmarks. Successful
// candy answers and normal drawing assessments continue to produce passing
// verdicts.
func TestEmptyRemoteErrorsDoNotOverrideSuccessfulResults(t *testing.T) {
	for _, empty := range []string{`null`, `""`, `"  "`, `{}`, `{"code":"","message":" "}`, `{"error":null}`, `{"error":{}}`} {
		for _, benchmark := range []string{"candy", "pelican"} {
			t.Run(benchmark+empty, func(t *testing.T) {
				var result TestResult
				body := fmt.Sprintf(`{"benchmark":%q,"status":"succeeded","error":%s,"candy":{"status":"passed","error":%s},"assessment":{"quality":"normal"}}`, benchmark, empty, empty)
				if err := json.Unmarshal([]byte(body), &result); err != nil {
					t.Fatal(err)
				}
				if result.Outcome() != "passed" {
					t.Fatal("空错误占位符覆盖了通过状态")
				}
			})
		}
	}
}

// TestRemoteErrorSafeDiagnostics decodes varied nested remote errors, HTTP
// markers and quota explanations. Only recognized categories and status codes
// survive, with usage exhaustion taking precedence over generic rate-limit
// wrappers.
func TestRemoteErrorSafeDiagnostics(t *testing.T) {
	cases := []struct {
		name   string // fixture scenario name
		body   string // remote response JSON
		code   string // expected error category
		status int    // expected HTTP status
	}{
		{"string auth", `"HTTP 401: incorrect API key"`, "auth", 401},
		{"object permission", `{"status_code":403,"error":{"code":"permission_denied","message":"private detail"}}`, "permission", 403},
		{"quota overrides rate status", `{"status":429,"error":{"type":"insufficient_quota","message":"private detail"}}`, "quota", 429},
		{"quota overrides rate wrapper code", `{"status":429,"code":"rate_limit_error","error":{"code":"DAILY_LIMIT_EXCEEDED"}}`, "quota", 429},
		{"quota overrides rate wrapper message", `{"status":429,"code":"rate_limit_error","message":"daily usage limit exceeded"}`, "quota", 429},
		// Daily, weekly and monthly usage exhaustion can carry HTTP 429, so the
		// diagnostic must retain the recognized quota cause.
		{"observed daily quota error", `"error: code=429 reason=\"DAILY_LIMIT_EXCEEDED\" message=\"daily usage limit exceeded\" metadata=map[]"`, "quota", 0},
		{"daily quota code", `{"status":429,"error":{"code":"DAILY_LIMIT_EXCEEDED"}}`, "quota", 429},
		{"daily quota message", `"HTTP 429: daily usage limit exceeded"`, "quota", 429},
		{"weekly quota code", `{"status":429,"error":{"code":"WEEKLY_LIMIT_EXCEEDED"}}`, "quota", 429},
		{"weekly quota message", `"HTTP 429: weekly usage limit exceeded"`, "quota", 429},
		{"monthly quota code", `{"status":429,"error":{"code":"MONTHLY_LIMIT_EXCEEDED"}}`, "quota", 429},
		{"monthly quota message", `"HTTP 429: monthly usage limit exceeded"`, "quota", 429},
		{"team member quota code", `{"status":429,"error":{"code":"TEAM_MEMBER_DAILY_LIMIT_EXCEEDED"}}`, "quota", 429},
		{"string rate limit", `"status_code=429 Too Many Requests"`, "rate_limit", 429},
		{"structured timeout", `{"http_status":"504","message":"private detail"}`, "timeout", 504},
		{"string timeout", `"Request timed out"`, "timeout", 0},
		{"invalid parameters", `{"error":{"code":"unsupported_parameter","message":"private detail"}}`, "invalid_request", 0},
		{"unknown 404", `{"status_code":404,"message":"private detail"}`, "invalid_request", 404},
		{"missing model", `{"status":404,"error":{"code":"model_not_found"}}`, "model", 404},
		{"transport", `"connection refused https://private.example"`, "transport", 0},
		{"HTTP protocol response", `"HTTP/1.1 502 Bad Gateway"`, "upstream", 502},
		{"HTTP error marker", `"HTTP error:503 private detail"`, "upstream", 503},
		{"unknown text", `"private detail"`, "remote_error", 0},
		{"task numbers are not status", `"task_401_429_503 failed after 504 seconds"`, "remote_error", 0},
		{"code numbers are not status", `{"code":"401","message":"task_503"}`, "remote_error", 0},
		{"arbitrary field ignored", `{"request_id":"HTTP 401","url":"https://private.example/503","headers":{"status":403}}`, "remote_error", 0},
		{"status outside error range", `{"status":200,"message":"private detail"}`, "remote_error", 0},
		{"fractional status ignored", `{"status_code":401.5,"message":"private detail"}`, "remote_error", 0},
		{"unknown shape", `["private detail"]`, "remote_error", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var remote RemoteError
			if err := json.Unmarshal([]byte(tc.body), &remote); err != nil {
				t.Fatal(err)
			}
			if remote.Code != tc.code || remote.HTTPStatus != tc.status || remote.Message != (&RemoteError{Code: tc.code}).SafeMessage() {
				t.Fatalf("安全错误分类不匹配: %+v", remote)
			}
			encoded, err := json.Marshal(remote)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"private detail", "private.example", "task_", "headers", "request_id"} {
				if strings.Contains(string(encoded), secret) || strings.Contains(remote.SafeMessage(), secret) {
					t.Fatal("安全错误回显了原文")
				}
			}
			if tc.status != 0 && !strings.Contains(remote.SafeMessage(), fmt.Sprintf("HTTP %d", tc.status)) {
				t.Fatal("安全诊断缺少明确状态码")
			}
		})
	}
}
