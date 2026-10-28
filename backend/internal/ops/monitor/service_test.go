package monitor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestChannelMonitorHealthRequiresRequestSamples supplies latency and cache
// observations before enough requests exist. It protects unknown health even
// when those secondary signals could otherwise produce a high score.
func TestChannelMonitorHealthRequiresRequestSamples(t *testing.T) {
	latency := int64(100)
	metrics := ChannelMonitorV2Metric{
		RequestCount: 49, CacheRateDenominator: 100000, CacheRate: 1,
		TTFT: ChannelMonitorV2Latency{SampleCount: 100, P50Ms: &latency},
	}
	health := ChannelMonitorV2HealthFor(metrics)
	require.Equal(t, "unknown", health.Overall)
	require.Nil(t, health.Score)
	metrics.RequestCount = 50
	metrics.CacheRate = 0
	health = ChannelMonitorV2HealthFor(metrics)
	require.Equal(t, "healthy", health.Overall)
	require.EqualValues(t, 100, *health.Score)
	require.Equal(t, "healthy", health.Cache)
	metrics.ErrorRate = 0.30
	health = ChannelMonitorV2HealthFor(metrics)
	require.Equal(t, "critical", health.ErrorRate)
	require.Equal(t, "critical", health.Overall)
}

// TestChannelMonitorWindowsAndPublicRedaction checks fixed history alignment and
// public snapshot privacy together. It protects the selected history shape while
// retaining rates and clearing volume counters, model inventories, and operators.
func TestChannelMonitorWindowsAndPublicRedaction(t *testing.T) {
	s := NewChannelMonitorV2Service(nil)
	now := time.Date(2026, 10, 28, 12, 3, 40, 0, time.UTC)
	s.now = func() time.Time { return now }
	for _, test := range []struct {
		name   string        // accepted product range
		window time.Duration // total displayed time span
		bucket time.Duration // fixed history bucket width
	}{{"90m", 90 * time.Minute, 5 * time.Minute}, {"24h", 24 * time.Hour, time.Hour}, {"7d", 7 * 24 * time.Hour, 12 * time.Hour}, {"30d", 30 * 24 * time.Hour, 24 * time.Hour}} {
		filter, err := s.ParseFilter(test.name, nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, test.window, filter.End.Sub(filter.Start))
		require.Equal(t, test.bucket, filter.Bucket)
		require.Equal(t, filter.End, filter.End.Truncate(test.bucket))
	}
	_, err := s.ParseFilter("unbounded", nil, nil, nil)
	require.ErrorIs(t, err, ErrChannelMonitorV2InvalidRange)
	metric := ChannelMonitorV2Metric{
		RequestCount: 80, RPM: 12, TPM: 8000, SuccessRate: .9, ErrorRate: .1,
		TTFT: ChannelMonitorV2Latency{SampleCount: 80},
	}
	result := ChannelMonitorV2Snapshot{
		Metrics: metric, Trend: []ChannelMonitorV2TrendPoint{{Metrics: metric}},
		Config: ChannelMonitorV2Config{GroupIDs: []int64{42}, Platforms: []ChannelMonitorV2PlatformConfig{{Models: []string{"private-model"}}}},
	}
	redactChannelMonitorV2Snapshot(&result, true)
	require.Zero(t, result.Metrics.RequestCount)
	require.Zero(t, result.Metrics.RPM)
	require.Zero(t, result.Metrics.TTFT.SampleCount)
	require.Equal(t, .9, result.Metrics.SuccessRate)
	require.Zero(t, result.Trend[0].Metrics.TPM)
	require.Empty(t, result.Config.GroupIDs)
	require.Empty(t, result.Config.Platforms[0].Models)
}
