package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/TokenFlux/TokenRouter/internal/ops/monitor"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// group_availability.go reads passive channel health for marketplace group cards.
// Request counts, ignored failures, cache tokens, and latency histograms share
// the configured platform and group scope. A single SQL statement reads those
// observations and their committed watermark so each card can distinguish an
// unknown measurement from incomplete history. Window and bucket health use
// the same Kedaya scorer as the operational channel monitor.
//
// Fixed rollup tiers have different retention periods. Output intervals are
// rounded to source-width multiples and aligned to the Unix epoch used by
// PostgreSQL date_bin. Histogram bounds are merged before selecting percentiles;
// neither window nor bucket latency averages precomputed percentile values.
// Successful-request ratios retain ignored failures, while health scoring
// removes them from its error numerator. No read invokes an upstream test or
// changes provider scheduling, credentials, or settlement state.

// passiveGroupObservationSQL combines totals and history in one database snapshot.
// NULL bucket starts identify whole-window rows, and histogram ranks retain the
// same enclosing upper-bound percentile estimate used by channel monitor queries.
const passiveGroupObservationSQL = `
WITH config AS (
 SELECT c.*, w.data_through, w.usage_coverage_start, w.error_coverage_start
 FROM channel_monitor_v2_config c
 JOIN channel_monitor_v2_watermarks w ON w.id=c.id AND w.data_through IS NOT NULL
 WHERE c.id=1 AND c.enabled
), scoped AS (
 SELECT m.* FROM channel_monitor_v2_metrics_rollup m CROSS JOIN config c
 WHERE m.bucket_seconds=$2 AND m.group_id=ANY($3)
 AND m.bucket_start >= $4 AND m.bucket_start < LEAST($5,c.data_through)
 AND (cardinality(c.group_ids)=0 OR m.group_id=ANY(c.group_ids))
 AND EXISTS (SELECT 1 FROM jsonb_array_elements(c.platforms) p
 WHERE p->>'platform'=m.platform AND (p->>'enabled')::boolean)
), facts AS (
 SELECT group_id,
 date_bin($1::interval,bucket_start,TIMESTAMPTZ '1970-01-01 00:00:00+00') AS bucket,
 SUM(success_requests) AS success, SUM(error_requests) AS errors,
 SUM(input_tokens) AS input, SUM(cache_creation_tokens) AS creation,
 SUM(cache_read_tokens) AS cached
 FROM scoped
 GROUP BY GROUPING SETS ((group_id,bucket),(group_id))
), ignored AS (
 SELECT e.group_id,
 date_bin($1::interval,e.bucket_start,TIMESTAMPTZ '1970-01-01 00:00:00+00') AS bucket,
 SUM(e.error_requests) AS errors
 FROM channel_monitor_v2_error_metrics_rollup e
 JOIN scoped m USING(bucket_start,bucket_seconds,platform,group_id,model)
 CROSS JOIN config c
 WHERE e.error_category=ANY(c.ignored_error_categories) AND e.taxonomy_version=1
 GROUP BY GROUPING SETS ((e.group_id,bucket),(e.group_id))
), hist AS (
 SELECT h.group_id,
 date_bin($1::interval,h.bucket_start,TIMESTAMPTZ '1970-01-01 00:00:00+00') AS bucket,
 h.upper_bound_ms, SUM(h.sample_count) AS count
 FROM channel_monitor_v2_latency_histograms_rollup h
 JOIN scoped m USING(bucket_start,bucket_seconds,platform,group_id,model)
 WHERE h.metric='ttft' AND h.user_id=0
 GROUP BY GROUPING SETS ((h.group_id,bucket,h.upper_bound_ms),(h.group_id,h.upper_bound_ms))
), ranked AS (
 SELECT *, SUM(count) OVER(PARTITION BY group_id,bucket ORDER BY upper_bound_ms) AS cumulative,
 SUM(count) OVER(PARTITION BY group_id,bucket) AS total FROM hist
), latency AS (
 SELECT group_id,bucket,MIN(upper_bound_ms) FILTER(WHERE cumulative>=total*0.5) AS p50,
 MAX(total) AS count FROM ranked GROUP BY group_id,bucket
)
SELECT f.group_id,f.bucket,f.success,f.errors,f.input,f.creation,f.cached,
 COALESCE(i.errors,0),l.p50,COALESCE(l.count,0),c.health_thresholds,c.data_through,
 c.usage_coverage_start <= $4 AND c.error_coverage_start <= $4,c.refresh_interval_seconds
FROM facts f CROSS JOIN config c
LEFT JOIN ignored i ON i.group_id=f.group_id AND i.bucket IS NOT DISTINCT FROM f.bucket
LEFT JOIN latency l ON l.group_id=f.group_id AND l.bucket IS NOT DISTINCT FROM f.bucket
ORDER BY f.group_id,f.bucket NULLS FIRST`

