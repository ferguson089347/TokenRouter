package intelligence

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// thumbnail.go supplies persistent screenshots for model quality history.
// Callers first authorize the observation through user or administrator detail;
// rendering then uses the retained HTML and stores a PNG separate from public
// observation JSON. Saved screenshots continue to describe historical works
// after their full HTML documents leave the ten-artifact retention window.
//
// A service-wide slot serializes browser work, and admission observes caller
// cancellation and service shutdown. The cache is checked again after admission
// because another request may have completed the same screenshot while this
// caller waited. Generation and read paths use this operation together, so
// historical backfill and first-page viewing cannot launch duplicate browser work.

// Thumbnail returns a saved screenshot or renders a retained drawing document.
// The caller must authorize run immediately before calling. Missing documents
// cannot be rendered, and a nil renderer leaves existing screenshots readable.
//
// Browser admission is serialized within this instance and cancelled by request
// completion or service shutdown. A second cache read after admission reuses work
// performed by earlier callers. Persistence finishes before bytes are returned,
// allowing subsequent requests to display the same frame without rerunning HTML.
func (s *IntelligenceService) Thumbnail(ctx context.Context, run *IntelligenceRun) ([]byte, error) {
	if run == nil || run.Benchmark != "drawing" {
		return nil, ErrIntelligenceNotFound
	}
	png, err := s.repo.GetThumbnail(ctx, run.ID)
	if err != nil || len(png) > 0 {
		return png, err
	}
	if run.HTML == "" {
		return nil, ErrIntelligenceNotFound
	}
	if s.thumbnails == nil {
		return nil, apperror.ServiceUnavailable("INTELLIGENCE_THUMBNAIL_UNAVAILABLE", "作品截图暂时无法生成")
	}
	// Tie the rendering budget to shutdown as well as the requesting consumer so
	// detached generation persistence cannot keep a browser running after service cancellation.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	stop := context.AfterFunc(s.ctx, cancel)
	defer cancel()
	defer stop()
	// Wait for browser ownership with both cancellation sources so shutdown does
	// not leave HTTP readers queued behind work whose dependencies are closing.
	select {
	case s.thumbnailSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	// Return browser admission after rendering or persistence errors as well as
	// success so later screenshot requests can continue through the same service slot.
	defer func() { <-s.thumbnailSlot }()
	png, err = s.repo.GetThumbnail(ctx, run.ID)
	if err != nil || len(png) > 0 {
		return png, err
	}
	png, err = s.thumbnails.Render(ctx, run.HTML)
	if err != nil {
		return nil, apperror.ServiceUnavailable("INTELLIGENCE_THUMBNAIL_UNAVAILABLE", "作品截图暂时无法生成")
	}
	// Publish the captured frame before returning it so the next history hover
	// reuses the saved PNG even when its full drawing document has been pruned.
	if err := s.repo.SaveThumbnail(ctx, run.ID, png); err != nil {
		return nil, err
	}
	return png, nil
}
