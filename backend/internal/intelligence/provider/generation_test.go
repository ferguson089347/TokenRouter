package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/intelligence/remote"
)

// TestGenerateGatewayProtocols sends configured models and prompts to a local
// TLS gateway fixture. Both protocols retain HTML and usage, and the streaming
// Responses terminal object contributes its text exactly once.
func TestGenerateGatewayProtocols(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions"} {
		t.Run(protocol, func(t *testing.T) {
			html := "<!DOCTYPE html><html><body>local artwork</body></html>"
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer sk-generation-fixture", r.Header.Get("Authorization"))
				require.Empty(t, r.Header.Get("Cookie"))
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, "configured-model", body["model"])
				require.Equal(t, true, body["stream"])
				require.Equal(t, "priority", body["service_tier"])
				if protocol == "responses" {
					require.Equal(t, "/v1/responses", r.URL.Path)
					require.Equal(t, "custom drawing prompt", body["input"])
					require.Equal(t, map[string]any{"effort": "high"}, body["reasoning"])
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", html)
					fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"error\":null,\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}],\"usage\":{\"input_tokens\":11,\"output_tokens\":22,\"output_tokens_details\":{\"reasoning_tokens\":0}}}}\n\n", html)
					return
				}
				require.Equal(t, "/v1/chat/completions", r.URL.Path)
				require.Equal(t, "high", body["reasoning_effort"])
				require.Equal(t, []any{map[string]any{"role": "user", "content": "custom drawing prompt"}}, body["messages"])
				fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":22,"completion_tokens_details":{"reasoning_tokens":0}}}`, html)
			}))
			t.Cleanup(server.Close)
			baseTransport, ok := server.Client().Transport.(*http.Transport)
			require.True(t, ok)
			transport := baseTransport.Clone()
			transport.TLSClientConfig.ServerName = "example.com"
			// Route the public fixture hostname to the isolated TLS listener while
			// exercising the production gateway URL validation and HTTP request builder.
			transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			t.Cleanup(transport.CloseIdleConnections)
			client := NewClient(&http.Client{Transport: transport})
			input := remote.CreateRequest{
				Benchmark:       "pelican",
				BaseURL:         "https://gateway.example/v1",
				APIKey:          "sk-generation-fixture",
				Model:           "configured-model",
				Protocol:        protocol,
				ReasoningEffort: "high",
				ServiceTier:     "priority",
			}
			result, err := client.Generate(context.Background(), input, "custom drawing prompt")
			require.NoError(t, err)
			require.Equal(t, html, result.HTML)
			require.Equal(t, "custom drawing prompt", result.Prompt)
			require.EqualValues(t, 11, *result.InputTokens)
			require.EqualValues(t, 22, *result.OutputTokens)
			require.Zero(t, *result.ReasoningTokens)
			require.NotNil(t, result.DurationMS)
			if protocol == "responses" {
				require.NotNil(t, result.FirstTokenMS)
			} else {
				require.Nil(t, result.FirstTokenMS)
			}
		})
	}
}

// TestGenerationFirstTokenTiming delays output after early lifecycle events and
// empty deltas. Both protocols measure the first nonempty model delta separately
// from stream completion, while buffered JSON leaves first-token timing absent.
func TestGenerationFirstTokenTiming(t *testing.T) {
	for _, tc := range []struct {
		protocol string // selected generation protocol
		initial  string // early noncontent events
		first    string // first actual token event
	}{
		{"responses", "data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"\"}\n\n", "data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"thinking\"}\n\n"},
		{"chat_completions", "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"<html>\"}}]}\n\n"},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				reader, writer := io.Pipe()
				defer func() { _ = reader.Close() }()
				// Emit lifecycle data before delaying the first real delta, then keep the
				// stream open so measuring completion would produce a distinct incorrect value.
				go func() {
					defer func() { _ = writer.Close() }()
					_, _ = io.WriteString(writer, tc.initial)
					time.Sleep(1813 * time.Millisecond)
					_, _ = io.WriteString(writer, tc.first)
					time.Sleep(4 * time.Second)
					_, _ = io.WriteString(writer, "data: [DONE]\n\n")
				}()
				started := time.Now()
				raw, firstToken, err := readGenerationBody(reader, tc.protocol, started)
				require.NoError(t, err)
				require.NotEmpty(t, raw)
				require.NotNil(t, firstToken)
				require.EqualValues(t, 1813, *firstToken)
				require.Equal(t, 5813*time.Millisecond, time.Since(started))
			})
		})
	}
	_, firstToken, err := readGenerationBody(strings.NewReader(`{"status":"completed"}`), "responses", time.Now())
	require.NoError(t, err)
	require.Nil(t, firstToken)
}

