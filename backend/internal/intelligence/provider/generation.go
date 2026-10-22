package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/egress/urlpolicy"
	"github.com/TokenFlux/TokenRouter/internal/intelligence/remote"
)

// generation.go requests drawing documents from the configured gateway and
// extracts complete HTML with generation measurements. Gateway authentication
// stays on the generation request; the separate assessment client receives
// completed artwork through its HTML benchmark. Both supported protocols can
// return JSON or server-sent events, and response bodies have a fixed byte cap.
//
// Stream fragments contribute text in arrival order, while a completed Responses
// object supplies authoritative content and usage when present. A stream needs
// a successful terminal event before its content can become an artifact. This
// keeps disconnected or token-truncated output from being submitted as completed
// artwork, even when the partial text happens to contain closing HTML tags.
// Arrival timing is observed as event lines are read, before complete output
// parsing, so the first token delay stays separate from total generation time.

// generationMaxOutputTokens bounds output and reasoning work requested from
// either gateway protocol so one drawing uses a predictable generation budget.
const generationMaxOutputTokens = 8192

// Document expressions find a complete HTML envelope inside model prose or code
// fences so previews and assessments receive the same extracted document.
var (
	htmlStartPattern = regexp.MustCompile(`(?i)<!doctype\s+html\b|<html(?:\s|>)`)
	htmlEndPattern   = regexp.MustCompile(`(?i)</html\s*>`)
	bodyStartPattern = regexp.MustCompile(`(?i)<body(?:\s|>)`)
	bodyEndPattern   = regexp.MustCompile(`(?i)</body\s*>`)
)

// Generate obtains complete drawing HTML using the configured model and prompt.
// It makes one bounded gateway request and disables body replay, redirects, and
// cookies because the supplied personal credential can authorize chargeable work.
// Empty, incomplete, or oversized documents return safe errors for the worker.
// Streaming responses report the first nonempty text or reasoning delta delay;
// buffered JSON keeps that measurement absent because its arrival cannot identify
// when the first token became available.
func (c *Client) Generate(ctx context.Context, input remote.CreateRequest, prompt string) (*remote.DrawingResult, error) {
	if input.Benchmark != "pelican" || strings.TrimSpace(prompt) == "" || len(prompt) > remote.MaxPromptBytes {
		return nil, remote.ErrInvalidRequest
	}
	if err := validateCreate(input, ""); err != nil {
		return nil, err
	}
	base, err := urlpolicy.ValidateHTTPSURL(input.BaseURL, urlpolicy.ValidationOptions{})
	if err != nil {
		return nil, remote.ErrInvalidRequest
	}
	payload := map[string]any{"model": input.Model, "stream": true}
	path := "/responses"
	// Each protocol places instructions and reasoning controls in different fields;
	// encode the selected settings together so gateway dispatch receives the configured task.
	if input.Protocol == "chat_completions" {
		path = "/chat/completions"
		payload["messages"] = []map[string]string{{"role": "user", "content": prompt}}
		payload["max_completion_tokens"] = generationMaxOutputTokens
		payload["stream_options"] = map[string]bool{"include_usage": true}
		if input.ReasoningEffort != "" {
			payload["reasoning_effort"] = input.ReasoningEffort
		}
	} else {
		payload["input"] = prompt
		payload["max_output_tokens"] = generationMaxOutputTokens
		payload["store"] = false
		if input.ReasoningEffort != "" {
			payload["reasoning"] = map[string]string{"effort": input.ReasoningEffort}
		}
	}
	if input.ServiceTier != "" {
		payload["service_tier"] = input.ServiceTier
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, remote.ErrInvalidRequest
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, remote.ErrInvalidRequest
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	req.Header.Set("Authorization", "Bearer "+input.APIKey)
	// A transport retry could spend the same Key's quota again after acceptance;
	// keep generation attempts single-use even when a pooled connection fails.
	req.GetBody = nil
	started := time.Now()
	resp, err := c.generationClient.Do(req)
	if err != nil {
		return nil, safeTransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		failure := &remote.HTTPError{StatusCode: resp.StatusCode}
		// Gateway error bodies can contain credentials. Classify a bounded complete
		// JSON response through RemoteError and discard every unrecognized raw detail.
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, remote.MaxResponseBytes+1))
		var diagnostic remote.RemoteError
		if readErr == nil && len(raw) <= remote.MaxResponseBytes && json.Unmarshal(raw, &diagnostic) == nil && diagnostic.Code != "" {
			diagnostic.HTTPStatus = resp.StatusCode
			failure.Diagnostic = &diagnostic
		}
		return nil, failure
	}
	raw, firstToken, err := readGenerationBody(resp.Body, input.Protocol, started)
	if err != nil {
		return nil, err
	}
	result, err := parseGeneration(raw, input.Protocol)
	if err != nil {
		return nil, err
	}
	duration := time.Since(started).Milliseconds()
	result.DurationMS = &duration
	result.FirstTokenMS = firstToken
	result.Prompt = prompt
	return result, nil
}

