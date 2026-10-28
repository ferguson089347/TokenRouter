package monitor

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// aggregator.go rebuilds passive channel facts from retained usage and errors.
// Construction retains dependencies and leaves execution to application startup.
// Each iteration acquires cluster leadership, reads the durable watermark, and
// refreshes the live overlap before spending remaining work on older history.
// Missing ticks resume at the last successful minute instead of leaving gaps.
// Database transactions publish rebuilt facts and their cursor together, making
// retries safe after interrupted work or a change of the elected process.
//
// Shutdown cancels configuration reads, lock acquisition, and aggregation SQL,
// then waits for the owned loop before shared storage can close. Historical
// chunks grow within Kedaya's depth limits and run at most every fifteen minutes;
// the first iteration prioritizes the recent two hours used by the default
// ninety-minute view. The lock is held until all work in that iteration ends.

// Aggregation timing matches Kedaya's live overlap, historical depth, and
// database budget while bounding each individual historical transaction.
const (
	monitorRecentOverlap   = 2 * time.Minute
	monitorBootstrap       = 2 * time.Hour
	monitorRetention       = 90 * 24 * time.Hour
	monitorRunBudget       = 3 * time.Minute
	monitorHistoryInterval = 15 * time.Minute
	monitorHistoryChunk    = time.Hour
)

// Aggregator rebuilds persisted observations under application-owned leadership.
type Aggregator struct {
	repo    ChannelMonitorV2Repository           // passive observation database interface
	acquire func(context.Context) (func(), bool) // iteration leadership acquisition
	logf    func(string, ...any)                 // application background error logger

	// mu protects lifetime admission. Once stopped, Start cannot create another
	// loop; done closes after the admitted loop releases its database leadership.
	mu           sync.Mutex         // lifetime coordination mutex
	started      bool               // background loop admitted
	stopped      bool               // permanent shutdown requested
	cancel       context.CancelFunc // owned loop cancellation
	done         chan struct{}      // background completion notification
	lastHistory  time.Time          // owned loop historical attempt time
	historyChunk time.Duration      // adaptive historical interval duration
}

// NewAggregator retains required storage and leadership callbacks for later
// startup. The caller keeps these dependencies available until Stop completes;
// construction starts no background work and does not read configuration.
func NewAggregator(repo ChannelMonitorV2Repository, acquire func(context.Context) (func(), bool), logf func(string, ...any)) *Aggregator {
	return &Aggregator{repo: repo, acquire: acquire, logf: logf, done: make(chan struct{}), historyChunk: monitorHistoryChunk}
}

// Start admits one background loop until permanent shutdown is requested.
// Application startup calls this after binding storage and logging; repeated
// starts and starts after Stop return without creating additional workers.
func (a *Aggregator) Start() {
	// Admission and cancellation publication share a mutex so Stop can always
	// cancel an admitted loop, including when startup and shutdown overlap.
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started || a.stopped {
		return
	}
	a.started = true
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	go a.loop(ctx)
}

// Stop permanently closes startup admission, cancels work, and waits for the
// worker within ctx. A deadline reports incomplete shutdown; a later Stop can
// continue waiting for the same worker without losing its completion signal.
func (a *Aggregator) Stop(ctx context.Context) error {
	// Read and cancel the admitted lifetime before waiting. Shared storage must
	// remain usable until the worker finishes releasing its current leadership.
	a.mu.Lock()
	a.stopped = true
	started := a.started
	if a.cancel != nil {
		a.cancel()
	}
	a.mu.Unlock()
	if !started {
		return nil
	}
	select {
	case <-a.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("passive channel monitor shutdown: %w", ctx.Err())
	}
}

