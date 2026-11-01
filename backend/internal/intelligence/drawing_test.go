package intelligence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/intelligence/remote"
)

// TestDrawingGenerationRetryResumesOneTask fails two gateway submissions before
// returning artwork. Persisted retry state survives worker replacement and the
// successful third attempt proceeds to assessment under the same task identity.
func TestDrawingGenerationRetryResumesOneTask(t *testing.T) {
	s, repo, client, _ := newIntelligenceFixture()
	repo.config.Benchmark = "drawing"
	run := intelligenceQueued()
	run.Benchmark = "drawing"
	client.generate = func(remote.CreateRequest, string) (*remote.DrawingResult, error) {
		require.Equal(t, client.generates, repo.run.GenerationAttempts)
		if client.generates < 3 {
			return nil, &remote.HTTPError{StatusCode: 524}
		}
		return &remote.DrawingResult{HTML: "<html><body>art</body></html>"}, nil
	}
	for attempt := range 3 {
		started := time.Now()
		s.execute(context.Background(), run)
		require.Equal(t, attempt+1, repo.run.GenerationAttempts)
		require.Equal(t, "iq_local", repo.run.ID)
		if attempt < 2 {
			require.Equal(t, "queued", repo.run.Status)
			require.Equal(t, "pending", repo.run.Verdict)
			require.Nil(t, repo.run.FinishedAt)
			require.Empty(t, repo.run.ErrorMessage)
			require.GreaterOrEqual(t, repo.nextPoll.Sub(started), time.Duration(attempt+1)*15*time.Second)
			// A replacement worker receives the saved task, including the generation
			// counter, rather than constructing another run with a fresh retry budget.
			copy := *repo.run
			run = &copy
		}
	}
	require.Equal(t, 3, client.generates)
	require.Equal(t, "generated", repo.run.Phase)
	require.Equal(t, "running", repo.run.Status)
	require.True(t, repo.run.HasArtifact)
	require.Zero(t, client.creates)
}

// TestDrawingGenerationRetryStopsAtBudget repeatedly fails the model exchange.
// Three submissions finish one diagnostic task, and a denied retry-state write
// cannot trigger another gateway call or a competing terminal-state write.
func TestDrawingGenerationRetryStopsAtBudget(t *testing.T) {
	s, repo, client, _ := newIntelligenceFixture()
	repo.config.Benchmark = "drawing"
	client.generate = func(remote.CreateRequest, string) (*remote.DrawingResult, error) {
		return nil, &remote.HTTPError{StatusCode: 524}
	}
	run := intelligenceQueued()
	run.Benchmark = "drawing"
	for range 3 {
		s.execute(context.Background(), run)
		copy := *repo.run
		run = &copy
	}
	require.Equal(t, 3, client.generates)
	require.Equal(t, "error", repo.run.Status)
	require.NotNil(t, repo.run.FinishedAt)
	require.Contains(t, repo.run.ErrorMessage, "524")
	require.Zero(t, client.creates)

	repo.denySave = true
	run = intelligenceQueued()
	run.GenerationAttempts = 1
	saved := len(repo.saves)
	require.True(t, s.retryDrawingGeneration(context.Background(), run, &remote.TransportError{Timeout: true}))
	require.Len(t, repo.saves, saved)
	require.Equal(t, 3, client.generates)
}

// TestDrawingGenerationRetryClassification distinguishes recoverable network
// and stream failures from permanent credentials, quota, and parameter errors.
// Cancellation and an elapsed observation window also prevent another submission.
func TestDrawingGenerationRetryClassification(t *testing.T) {
	for _, scenario := range []struct {
		name  string // fixture diagnostic scenario
		err   error  // observed gateway failure
		retry bool   // expected retry admission
	}{
		{"timeout", &remote.TransportError{Timeout: true}, true},
		{"connection", &remote.TransportError{}, true},
		{"cancelled", &remote.TransportError{Canceled: true}, false},
		{"incomplete", remote.ErrInvalidResponse, true},
		{"oversized", remote.ErrResponseTooLarge, false},
		{"bad request", &remote.HTTPError{StatusCode: 400}, false},
		{"authentication", &remote.HTTPError{StatusCode: 401}, false},
		{"authentication overrides transient body", &remote.HTTPError{StatusCode: 401, Diagnostic: &remote.RemoteError{Code: "timeout"}}, false},
		{"rate limit", &remote.HTTPError{StatusCode: 429}, true},
		{"upstream", &remote.HTTPError{StatusCode: 502}, true},
		{"quota overrides wrapper", &remote.HTTPError{StatusCode: 502, Diagnostic: &remote.RemoteError{Code: "quota"}}, false},
		{"stream timeout", &remote.HTTPError{Diagnostic: &remote.RemoteError{Code: "timeout"}}, true},
		{"unexpected", errors.New("fixture invalid state"), false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			require.Equal(t, scenario.retry, drawingGenerationRetryable(scenario.err))
		})
	}
	s, repo, _, _ := newIntelligenceFixture()
	run := intelligenceQueued()
	run.GenerationAttempts = 1
	run.CreatedAt = time.Now().Add(-31 * time.Minute)
	require.False(t, s.retryDrawingGeneration(context.Background(), run, remote.ErrInvalidResponse))
	run.CreatedAt = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, s.retryDrawingGeneration(ctx, run, remote.ErrInvalidResponse))
	require.Empty(t, repo.saves)
}

// TestDrawingAssessmentRetryKeepsGeneratedArtwork retries a remote execution
// failure using stored HTML. The next assessment succeeds without another model
// generation, and its accepted result keeps the existing task identity.
func TestDrawingAssessmentRetryKeepsGeneratedArtwork(t *testing.T) {
	s, repo, client, _ := newIntelligenceFixture()
	repo.config.Benchmark = "drawing"
	run := intelligenceQueued()
	run.Benchmark = "drawing"
	run.LocalGeneration = true
	run.GenerationAttempts = 1
	run.AssessmentAttempts = 1
	run.Status = "running"
	run.RemoteID = "failed-assessment"
	run.HTML = "<html><body>saved art</body></html>"
	client.result = &remote.TestResult{ID: run.RemoteID, Benchmark: "html", Status: "failed", Error: &remote.RemoteError{Code: "upstream"}}
	s.execute(context.Background(), run)
	require.Empty(t, repo.run.RemoteID)
	require.Equal(t, "running", repo.run.Status)
	require.Equal(t, "generated", repo.run.Phase)
	require.Equal(t, "pending", repo.run.Verdict)
	require.Equal(t, run.HTML, repo.run.HTML)
	client.result = &remote.TestResult{ID: "successful-assessment", Benchmark: "html", Status: "succeeded", Assessment: &remote.Assessment{Quality: "normal"}}
	s.execute(context.Background(), run)
	require.Equal(t, "completed", repo.run.Status)
	require.Equal(t, "passed", repo.run.Verdict)
	require.Equal(t, 2, repo.run.AssessmentAttempts)
	require.Equal(t, 1, repo.run.GenerationAttempts)
	require.Zero(t, client.generates)
	require.Equal(t, 1, client.creates)
	require.Equal(t, "iq_local", repo.run.ID)
}