// GroupAvailabilityStore reads committed passive group observations through SQL.
type GroupAvailabilityStore struct {
	db *sql.DB // application-owned SQL pool
}

// NewGroupAvailabilityStore retains the application SQL pool for public passive
// summaries. Construction performs no queries, and the caller keeps storage
// available until marketplace request handlers complete during application shutdown.
func NewGroupAvailabilityStore(db *sql.DB) *GroupAvailabilityStore {
	return &GroupAvailabilityStore{db: db}
}

// GetSummaryByGroupIDs returns request ratios, Kedaya health, and fixed history
// for the selected public groups. Settings restrict platform and group scope;
// disabled monitoring or missing committed data produces no measured summary.
// Both summary and bucket health require the configured minimum request count.
//
// The source tier covers the requested retention period, and output widths are
// raised to compatible multiples before epoch-aligned buckets are seeded. The
// single query returns aggregate rows first, followed by observed history cells.
// Missing cells remain unknown. Cancellation propagates through SQL and no
// partial result is returned after a scan failure. Rates and nullable latency
// values retain their units when copied into the public presentation records.
func (s *GroupAvailabilityStore) GetSummaryByGroupIDs(ctx context.Context, ids []int64, days, minutes int, zone string, now time.Time) (map[int64]*routing.GroupAvailabilitySummary, error) {
	days, minutes = routing.NormalizeMarketplaceAvailabilityWindow(days, minutes)
	sourceSeconds := 300
	if days > 7 {
		sourceSeconds = 3600
	}
	if days > 30 {
		sourceSeconds = 43200
	}
	if days > 45 {
		sourceSeconds = 86400
	}
	// Coarse retained rows cannot be subdivided into smaller observations. Round
	// output intervals to source multiples so adjacent cells never overlap facts.
	minutes = ((minutes*60 + sourceSeconds - 1) / sourceSeconds) * sourceSeconds / 60
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("passive availability timezone: %w", err)
	}
	width := time.Duration(minutes) * time.Minute
	// Unix epoch alignment matches PostgreSQL for intervals that do not divide
	// one UTC day, keeping database bucket labels and output array indexes equal.
	origin := time.Unix(0, 0).UTC()
	end := origin.Add(now.Sub(origin).Truncate(width)).Add(width)
	start := origin.Add(end.Add(-time.Duration(days) * 24 * time.Hour).Sub(origin).Truncate(width))
	result := make(map[int64]*routing.GroupAvailabilitySummary, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.db.QueryContext(ctx, passiveGroupObservationSQL, fmt.Sprintf("%d seconds", minutes*60), sourceSeconds, pq.Array(ids), start, end)
	if err != nil {
		return nil, err
	}
	// Closing the cursor releases its connection after success or a scan error;
	// query cancellation and row errors remain visible to the marketplace reader.
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, ignored int64
		var at sql.NullTime
		var p50 sql.NullInt64
		var raw []byte
		var through time.Time
		var complete bool
		var refresh int
		var metrics monitor.ChannelMonitorV2Metric
		if err := rows.Scan(&id, &at, &metrics.SuccessRequests, &metrics.ErrorRequests,
			&metrics.InputTokens, &metrics.CacheCreationTokens, &metrics.CacheReadTokens,
			&ignored, &p50, &metrics.TTFT.SampleCount, &raw, &through, &complete, &refresh); err != nil {
			return nil, err
		}
		thresholds := monitor.DefaultChannelMonitorV2HealthThresholds()
		if err := json.Unmarshal(raw, &thresholds); err != nil {
			return nil, fmt.Errorf("passive availability health thresholds: %w", err)
		}
		thresholds = monitor.NormalizeChannelMonitorV2HealthThresholds(thresholds)
		metrics.RequestCount = metrics.SuccessRequests + metrics.ErrorRequests
		if metrics.RequestCount > 0 {
			metrics.SuccessRate = float64(metrics.SuccessRequests) / float64(metrics.RequestCount)
			metrics.ErrorRate = float64(max(metrics.ErrorRequests-ignored, 0)) / float64(metrics.RequestCount)
		}
		metrics.CacheRateDenominator = metrics.InputTokens + metrics.CacheCreationTokens + metrics.CacheReadTokens
		if metrics.CacheRateDenominator > 0 {
			metrics.CacheRate = float64(metrics.CacheReadTokens) / float64(metrics.CacheRateDenominator)
		}
		if p50.Valid {
			metrics.TTFT.P50Ms = &p50.Int64
		}
		health := passiveGroupHealth(metrics, thresholds)
		var rate *float64
		if metrics.RequestCount >= thresholds.MinimumSample {
			rate = &metrics.SuccessRate
		}
		summary := result[id]
		if summary == nil {
			summary = &routing.GroupAvailabilitySummary{
				WindowDays: days, BucketMinutes: minutes, LastStatus: "unknown",
				DataThrough: &through, CoverageComplete: complete, RefreshIntervalSeconds: refresh,
			}
			for bucket := start; bucket.Before(end); bucket = bucket.Add(width) {
				summary.Days = append(summary.Days, routing.GroupAvailabilityBucket{Date: bucket.In(location).Format(time.RFC3339)})
			}
			result[id] = summary
		}
		if !at.Valid {
			summary.SuccessCount, summary.TotalCount = metrics.SuccessRequests, metrics.RequestCount
			summary.AvailabilityRate, summary.Health = rate, health
			continue
		}
		index := int(at.Time.Sub(start) / width)
		if index < 0 || index >= len(summary.Days) {
			continue
		}
		bucket := &summary.Days[index]
		bucket.SuccessCount, bucket.TotalCount = metrics.SuccessRequests, metrics.RequestCount
		bucket.AvailabilityRate, bucket.Health = rate, health
		checked := at.Time.Add(width)
		if checked.After(through) {
			checked = through
		}
		summary.LastCheckedAt, summary.LastStatus = &checked, health.Status
	}
	return result, rows.Err()
}

// passiveGroupHealth applies the shared scorer and exposes measured signal
// summaries. Unknown latency or cache signals remain absent even when another
// signal produced an overall score; public values never imply missing samples.
func passiveGroupHealth(metrics monitor.ChannelMonitorV2Metric, thresholds monitor.ChannelMonitorV2HealthThresholds) *routing.GroupAvailabilityHealth {
	health := monitor.ChannelMonitorV2HealthForWithThresholds(metrics, thresholds)
	result := &routing.GroupAvailabilityHealth{Status: health.Overall, Score: health.Score}
	if health.TTFT != "unknown" {
		result.TTFTP50Ms = metrics.TTFT.P50Ms
	}
	if health.Cache != "unknown" {
		result.CacheRate = &metrics.CacheRate
	}
	return result
}