// loop runs serial bounded iterations and waits using the persisted refresh
// interval. Configuration failures back off for one minute and remain visible
// through logging; cancellation interrupts both the iteration and its wait.
func (a *Aggregator) loop(ctx context.Context) {
	// Completion includes all in-flight SQL and leadership cleanup, allowing the
	// lifecycle owner to close storage only after this signal is observable.
	defer close(a.done)
	for ctx.Err() == nil {
		interval := time.Minute
		runCtx, cancel := context.WithTimeout(ctx, monitorRunBudget)
		cfg, err := a.repo.GetConfig(runCtx)
		if err == nil && cfg.Enabled {
			interval = time.Duration(cfg.RefreshIntervalSeconds) * time.Second
			err = a.Run(runCtx, time.Now())
		}
		cancel()
		if err != nil && ctx.Err() == nil && a.logf != nil {
			a.logf("passive channel aggregation failed: %v", err)
		}
		if interval < time.Minute {
			interval = time.Minute
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			// Stop the pending wait when application shutdown interrupts the worker;
			// no additional iteration may start against closing application storage.
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Run recomputes one leader-owned live window and an optional historical chunk.
// Call serially with a bounded context; the background loop is its production
// caller. Every iteration rereads durable cursors, so missed ticks, restarts,
// and administrator-directed rewind are reflected without a process restart.
//
// Live catch-up advances in six-hour chunks after long outages. Historical work
// runs after live repair, at most once per fifteen minutes, and its start is
// aligned to UTC hours so fixed rollups can be rebuilt from complete source
// buckets. A failed repository call leaves its durable watermark unchanged and
// the next iteration repeats the pending range under newly acquired leadership.
func (a *Aggregator) Run(ctx context.Context, now time.Time) error {
	release, acquired := a.acquire(ctx)
	if !acquired {
		return nil
	}
	// Keep leadership through both transactions, including cursor publication,
	// so another instance cannot rebuild the same mutable range concurrently.
	defer release()
	now = now.UTC().Truncate(time.Minute)
	wm, err := a.repo.GetAggregationWatermark(ctx)
	if err != nil {
		return err
	}
	if wm == nil || !wm.HasData {
		return a.repo.RecomputeRange(ctx, now.Add(-monitorBootstrap).Truncate(time.Hour), now)
	}
	start := now.Add(-monitorRecentOverlap)
	if !wm.DataThrough.IsZero() && wm.DataThrough.Before(start) {
		start = wm.DataThrough
	}
	// Raw history outside retention cannot be rebuilt. Clamp an obsolete cursor
	// before chunking so an old installation can advance into its retained data.
	if cutoff := now.Add(-monitorRetention); start.Before(cutoff) {
		start = cutoff
	}
	// Advance the oldest missing live range first; capping its end keeps older
	// gaps repairable instead of moving the watermark past an unrecomputed hole.
	end := now
	if end.Sub(start) > 6*time.Hour {
		end = start.Add(6 * time.Hour)
	}
	liveStarted := time.Now()
	if err := a.repo.RecomputeLiveRange(ctx, start, end); err != nil {
		return err
	}
	if end.Before(now) || time.Since(liveStarted) > 8*time.Second || (!a.lastHistory.IsZero() && now.Sub(a.lastHistory) < monitorHistoryInterval) {
		return nil
	}
	end = wm.BackfillCursor.UTC().Truncate(time.Hour)
	if end.IsZero() {
		return nil
	}
	a.lastHistory = now
	if !end.After(now.Add(-monitorRetention)) {
		// Completed historical bootstrap still needs retention and current coarse
		// bucket refresh. Reuse its transaction periodically after a cheap live tick.
		return a.repo.RecomputeRange(ctx, now.Add(-monitorRecentOverlap), now)
	}
	maximum := 6 * time.Hour
	if now.Sub(end) < 24*time.Hour {
		maximum = 2 * time.Hour
	} else if now.Sub(end) < 7*24*time.Hour {
		maximum = 4 * time.Hour
	}
	chunk := min(max(a.historyChunk, 15*time.Minute), maximum)
	start = end.Add(-chunk)
	// Older fixed buckets need adjacent minute facts until their whole day has
	// been rebuilt. Kedaya aligns deeper history to UTC dates for that purpose.
	if end.Before(now.Add(-7 * 24 * time.Hour)) {
		start = start.Truncate(24 * time.Hour)
	}
	start = maxTime(start, now.Add(-monitorRetention))
	historyStarted := time.Now()
	if err := a.repo.RecomputeRange(ctx, start, end); err != nil {
		// Shrink expensive historical work after failure, retaining the durable
		// cursor so another attempt can rebuild the same uncommitted interval.
		a.historyChunk = max(15*time.Minute, chunk/2)
		return err
	}
	if time.Since(historyStarted) < 15*time.Second {
		// Increase the next chunk only after a cheap successful rebuild. Depth
		// ceilings still bound growth and protect the recent view under database load.
		a.historyChunk = min(maximum, chunk+chunk/2)
	}
	return nil
}

// maxTime returns the later timestamp used by retained-history range clamps.
func maxTime(first, second time.Time) time.Time {
	if first.After(second) {
		return first
	}
	return second
}