// readGenerationBody retains bounded response bytes while observing stream timing.
// The first nonempty text or reasoning delta sets elapsed milliseconds from
// started; lifecycle events, empty deltas, usage, and buffered JSON leave it unset.
// Read errors return safe transport classifications and an oversized body fails.
//
// Event arrival is measured during reading because scanning after complete body
// collection would report total generation time as first-token latency. One extra
// byte detects truncation, and subsequent events cannot replace the first observed
// delay. Complete body validation remains the responsibility of parseGeneration.
func readGenerationBody(source io.Reader, protocol string, started time.Time) ([]byte, *int64, error) {
	// Retain one extra byte so exceeding the response cap is distinguishable from
	// an exactly full response before its completed HTML can be accepted downstream.
	reader := bufio.NewReader(io.LimitReader(source, remote.MaxResponseBytes+1))
	var body bytes.Buffer
	var firstToken *int64
	for {
		line, err := reader.ReadString('\n')
		_, _ = body.WriteString(line)
		if body.Len() > remote.MaxResponseBytes {
			return nil, nil, remote.ErrResponseTooLarge
		}
		if firstToken == nil && strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if generationTokenEvent(data, protocol) {
				// Capture timing at the first completed content event line. Heartbeats and
				// lifecycle notifications can arrive earlier while the model is still waiting.
				elapsed := time.Since(started).Milliseconds()
				firstToken = &elapsed
			}
		}
		if err == io.EOF {
			return body.Bytes(), firstToken, nil
		}
		if err != nil {
			return nil, nil, safeTransportError(err)
		}
	}
}

// generationTokenEvent identifies a nonempty model text or reasoning stream delta.
// Responses uses named delta event types, while Chat choice zero carries content
// or reasoning text. Invalid JSON, empty fields, and lifecycle events return false.
func generationTokenEvent(data, protocol string) bool {
	if !gjson.Valid(data) {
		return false
	}
	event := gjson.Parse(data)
	if protocol == "chat_completions" {
		for _, choice := range event.Get("choices").Array() {
			if choice.Get("index").Int() != 0 {
				continue
			}
			for _, field := range []string{"content", "reasoning_content", "reasoning"} {
				value := choice.Get("delta." + field)
				if value.Type == gjson.String && value.String() != "" {
					return true
				}
			}
		}
		return false
	}
	switch event.Get("type").String() {
	case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta", "response.refusal.delta":
		value := event.Get("delta")
		return value.Type == gjson.String && value.String() != ""
	default:
		return false
	}
}

