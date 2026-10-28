//go:build integration

package postgres

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

// TestPassiveGroupAvailability uses committed fixed rollups with sufficient and
// insufficient samples in real PostgreSQL. Public summaries must follow monitor
// settings, retain unknown sparse buckets, and read groups without active probes.
func TestPassiveGroupAvailability(t *testing.T) {
	db := postgrescontainer.New(t)
	now := time.Now().UTC().Truncate(time.Minute)
	first := now.Add(-time.Hour).Truncate(5 * time.Minute)
	second := first.Add(5 * time.Minute)
	_, err := db.Exec(`INSERT INTO channel_monitor_v2_watermarks
 (id,data_through,usage_coverage_start,error_coverage_start,last_successful_at,backfill_cursor)
 VALUES(1,$1,$2,$2,$1,$2)
 ON CONFLICT(id) DO UPDATE SET data_through=$1,usage_coverage_start=$2,error_coverage_start=$2`, now, now.Add(-48*time.Hour))
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO channel_monitor_v2_metrics_rollup
 (bucket_start,bucket_seconds,platform,group_id,model,success_requests,error_requests)
 VALUES($1,300,'openai',42,'model',54,6),($2,300,'openai',42,'model',1,0)`, first, second)
	require.NoError(t, err)
	store := NewGroupAvailabilityStore(db)
	result, err := store.GetSummaryByGroupIDs(t.Context(), []int64{42}, 1, 5, "Asia/Tokyo", now)
	require.NoError(t, err)
	require.EqualValues(t, 61, result[42].TotalCount)
	require.InDelta(t, 55.0/61, *result[42].AvailabilityRate, 0.000001)
	require.Equal(t, "unknown", result[42].LastStatus)
	require.Equal(t, "warning", result[42].Health.Status)
	require.NotNil(t, result[42].Health.Score)
	require.True(t, result[42].CoverageComplete)
	require.True(t, now.Equal(*result[42].DataThrough))
	known := 0
	for _, bucket := range result[42].Days {
		if bucket.AvailabilityRate != nil {
			known++
			require.Equal(t, .9, *bucket.AvailabilityRate)
		}
	}
	require.Equal(t, 1, known)
	// Ignored authentication failures remain visible in true success rates but
	// do not lower health. Histogram samples gate public latency independently;
	// fifty observations use the enclosing histogram bound for the window P50.
	_, err = db.Exec(`INSERT INTO channel_monitor_v2_error_metrics_rollup
 (bucket_start,bucket_seconds,platform,group_id,model,error_category,taxonomy_version,error_requests)
 VALUES($1,300,'openai',42,'model','authentication',1,6);
 `, first)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO channel_monitor_v2_latency_histograms_rollup
 (bucket_start,bucket_seconds,platform,group_id,model,user_id,metric,upper_bound_ms,sample_count)
 VALUES($1,300,'openai',42,'model',0,'ttft',1000,30),
 ($1,300,'openai',42,'model',0,'ttft',5000,30)`, first)
	require.NoError(t, err)
	result, err = store.GetSummaryByGroupIDs(t.Context(), []int64{42}, 1, 5, "UTC", now)
	require.NoError(t, err)
	require.Equal(t, "healthy", result[42].Health.Status)
	require.EqualValues(t, 1000, *result[42].Health.TTFTP50Ms)
	require.InDelta(t, 55.0/61, *result[42].AvailabilityRate, 0.000001)
	// A non-day-dividing interval must use PostgreSQL's epoch phase, and widths
	// smaller than a source multiple are rounded up before assigning fixed facts.
	irregular, err := store.GetSummaryByGroupIDs(t.Context(), []int64{42}, 1, 23, "UTC", now)
	require.NoError(t, err)
	require.Equal(t, 25, irregular[42].BucketMinutes)
	require.EqualValues(t, 61, irregular[42].TotalCount)
	for _, bucket := range irregular[42].Days {
		at, parseErr := time.Parse(time.RFC3339, bucket.Date)
		require.NoError(t, parseErr)
		require.Zero(t, at.Unix()%(25*60))
	}
	_, err = db.Exec(`UPDATE channel_monitor_v2_config SET enabled=false WHERE id=1`)
	require.NoError(t, err)
	result, err = store.GetSummaryByGroupIDs(t.Context(), []int64{42}, 1, 5, "UTC", now)
	require.NoError(t, err)
	require.Empty(t, result)
}
