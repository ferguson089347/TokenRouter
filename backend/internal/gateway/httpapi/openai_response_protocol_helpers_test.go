package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestHeaderKeepaliveLeavesDisabledAndBufferedResponsesUntouched exercises
// unconfigured, disabled, and nonstreaming calls through the response helper.
// Their waits retain an unwritten writer so ordinary HTTP errors remain available.
func TestHeaderKeepaliveLeavesDisabledAndBufferedResponsesUntouched(t *testing.T) {
	for _, scenario := range []struct {
		name   string                // fixture policy description
		output *OpenAIResponseOutput // configured response output policy
		stream bool                  // client requested SSE output
	}{
		{"missing output", nil, true},
		{"unconfigured", &OpenAIResponseOutput{Options: OpenAIResponseOptions{StreamKeepaliveInterval: 10}}, true},
		{"disabled", &OpenAIResponseOutput{Options: OpenAIResponseOptions{Configured: true}}, true},
		{"buffered", &OpenAIResponseOutput{Options: OpenAIResponseOptions{Configured: true, StreamKeepaliveInterval: 10}}, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				stop := scenario.output.StartHeaderKeepalive(c, scenario.stream)
				defer stop()
				time.Sleep(130 * time.Second)
				synctest.Wait()
				require.False(t, c.Writer.Written())
				require.Empty(t, rec.Body.String())
			})
		})
	}
}

// TestSummarizeNoOutputBody_RespectsLogBodyConfig summarizes an in-progress
// image stream with body logging disabled. It checks that the last event
// remains visible while the raw body excerpt is omitted.
func TestSummarizeNoOutputBody_RespectsLogBodyConfig(t *testing.T) {
	body := []byte("data: {\"type\":\"response.in_progress\",\"response\":{\"status\":\"in_progress\"}}\n\n")
	svc := newImagesFixture(imagesFixtureInputs{})

	summary := svc.Output.ImageNoOutputSummary(body)
	if !strings.Contains(summary, "last_event=response.in_progress") {
		t.Fatalf("summary should keep structured diagnostics, got %q", summary)
	}
	if strings.Contains(summary, " body=") {
		t.Fatalf("summary should omit body when log_upstream_error_body is disabled, got %q", summary)
	}
}