// TestGenerateSafeHTTPDiagnostics returns gateway rejection bodies through an
// isolated TLS endpoint. Quota errors retain their safe category and HTTP status,
// while arbitrary messages and credential text cannot enter the returned error.
func TestGenerateSafeHTTPDiagnostics(t *testing.T) {
	for _, body := range []string{
		`{"code":"INSUFFICIENT_BALANCE","message":"Insufficient account balance sk-private-secret"}`,
		`{"error":{"code":"insufficient_quota","message":"sk-private-secret"}}`,
		`{"code":"unknown","message":"sk-private-secret"}`,
		`<html>sk-private-secret</html>`,
	} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(server.Close)
		transport, ok := server.Client().Transport.(*http.Transport)
		require.True(t, ok)
		transport = transport.Clone()
		transport.TLSClientConfig.ServerName = "example.com"
		// Connect the public gateway fixture name to the local listener so these
		// diagnostic checks exercise the same URL restrictions as real generation requests.
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		}
		t.Cleanup(transport.CloseIdleConnections)
		client := NewClient(&http.Client{Transport: transport})
		_, err := client.Generate(context.Background(), remote.CreateRequest{
			Benchmark: "pelican", BaseURL: "https://gateway.example/v1",
			APIKey: "fixture-key", Model: "fixture-model", Protocol: "responses",
		}, "Draw HTML")
		var failure *remote.HTTPError
		require.ErrorAs(t, err, &failure)
		require.Equal(t, http.StatusForbidden, failure.StatusCode)
		require.NotContains(t, err.Error(), "sk-private-secret")
		if strings.Contains(body, "INSUFFICIENT_BALANCE") || strings.Contains(body, "insufficient_quota") {
			require.NotNil(t, failure.Diagnostic)
			require.Equal(t, "quota", failure.Diagnostic.Code)
			require.Contains(t, failure.Diagnostic.SafeMessage(), "余额或额度不足")
		}
		if failure.Diagnostic != nil {
			require.NotContains(t, failure.Diagnostic.SafeMessage(), "sk-private-secret")
		}
	}
}

// TestParseGenerationCompletionBoundaries exercises incomplete, truncated, and
// oversized gateway output. A complete envelope alone cannot qualify a failed
// stream, and valid Chat fragments retain optional missing token measurements.
func TestParseGenerationCompletionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string // fixture scenario name
		protocol string // selected gateway protocol
		body     string // gateway response content
		want     error  // expected decoding failure
	}{
		{"disconnected", "responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<html><body>art</body></html>\"}\n\n", remote.ErrInvalidResponse},
		{"truncated", "chat_completions", `{"choices":[{"message":{"content":"<html><body>art</body></html>"},"finish_reason":"length"}]}`, remote.ErrInvalidResponse},
		{"missing body", "chat_completions", `{"choices":[{"message":{"content":"<html></html>"},"finish_reason":"stop"}]}`, remote.ErrInvalidResponse},
		{"empty", "responses", `{}`, remote.ErrInvalidResponse},
		{"oversized", "chat_completions", `{"choices":[{"message":{"content":"<html><body>` + strings.Repeat("a", remote.MaxHTMLBytes) + `</body></html>"},"finish_reason":"stop"}]}`, remote.ErrResponseTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseGeneration([]byte(tc.body), tc.protocol)
			require.ErrorIs(t, err, tc.want)
		})
	}
	result, err := parseGeneration([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"```html\\n<html><body>art</body></html>\\n```\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"), "chat_completions")
	require.NoError(t, err)
	require.Equal(t, "<html><body>art</body></html>", result.HTML)
	require.Nil(t, result.OutputTokens)
}
