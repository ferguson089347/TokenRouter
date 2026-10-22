package intelligence

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// thumbnailRepo records screenshots for the core cache and admission tests.
type thumbnailRepo struct {
	IntelligenceRepository                   // unused persistence operations
	mu                     sync.Mutex        // screenshot map ownership
	images                 map[string][]byte // persisted fixture images
}

// thumbnailRenderer injects a screenshot function for deterministic admission tests.
type thumbnailRenderer func(context.Context, string) ([]byte, error)

// GetThumbnail reads a copied fixture PNG under the screenshot map lock.
func (r *thumbnailRepo) GetThumbnail(_ context.Context, id string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.images[id]...), nil
}

// SaveThumbnail stores a copied fixture PNG under the screenshot map lock.
func (r *thumbnailRepo) SaveThumbnail(_ context.Context, id string, png []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.images[id] = append([]byte(nil), png...)
	return nil
}

// Render delegates the requested document to the injected screenshot function.
func (r thumbnailRenderer) Render(ctx context.Context, html string) ([]byte, error) {
	return r(ctx, html)
}

// TestThumbnailSharesPersistedFrame starts concurrent requests for one document.
// A single render produces both results, and the saved frame remains available
// after the full HTML leaves the artifact retention window.
func TestThumbnailSharesPersistedFrame(t *testing.T) {
	repo := &thumbnailRepo{images: map[string][]byte{}}
	var renders atomic.Int32
	started := make(chan struct{})
	finish := make(chan struct{})
	render := thumbnailRenderer(func(context.Context, string) ([]byte, error) {
		renders.Add(1)
		close(started)
		<-finish
		return []byte("saved-png"), nil
	})
	service := NewIntelligenceService(repo, nil, nil, nil, nil, nil, nil, render)
	t.Cleanup(service.Stop)
	run := &IntelligenceRun{ID: "drawing", Benchmark: "drawing", HTML: "<html><body>drawing</body></html>"}
	results := make(chan []byte, 2)
	var work sync.WaitGroup
	// Hold the first render while another caller requests the same observation,
	// verifying the second admitted caller reuses the completed persistent frame.
	for range 2 {
		work.Go(func() {
			image, err := service.Thumbnail(context.Background(), run)
			require.NoError(t, err)
			results <- image
		})
	}
	<-started
	close(finish)
	work.Wait()
	require.Equal(t, int32(1), renders.Load())
	require.Equal(t, []byte("saved-png"), <-results)
	require.Equal(t, []byte("saved-png"), <-results)
	run.HTML = ""
	image, err := service.Thumbnail(context.Background(), run)
	require.NoError(t, err)
	require.Equal(t, []byte("saved-png"), image)
}

// TestThumbnailAdmissionCancellation fills browser admission before requesting
// a second screenshot. Cancellation releases the queued reader with its context
// error and performs no browser work or persistent screenshot update.
func TestThumbnailAdmissionCancellation(t *testing.T) {
	repo := &thumbnailRepo{images: map[string][]byte{}}
	service := NewIntelligenceService(repo, nil, nil, nil, nil, nil, nil, thumbnailRenderer(func(context.Context, string) ([]byte, error) {
		t.Fatal("cancelled admission rendered a document")
		return nil, nil
	}))
	t.Cleanup(service.Stop)
	service.thumbnailSlot <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.Thumbnail(ctx, &IntelligenceRun{ID: "drawing", Benchmark: "drawing", HTML: "document"})
	require.ErrorIs(t, err, context.Canceled)
	<-service.thumbnailSlot
}