// parseGeneration decodes complete gateway JSON or a terminated event stream.
// Responses output blocks and Chat choice zero provide text; optional usage
// retains absence, and unsuccessful terminal states reject even syntactically
// complete HTML. Extracted artwork is bounded separately from the response body.
// The completed Responses object replaces accumulated deltas because providers
// repeat content in both representations; appending that final text would
// duplicate document bytes and change the assessed artifact.
func parseGeneration(raw []byte, protocol string) (*remote.DrawingResult, error) {
	var text strings.Builder
	var usage gjson.Result
	complete := false
	if json.Valid(raw) {
		data := gjson.ParseBytes(raw)
		usage = data.Get("usage")
		if protocol == "chat_completions" {
			_, _ = text.WriteString(data.Get("choices.0.message.content").String())
			complete = data.Get("choices.0.finish_reason").String() == "stop"
		} else {
			_, _ = text.WriteString(responseOutput(data))
			complete = data.Get("status").String() == "completed" && data.Get("error").Type == gjson.Null
		}
	} else {
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		scanner.Buffer(make([]byte, 4096), remote.MaxResponseBytes)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				continue
			}
			if !gjson.Valid(data) {
				return nil, remote.ErrInvalidResponse
			}
			event := gjson.Parse(data)
			if event.Get("error").Type != gjson.Null {
				return nil, remote.ErrInvalidResponse
			}
			if protocol == "chat_completions" {
				for _, choice := range event.Get("choices").Array() {
					if choice.Get("index").Int() != 0 {
						continue
					}
					_, _ = text.WriteString(choice.Get("delta.content").String())
					if reason := choice.Get("finish_reason"); reason.Type == gjson.String {
						complete = reason.String() == "stop"
					}
				}
				if event.Get("usage").IsObject() {
					usage = event.Get("usage")
				}
				continue
			}
			switch event.Get("type").String() {
			case "response.output_text.delta":
				_, _ = text.WriteString(event.Get("delta").String())
			case "response.completed":
				response := event.Get("response")
				complete = response.Get("status").String() == "completed" && response.Get("error").Type == gjson.Null
				usage = response.Get("usage")
				if output := responseOutput(response); output != "" {
					// The completed object repeats text already received through deltas;
					// replace accumulated fragments so each output byte appears once in HTML.
					text.Reset()
					_, _ = text.WriteString(output)
				}
			case "response.failed", "response.incomplete", "error":
				return nil, remote.ErrInvalidResponse
			}
		}
		if scanner.Err() != nil {
			return nil, remote.ErrInvalidResponse
		}
	}
	if !complete {
		return nil, remote.ErrInvalidResponse
	}
	html := extractHTML(text.String())
	if html == "" {
		return nil, remote.ErrInvalidResponse
	}
	if len(html) > remote.MaxHTMLBytes {
		return nil, remote.ErrResponseTooLarge
	}
	result := &remote.DrawingResult{HTML: html}
	if protocol == "chat_completions" {
		result.InputTokens = generationToken(usage.Get("prompt_tokens"))
		result.OutputTokens = generationToken(usage.Get("completion_tokens"))
		result.ReasoningTokens = generationToken(usage.Get("completion_tokens_details.reasoning_tokens"))
	} else {
		result.InputTokens = generationToken(usage.Get("input_tokens"))
		result.OutputTokens = generationToken(usage.Get("output_tokens"))
		result.ReasoningTokens = generationToken(usage.Get("output_tokens_details.reasoning_tokens"))
	}
	return result, nil
}

// responseOutput joins assistant output text from a completed Responses object.
func responseOutput(data gjson.Result) string {
	var text strings.Builder
	for _, item := range data.Get("output").Array() {
		if item.Get("type").String() != "message" || item.Get("role").String() != "assistant" {
			continue
		}
		for _, content := range item.Get("content").Array() {
			if content.Get("type").String() == "output_text" {
				_, _ = text.WriteString(content.Get("text").String())
			}
		}
	}
	return text.String()
}

// generationToken retains nonnegative integer measurements and returns nil for
// missing or invalid values. This lets result displays distinguish an absent
// measurement from a reported zero token count.
func generationToken(value gjson.Result) *int64 {
	if value.Type != gjson.Number || value.Float() < 0 || value.Float() != float64(value.Int()) {
		return nil
	}
	n := value.Int()
	return &n
}

// extractHTML isolates a complete HTML document including its body element.
// Leading prose and code fences are removed; a missing envelope or body returns
// an empty string so the worker cannot assess incomplete model output.
func extractHTML(text string) string {
	start := htmlStartPattern.FindStringIndex(text)
	if start == nil {
		return ""
	}
	text = text[start[0]:]
	end := htmlEndPattern.FindStringIndex(text)
	if end == nil {
		return ""
	}
	html := strings.TrimSpace(text[:end[1]])
	if !bodyStartPattern.MatchString(html) || !bodyEndPattern.MatchString(html) {
		return ""
	}
	return html
}
