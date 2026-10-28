package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// aggregationFixture embeds unused queries and records the ranges actually
// rebuilt by the production worker. Cursors model previously committed storage.
type aggregationFixture struct {
	ChannelMonitorV2Repository                                      // unused query method fixture
	watermark                  ChannelMonitorV2AggregationWatermark // committed cursor fixture
	ranges                     [][2]time.Time                       // rebuilt live time windows
	history                    [][2]time.Time                       // rebuilt historical time windows
	entered                    chan struct{}                        // configuration read admission
}

// GetAggregationWatermark returns the fixture's committed cursor without advancing it.
func (f *aggregationFixture) GetAggregationWatermark(context.Context) (*ChannelMonitorV2AggregationWatermark, error) {
	return &f.watermark, nil
}

// RecomputeLiveRange records the range admitted by production catch-up logic.
func (f *aggregationFixture) RecomputeLiveRange(_ context.Context, start, end time.Time) error {
	f.ranges = append(f.ranges, [2]time.Time{start, end})
	return nil
}

// RecomputeRange records historical work admitted after a successful live refresh.
func (f *aggregationFixture) RecomputeRange(_ context.Context, start, end time.Time) error {
	f.history = append(f.history, [2]time.Time{start, end})
	return nil
}

// GetConfig blocks until cancellation so Stop must interrupt actual pending work.
func (f *aggregationFixture) GetConfig(ctx context.Context) (*ChannelMonitorV2Config, error) {
	close(f.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestAggregatorRepairsOldestMissedWindow restores a watermark ten hours behind
// the current minute. It protects bounded catch-up starting at the missing
// interval rather than skipping directly to the recent overlap.
func TestAggregatorRepairsOldestMissedWindow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	f := &aggregationFixture{watermark: ChannelMonitorV2AggregationWatermark{HasData: true, DataThrough: now.Add(-10 * time.Hour)}}
	released := false
	a := NewAggregator(f, func(context.Context) (func(), bool) {
		return func() { released = true }, true
	}, nil)
	require.NoError(t, a.Run(t.Context(), now))
	require.Equal(t, [][2]time.Time{{now.Add(-10 * time.Hour), now.Add(-4 * time.Hour)}}, f.ranges)
	require.True(t, released)
}

// TestAggregatorStopCancelsConfigurationRead waits until the owned loop enters
// a blocking configuration query. It verifies cancellation completes shutdown
// and repeated Start cannot open a new worker after Stop.
func TestAggregatorStopCancelsConfigurationRead(t *testing.T) {
	f := &aggregationFixture{entered: make(chan struct{})}
	a := NewAggregator(f, nil, nil)
	a.Start()
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("configuration query did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, a.Stop(ctx))
	a.Start()
	require.NoError(t, a.Stop(ctx))
}

// TestAggregatorHistoryUsesWholeDaysAndPreservesLiveRefresh supplies a historical
// cursor deeper than seven days. It protects UTC date alignment and the minimum
// interval between historical transactions while allowing each live refresh.
func TestAggregatorHistoryUsesWholeDaysAndPreservesLiveRefresh(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)
	cursor := now.Add(-8 * 24 * time.Hour)
	f := &aggregationFixture{watermark: ChannelMonitorV2AggregationWatermark{HasData: true, DataThrough: now.Add(-5 * time.Minute), BackfillCursor: cursor}}
	a := NewAggregator(f, func(context.Context) (func(), bool) { return func() {}, true }, nil)
	require.NoError(t, a.Run(t.Context(), now))
	require.Equal(t, [][2]time.Time{{cursor.Add(-time.Hour).Truncate(24 * time.Hour), cursor}}, f.history)
	require.NoError(t, a.Run(t.Context(), now.Add(5*time.Minute)))
	require.Len(t, f.history, 1)
	require.Len(t, f.ranges, 2)
}
