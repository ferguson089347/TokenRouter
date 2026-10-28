package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/TokenFlux/TokenRouter/internal/ops/monitor"
)

// monitor.go persists Kedaya V2 passive channel queries and configuration.
// Queries use stored request platforms and the shared usage performance SQL
// expressions. Fixed time windows remain rebuildable from retained usage and
// error data; each successful aggregation commits its facts and durable cursor
// together, allowing interrupted workers to repeat a range safely.
//
// Administrator queries retain traffic volumes and diagnostic samples. The
// monitor service applies ordinary-viewer redaction after the repository has
// intersected requested groups with server-derived permissions. Historical
// coverage is reported from committed watermarks, and empty buckets remain
// distinguishable from missing coverage rather than implying successful calls.
// Adapted from kiss-kedaya/sub2api at 0ff60acabffe06a8de2d82f8b0ba657f5551512f.

// channelMonitorV2Repository represents channel monitor v2 repository data used by passive channel queries.
type channelMonitorV2Repository struct {
	db *sql.DB // borrowed application SQL pool
}

// channelMonitorV2Fact represents channel monitor v2 fact data used by passive channel queries.
type channelMonitorV2Fact struct {
	BucketStart      string // UTC bucket timestamp text
	Platform         string // saved execution platform name
	GroupName        string // current group display name
	Model            string // stored requested model name
	GroupID          int64  // recorded group database identifier
	Success          int64  // eligible successful request count
	Errors           int64  // deduplicated failed request count
	UpstreamAffected int64  // requests with upstream failures
	UpstreamAttempts int64  // recorded upstream error attempts
	Input            int64  // uncached input token count
	Output           int64  // generated output token count
	CacheCreation    int64  // cache creation token count
	CacheRead        int64  // cached input token count
	TTFTSum          int64  // milliseconds
	TTFTCount        int64  // valid first-output sample count
	DurationSum      int64  // milliseconds
	DurationCount    int64  // valid exchange duration count
}

// channelMonitorV2Histogram contains counts at one stored latency upper bound.
type channelMonitorV2Histogram struct {
	BucketStart string // UTC bucket timestamp text
	Platform    string // saved execution platform name
	Model       string // stored requested model name
	Metric      string // latency histogram metric name
	GroupID     int64  // observed group database identifier
	UserID      int64  // observed acting user identifier
	UpperBound  int64  // milliseconds
	Count       int64  // observed latency sample count
}

// channelMonitorV2MatrixKey identifies one configured platform, group, and model combination.
type channelMonitorV2MatrixKey struct {
	platform string // saved execution platform name
	model    string // resolved display model bucket
	groupID  int64  // observed group database identifier
}

// channelMonitorV2MatrixAccumulator combines a dimension total and individual history intervals.
type channelMonitorV2MatrixAccumulator struct {
	groupName string                        // current group display name
	total     *metricAccumulator            // complete row measurement accumulator
	buckets   map[string]*metricAccumulator // per-bucket private measurement accumulators
}

// channelMonitorV2GroupInfo contains current labels used to seed empty matrix rows.
type channelMonitorV2GroupInfo struct {
	name     string // current group display name
	platform string // optional provider platform hint
}

// metricAccumulator combines additive fact measurements with mergeable latency histogram counts.
type metricAccumulator struct {
	success          int64                      // eligible successful request count
	errors           int64                      // deduplicated failed request count
	upstreamAffected int64                      // requests with upstream failures
	upstreamAttempts int64                      // recorded upstream error attempts
	input            int64                      // uncached input token count
	output           int64                      // generated output token count
	cacheCreation    int64                      // cache creation token count
	cacheRead        int64                      // cached input token count
	ttftSum          int64                      // milliseconds
	ttftCount        int64                      // valid first-output sample count
	durationSum      int64                      // milliseconds
	durationCount    int64                      // valid exchange duration count
	hist             map[string]map[int64]int64 // per-metric latency bound counts
}

// NewChannelMonitorV2Repository retains the application SQL pool for passive
// channel persistence. The caller keeps the pool open until query handlers and
// the background aggregator have completed; construction performs no queries or
// schema changes.
func NewChannelMonitorV2Repository(db *sql.DB) monitor.ChannelMonitorV2Repository {
	return &channelMonitorV2Repository{db: db}
}

// GetConfig loads persisted monitor settings and appends missing known platform
// entries. Returned platform defaults affect this query result; persisting them
// requires a separate version-checked configuration update by an authenticated
// administrator.
func (r *channelMonitorV2Repository) GetConfig(ctx context.Context) (*monitor.ChannelMonitorV2Config, error) {
	var cfg monitor.ChannelMonitorV2Config
	var platforms, thresholds []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT version, enabled, refresh_interval_seconds, platforms, group_ids,
		       COALESCE(ignored_error_categories, '{}'),
		       COALESCE(health_thresholds, '{}'::jsonb),
		       updated_at, updated_by
		FROM channel_monitor_v2_config WHERE id = 1`).Scan(
		&cfg.Version, &cfg.Enabled, &cfg.RefreshIntervalSeconds, &platforms,
		pq.Array(&cfg.GroupIDs), pq.Array(&cfg.IgnoredErrorCategories),
		&thresholds,
		&cfg.UpdatedAt, &cfg.UpdatedBy,
	)
	if err != nil {
		return nil, fmt.Errorf("get channel monitor v2 config: %w", err)
	}
	if err := json.Unmarshal(platforms, &cfg.Platforms); err != nil {
		return nil, fmt.Errorf("decode channel monitor v2 platforms: %w", err)
	}
	if cfg.IgnoredErrorCategories == nil {
		cfg.IgnoredErrorCategories = []string{}
	}
	cfg.HealthThresholds = monitor.DefaultChannelMonitorV2HealthThresholds()
	if len(thresholds) > 0 {
		_ = json.Unmarshal(thresholds, &cfg.HealthThresholds)
	}
	cfg.HealthThresholds = monitor.NormalizeChannelMonitorV2HealthThresholds(cfg.HealthThresholds)
	return &cfg, nil
}

// UpdateConfig normalizes platform scopes, groups, ignored categories, and
// health thresholds before replacing configuration. The actor identifies the
// administrator, and expectedVersion makes concurrent edits fail with a conflict
// instead of replacing a newer save.
func (r *channelMonitorV2Repository) UpdateConfig(ctx context.Context, cfg monitor.ChannelMonitorV2Config, expectedVersion int) (*monitor.ChannelMonitorV2Config, error) {
	platforms, err := json.Marshal(cfg.Platforms)
	if err != nil {
		return nil, err
	}
	if cfg.IgnoredErrorCategories == nil {
		cfg.IgnoredErrorCategories = []string{}
	}
	cfg.HealthThresholds = monitor.NormalizeChannelMonitorV2HealthThresholds(cfg.HealthThresholds)
	thresholds, err := json.Marshal(cfg.HealthThresholds)
	if err != nil {
		return nil, err
	}
	var updated monitor.ChannelMonitorV2Config
	var raw, rawThresholds []byte
	err = r.db.QueryRowContext(ctx, `
		UPDATE channel_monitor_v2_config
		SET version = version + 1, enabled = $1, refresh_interval_seconds = $2,
		    platforms = $3, group_ids = $4, ignored_error_categories = $5,
		    health_thresholds = $6, updated_by = $7, updated_at = NOW()
		WHERE id = 1 AND version = $8
		RETURNING version, enabled, refresh_interval_seconds, platforms, group_ids,
		          COALESCE(ignored_error_categories, '{}'),
		          COALESCE(health_thresholds, '{}'::jsonb),
		          updated_at, updated_by`,
		cfg.Enabled, cfg.RefreshIntervalSeconds, platforms, pq.Array(cfg.GroupIDs),
		pq.Array(cfg.IgnoredErrorCategories), thresholds, cfg.UpdatedBy, expectedVersion,
	).Scan(&updated.Version, &updated.Enabled, &updated.RefreshIntervalSeconds, &raw,
		pq.Array(&updated.GroupIDs), pq.Array(&updated.IgnoredErrorCategories),
		&rawThresholds,
		&updated.UpdatedAt, &updated.UpdatedBy)
	if err == sql.ErrNoRows {
		return nil, monitor.ErrChannelMonitorV2ConfigConflict
	}
	if err != nil {
		return nil, fmt.Errorf("update channel monitor v2 config: %w", err)
	}
	if err := json.Unmarshal(raw, &updated.Platforms); err != nil {
		return nil, err
	}
	if updated.IgnoredErrorCategories == nil {
		updated.IgnoredErrorCategories = []string{}
	}
	updated.HealthThresholds = monitor.DefaultChannelMonitorV2HealthThresholds()
	_ = json.Unmarshal(rawThresholds, &updated.HealthThresholds)
	updated.HealthThresholds = monitor.NormalizeChannelMonitorV2HealthThresholds(updated.HealthThresholds)
	return &updated, nil
}

// GetDimensions loads observed and configured filter choices within common
// committed coverage. Catalog choices ignore the current platform, group, and
// model selections so changing one filter does not erase the others.
// Configuration scopes and server-authorized group intersections still apply to
// storage reads. Counts are accumulated independently for platforms, groups, and
// platform-specific model names, leaving shared textual model names separate
// when they belong to different provider families.
func (r *channelMonitorV2Repository) GetDimensions(ctx context.Context, filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config) (*monitor.ChannelMonitorV2Dimensions, error) {
	if channelMonitorV2RestrictedGroupScopeEmpty(filter, cfg) {
		return &monitor.ChannelMonitorV2Dimensions{
			Platforms: []monitor.ChannelMonitorV2Dimension{},
			Groups:    []monitor.ChannelMonitorV2GroupDimension{},
			Models:    []monitor.ChannelMonitorV2Dimension{},
		}, nil
	}
	coverage, err := r.loadCoverage(ctx, filter)
	if err != nil {
		return nil, err
	}
	// Catalog dimensions must stay independent of the currently selected
	// platform/group/model multi-select filters so pickers keep their full option set.
	// Time/coverage still come from the request filter; config scope (enabled platforms,
	// group allow-list, display-model collapse) still applies.
	catalogFilter := channelMonitorV2CatalogFilter(channelMonitorV2CommonCoverageFilter(filter, *coverage))
	where, args, _ := channelMonitorV2WhereWithRollup(catalogFilter, cfg, "m")
	query := `SELECT m.platform, COALESCE(g.name, ''), 'unknown', m.group_id, m.model,
	                 SUM(m.success_requests + m.error_requests)
	          FROM ` + channelMonitorV2MetricsTable(catalogFilter) + ` m
	          LEFT JOIN groups g ON g.id = NULLIF(m.group_id, 0) ` + where + `
	          GROUP BY m.platform, g.name, m.group_id, m.model`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	platformCounts := map[string]int64{}
	// modelValue keeps each model choice tied to its platform-specific volume.
	type modelValue struct {
		platform string // observed execution platform name
		count    int64  // scoped eligible request count
	}
	// Model dimensions are platform-scoped; identical names can represent
	// different upstream models and must not share counts.
	modelCounts := map[string]modelValue{}
	// groupValue accumulates current labels and scoped traffic for one group.
	type groupValue struct {
		name     string // current group display name
		platform string // optional provider family hint
		count    int64  // scoped eligible request count
	}
	groupCounts := map[int64]groupValue{}
	for _, platform := range channelMonitorV2EnabledPlatforms(cfg) {
		platformCounts[platform] += 0
		for _, p := range cfg.Platforms {
			if p.Platform != platform || len(p.Models) == 0 {
				continue
			}
			for _, model := range p.Models {
				modelCounts[platform+"\x00"+model] = modelValue{platform: platform, count: 0}
			}
		}
	}
	groupInfo, err := r.loadChannelMonitorV2GroupInfo(ctx, configuredChannelMonitorV2GroupIDs(catalogFilter, cfg))
	if err != nil {
		return nil, err
	}
	for groupID, info := range groupInfo {
		groupCounts[groupID] = groupValue{name: info.name, platform: info.platform, count: 0}
	}
	for rows.Next() {
		var platform, groupName, groupPlatform, model string
		var groupID, count int64
		if err := rows.Scan(&platform, &groupName, &groupPlatform, &groupID, &model, &count); err != nil {
			return nil, err
		}
		platformCounts[platform] += count
		displayModel := channelMonitorV2DisplayModel(cfg, platform, model)
		modelKey := platform + "\x00" + displayModel
		currentModel := modelCounts[modelKey]
		currentModel.count += count
		if currentModel.platform == "" {
			currentModel.platform = platform
		}
		modelCounts[modelKey] = currentModel
		if groupID > 0 {
			current := groupCounts[groupID]
			current.name = groupName
			current.platform = groupPlatform
			current.count += count
			groupCounts[groupID] = current
		}
	}
	result := &monitor.ChannelMonitorV2Dimensions{
		Platforms: []monitor.ChannelMonitorV2Dimension{},
		Groups:    []monitor.ChannelMonitorV2GroupDimension{},
		Models:    []monitor.ChannelMonitorV2Dimension{},
	}
	for value, count := range platformCounts {
		result.Platforms = append(result.Platforms, monitor.ChannelMonitorV2Dimension{Value: value, Label: value, RequestCount: count})
	}
	for value, meta := range modelCounts {
		result.Models = append(result.Models, monitor.ChannelMonitorV2Dimension{Value: value, Label: channelMonitorV2ModelLabel(value), Platform: meta.platform, RequestCount: meta.count})
	}
	for id, value := range groupCounts {
		result.Groups = append(result.Groups, monitor.ChannelMonitorV2GroupDimension{ID: id, Name: value.name, Platform: value.platform, RequestCount: value.count})
	}
	sort.Slice(result.Platforms, func(i, j int) bool { return result.Platforms[i].RequestCount > result.Platforms[j].RequestCount })
	sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].RequestCount > result.Models[j].RequestCount })
	sort.Slice(result.Groups, func(i, j int) bool { return result.Groups[i].RequestCount > result.Groups[j].RequestCount })
	return result, rows.Err()
}

// GetSnapshot combines scoped minute or fixed-rollup facts with latency
// histograms and ignored-error counts. Summary metrics and trend buckets share
// the common committed coverage interval. Ignored categories reduce the scored
// error numerator while absolute requests and true success rates retain those
// failures. Health is computed after that correction, and fixed bucket seeding
// keeps unobserved portions of the selected display window available with
// unknown health.
func (r *channelMonitorV2Repository) GetSnapshot(ctx context.Context, filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config, admin bool) (*monitor.ChannelMonitorV2Snapshot, error) {
	if channelMonitorV2RestrictedGroupScopeEmpty(filter, cfg) {
		return &monitor.ChannelMonitorV2Snapshot{Trend: []monitor.ChannelMonitorV2TrendPoint{}}, nil
	}
	coverage, err := r.loadCoverage(ctx, filter)
	if err != nil {
		return nil, err
	}
	effectiveFilter := channelMonitorV2CommonCoverageFilter(filter, *coverage)
	facts, err := r.loadFacts(ctx, effectiveFilter, cfg, true)
	if err != nil {
		return nil, err
	}
	histograms, err := r.loadHistograms(ctx, effectiveFilter, cfg, 0, true)
	if err != nil {
		return nil, err
	}
	byBucket := map[string]*metricAccumulator{}
	total := newMetricAccumulator()
	for _, fact := range facts {
		if !channelMonitorV2ModelSelected(filter, cfg, fact.Platform, fact.Model) {
			continue
		}
		bucket := fact.BucketStart
		acc := byBucket[bucket]
		if acc == nil {
			acc = newMetricAccumulator()
			byBucket[bucket] = acc
		}
		acc.addFact(fact)
		total.addFact(fact)
	}
	for _, hist := range histograms {
		if !channelMonitorV2ModelSelected(filter, cfg, hist.Platform, hist.Model) {
			continue
		}
		if acc := byBucket[hist.BucketStart]; acc != nil {
			acc.addHistogram(hist)
		}
		total.addHistogram(hist)
	}
	minutes := channelMonitorV2CoveredMinutes(filter, *coverage)
	// Category-level ignored errors adjust error_rate without dropping volume.
	ignoredByBucket, ignoredTotal, err := r.loadIgnoredErrorCounts(ctx, effectiveFilter, cfg)
	if err != nil {
		return nil, fmt.Errorf("load ignored error counts: %w", err)
	}
	metrics := total.metric(minutes, admin)
	applyIgnoredErrors(&metrics, ignoredTotal)
	if !admin {
		cfg.UpdatedBy = nil
	}
	result := &monitor.ChannelMonitorV2Snapshot{Config: cfg, Coverage: *coverage, Metrics: metrics, Health: monitor.ChannelMonitorV2HealthForWithThresholds(metrics, cfg.HealthThresholds), Trend: []monitor.ChannelMonitorV2TrendPoint{}}
	keys := make([]string, 0, len(byBucket))
	for key := range byBucket {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		bucket, _ := time.Parse(time.RFC3339Nano, key)
		m := byBucket[key].metric(filter.Bucket.Minutes(), admin)
		applyIgnoredErrors(&m, ignoredByBucket[key])
		result.Trend = append(result.Trend, monitor.ChannelMonitorV2TrendPoint{BucketStart: bucket, Metrics: m, Health: monitor.ChannelMonitorV2HealthForWithThresholds(m, cfg.HealthThresholds)})
	}
	return result, nil
}

// GetModels groups scoped facts by platform and configured display model before
// calculating health. Unlisted configured models collapse to the other-model
// bucket, while empty model lists retain actual names. Histogram counts and
// ignored-error counts use the same displayed keys as request metrics. Every
// returned model receives its own health decision and latency percentiles, and
// administrator mode controls whether optional upstream-attempt counters are
// populated.
func (r *channelMonitorV2Repository) GetModels(ctx context.Context, filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config, admin bool) (*monitor.ChannelMonitorV2List[monitor.ChannelMonitorV2ModelRow], error) {
	if channelMonitorV2RestrictedGroupScopeEmpty(filter, cfg) {
		return &monitor.ChannelMonitorV2List[monitor.ChannelMonitorV2ModelRow]{Items: []monitor.ChannelMonitorV2ModelRow{}}, nil
	}
	coverage, err := r.loadCoverage(ctx, filter)
	if err != nil {
		return nil, err
	}
	effectiveFilter := channelMonitorV2CommonCoverageFilter(filter, *coverage)
	facts, err := r.loadFacts(ctx, effectiveFilter, cfg, false)
	if err != nil {
		return nil, err
	}
	hist, err := r.loadHistograms(ctx, effectiveFilter, cfg, 0, false)
	if err != nil {
		return nil, err
	}
	accs := map[string]*metricAccumulator{}
	for _, platform := range channelMonitorV2EnabledPlatforms(cfg) {
		if len(filter.Platforms) > 0 && !containsString(filter.Platforms, platform) {
			continue
		}
		models := configuredChannelMonitorV2Models(cfg, platform, filter)
		for _, model := range models {
			if model == "" {
				continue
			}
			key := platform + "\x00" + model
			if accs[key] == nil {
				accs[key] = newMetricAccumulator()
			}
		}
	}
	for _, fact := range facts {
		if !channelMonitorV2ModelSelected(filter, cfg, fact.Platform, fact.Model) {
			continue
		}
		model := channelMonitorV2DisplayModel(cfg, fact.Platform, fact.Model)
		key := fact.Platform + "\x00" + model
		if accs[key] == nil {
			accs[key] = newMetricAccumulator()
		}
		accs[key].addFact(fact)
	}
	for _, h := range hist {
		if !channelMonitorV2ModelSelected(filter, cfg, h.Platform, h.Model) {
			continue
		}
		key := h.Platform + "\x00" + channelMonitorV2DisplayModel(cfg, h.Platform, h.Model)
		if accs[key] != nil {
			accs[key].addHistogram(h)
		}
	}
	// Per platform+model ignored counts for error_rate adjustment.
	ignoredByPM, _, err := r.loadIgnoredErrorCountsByPlatformModel(ctx, effectiveFilter, cfg)
	if err != nil {
		return nil, fmt.Errorf("load ignored error counts by platform/model: %w", err)
	}
	items := make([]monitor.ChannelMonitorV2ModelRow, 0, len(accs))
	minutes := channelMonitorV2CoveredMinutes(filter, *coverage)
	for key, acc := range accs {
		parts := strings.SplitN(key, "\x00", 2)
		metrics := acc.metric(minutes, admin)
		applyIgnoredErrors(&metrics, ignoredByPM[key])
		items = append(items, monitor.ChannelMonitorV2ModelRow{Platform: parts[0], Model: parts[1], Metrics: metrics, Health: monitor.ChannelMonitorV2HealthForWithThresholds(metrics, cfg.HealthThresholds)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Metrics.RequestCount > items[j].Metrics.RequestCount })
	return &monitor.ChannelMonitorV2List[monitor.ChannelMonitorV2ModelRow]{Coverage: *coverage, Items: items}, nil
}

// GetMatrix builds totals and fixed history cells for the selected platform,
// group, and model dimensions. Fact counts, histogram bounds, and ignored
// categories are joined by the same dimension and time keys before health
// scoring. Configured rows can be seeded without traffic, retaining unknown
// health for their empty cells. Group scope intersects configuration with viewer
// permissions before storage queries, so explicit empty permission sets cannot
// expand into unrestricted results.
func (r *channelMonitorV2Repository) GetMatrix(ctx context.Context, filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config, groupBy monitor.ChannelMonitorV2GroupBy, admin bool) (*monitor.ChannelMonitorV2Matrix, error) {
	if channelMonitorV2RestrictedGroupScopeEmpty(filter, cfg) {
		return &monitor.ChannelMonitorV2Matrix{GroupBy: groupBy, Items: []monitor.ChannelMonitorV2MatrixRow{}}, nil
	}
	coverage, err := r.loadCoverage(ctx, filter)
	if err != nil {
		return nil, err
	}
	effectiveFilter := channelMonitorV2CommonCoverageFilter(filter, *coverage)
	facts, err := r.loadFacts(ctx, effectiveFilter, cfg, true)
	if err != nil {
		return nil, err
	}
	histograms, err := r.loadHistograms(ctx, effectiveFilter, cfg, 0, true)
	if err != nil {
		return nil, err
	}

	seedGroupIDs := configuredChannelMonitorV2GroupIDs(filter, cfg)
	// Empty config group list means all groups — load active groups so matrix seed
	// can materialize real platform/group rows (not bare platform placeholders).
	if len(seedGroupIDs) == 0 && !filter.RestrictGroups &&
		(groupBy == monitor.ChannelMonitorV2GroupByPlatformGroup || groupBy == monitor.ChannelMonitorV2GroupByPlatformGroupModel) {
		allIDs, loadErr := r.listActiveGroupIDs(ctx)
		if loadErr != nil {
			return nil, loadErr
		}
		seedGroupIDs = allIDs
	}
	groupInfo, err := r.loadChannelMonitorV2GroupInfo(ctx, seedGroupIDs)
	if err != nil {
		return nil, err
	}
	accs := seedChannelMonitorV2MatrixAccumulators(filter, cfg, groupBy, groupInfo)
	for _, fact := range facts {
		if !channelMonitorV2ModelSelected(filter, cfg, fact.Platform, fact.Model) {
			continue
		}
		// platform_group dimensions require a real group; skip group_id=0 traffic rows.
		if (groupBy == monitor.ChannelMonitorV2GroupByPlatformGroup || groupBy == monitor.ChannelMonitorV2GroupByPlatformGroupModel) &&
			fact.GroupID <= 0 {
			continue
		}
		key := channelMonitorV2MatrixDimensionKey(groupBy, cfg, fact.Platform, fact.GroupID, fact.Model)
		acc := accs[key]
		if acc == nil {
			acc = &channelMonitorV2MatrixAccumulator{total: newMetricAccumulator(), buckets: make(map[string]*metricAccumulator)}
			accs[key] = acc
		}
		if key.groupID > 0 {
			acc.groupName = fact.GroupName
		}
		bucket := acc.buckets[fact.BucketStart]
		if bucket == nil {
			bucket = newMetricAccumulator()
			acc.buckets[fact.BucketStart] = bucket
		}
		acc.total.addFact(fact)
		bucket.addFact(fact)
	}
	for _, histogram := range histograms {
		if !channelMonitorV2ModelSelected(filter, cfg, histogram.Platform, histogram.Model) {
			continue
		}
		if (groupBy == monitor.ChannelMonitorV2GroupByPlatformGroup || groupBy == monitor.ChannelMonitorV2GroupByPlatformGroupModel) &&
			histogram.GroupID <= 0 {
			continue
		}
		key := channelMonitorV2MatrixDimensionKey(groupBy, cfg, histogram.Platform, histogram.GroupID, histogram.Model)
		acc := accs[key]
		if acc == nil {
			continue
		}
		acc.total.addHistogram(histogram)
		if bucket := acc.buckets[histogram.BucketStart]; bucket != nil {
			bucket.addHistogram(histogram)
		}
	}

	result := &monitor.ChannelMonitorV2Matrix{GroupBy: groupBy, Coverage: *coverage, Items: make([]monitor.ChannelMonitorV2MatrixRow, 0, len(accs))}
	minutes := channelMonitorV2CoveredMinutes(filter, *coverage)
	ignoredByDimBucket, ignoredByDim, err := r.loadIgnoredErrorCountsByMatrixKey(ctx, effectiveFilter, cfg, groupBy)
	if err != nil {
		return nil, fmt.Errorf("load ignored error counts by matrix key: %w", err)
	}
	for key, acc := range accs {
		// platform_group views only emit rows with a real group_id.
		if (groupBy == monitor.ChannelMonitorV2GroupByPlatformGroup || groupBy == monitor.ChannelMonitorV2GroupByPlatformGroupModel) &&
			key.groupID <= 0 {
			continue
		}
		metrics := acc.total.metric(minutes, admin)
		applyIgnoredErrors(&metrics, ignoredByDim[key])
		row := monitor.ChannelMonitorV2MatrixRow{Platform: key.platform, GroupName: acc.groupName, Model: key.model, Metrics: metrics, Health: monitor.ChannelMonitorV2HealthForWithThresholds(metrics, cfg.HealthThresholds), Buckets: []monitor.ChannelMonitorV2TrendPoint{}}
		if key.groupID > 0 {
			groupID := key.groupID
			row.GroupID = &groupID
		}
		bucketKeys := make([]string, 0, len(acc.buckets))
		for bucket := range acc.buckets {
			bucketKeys = append(bucketKeys, bucket)
		}
		sort.Strings(bucketKeys)
		for _, bucketKey := range bucketKeys {
			bucketStart, parseErr := time.Parse(time.RFC3339Nano, bucketKey)
			if parseErr != nil {
				continue
			}
			bucketMetrics := acc.buckets[bucketKey].metric(filter.Bucket.Minutes(), admin)
			applyIgnoredErrors(&bucketMetrics, ignoredByDimBucket[key][bucketKey])
			row.Buckets = append(row.Buckets, monitor.ChannelMonitorV2TrendPoint{BucketStart: bucketStart, Metrics: bucketMetrics, Health: monitor.ChannelMonitorV2HealthForWithThresholds(bucketMetrics, cfg.HealthThresholds)})
		}
		result.Items = append(result.Items, row)
	}
	sort.Slice(result.Items, func(i, j int) bool {
		a, b := result.Items[i], result.Items[j]
		if a.Platform != b.Platform {
			return a.Platform < b.Platform
		}
		if a.GroupName != b.GroupName {
			return a.GroupName < b.GroupName
		}
		if a.GroupID != nil && b.GroupID != nil && *a.GroupID != *b.GroupID {
			return *a.GroupID < *b.GroupID
		}
		return a.Model < b.Model
	})
	return result, nil
}

// channelMonitorV2MatrixDimensionKey constructs the stable accumulator key for
// the selected matrix grouping.
func channelMonitorV2MatrixDimensionKey(groupBy monitor.ChannelMonitorV2GroupBy, cfg monitor.ChannelMonitorV2Config, platform string, groupID int64, model string) channelMonitorV2MatrixKey {
	key := channelMonitorV2MatrixKey{platform: platform}
	if groupBy == monitor.ChannelMonitorV2GroupByPlatformGroup || groupBy == monitor.ChannelMonitorV2GroupByPlatformGroupModel {
		key.groupID = groupID
	}
	if groupBy == monitor.ChannelMonitorV2GroupByPlatformModel || groupBy == monitor.ChannelMonitorV2GroupByPlatformGroupModel {
		key.model = channelMonitorV2DisplayModel(cfg, platform, model)
	}
	return key
}

// seedChannelMonitorV2MatrixAccumulators creates display rows for configured
// platforms, models, and permitted active groups. Group-aware dimensions require
// actual positive group identifiers; unrestricted administrators can discover
// current active groups when configuration lists none. Local groups can contain
// multiple provider platforms, so an absent platform hint permits each enabled
// platform. Empty request history seeds accumulators rather than successes,
// allowing the UI to show missing observations with unknown health.
func seedChannelMonitorV2MatrixAccumulators(filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config, groupBy monitor.ChannelMonitorV2GroupBy, groupInfo map[int64]channelMonitorV2GroupInfo) map[channelMonitorV2MatrixKey]*channelMonitorV2MatrixAccumulator {
	accs := map[channelMonitorV2MatrixKey]*channelMonitorV2MatrixAccumulator{}
	platforms := channelMonitorV2EnabledPlatforms(cfg)
	if len(filter.Platforms) > 0 {
		platforms = intersectStrings(platforms, filter.Platforms)
	}
	// Platform-only groupBy seeds groupID=0. When group is part of the dimension
	// (platform_group / platform_group_model), never seed a bare platform row —
	// only real group IDs (from config or discovered groupInfo).
	needsGroup := groupBy == monitor.ChannelMonitorV2GroupByPlatformGroup || groupBy == monitor.ChannelMonitorV2GroupByPlatformGroupModel
	groupIDs := []int64{0}
	if needsGroup {
		groupIDs = configuredChannelMonitorV2GroupIDs(filter, cfg)
		if len(groupIDs) == 0 && !filter.RestrictGroups {
			// Empty config group list means "all groups": use discovered active groups.
			for id := range groupInfo {
				groupIDs = append(groupIDs, id)
			}
			slices.Sort(groupIDs)
		}
		// Still empty → seed nothing; rows come only from fact traffic.
		if len(groupIDs) == 0 {
			return accs
		}
	}
	for _, platform := range platforms {
		models := []string{""}
		if groupBy == monitor.ChannelMonitorV2GroupByPlatformModel || groupBy == monitor.ChannelMonitorV2GroupByPlatformGroupModel {
			models = configuredChannelMonitorV2Models(cfg, platform, filter)
			if len(models) == 0 {
				models = []string{""}
			}
		}
		for _, groupID := range groupIDs {
			info := groupInfo[groupID]
			if needsGroup {
				if groupID <= 0 {
					continue
				}
				if info.platform != "" && info.platform != platform {
					continue
				}
			}
			for _, model := range models {
				key := channelMonitorV2MatrixKey{platform: platform}
				if needsGroup {
					key.groupID = groupID
				}
				if groupBy == monitor.ChannelMonitorV2GroupByPlatformModel || groupBy == monitor.ChannelMonitorV2GroupByPlatformGroupModel {
					key.model = model
				}
				if accs[key] == nil {
					accs[key] = &channelMonitorV2MatrixAccumulator{groupName: info.name, total: newMetricAccumulator(), buckets: make(map[string]*metricAccumulator)}
				}
			}
		}
	}
	return accs
}

// configuredChannelMonitorV2Models resolves configured display models against
// selected model filters. An unrestricted configuration retains the explicit
// selection, while an enabled platform with a model inventory limits available
// display rows to that inventory and its other-model bucket.
func configuredChannelMonitorV2Models(cfg monitor.ChannelMonitorV2Config, platform string, filter monitor.ChannelMonitorV2Filter) []string {
	models := []string{}
	for _, p := range cfg.Platforms {
		if p.Platform != platform {
			continue
		}
		models = append(models, p.Models...)
		break
	}
	if len(filter.Models) > 0 {
		if len(models) == 0 {
			models = append(models, filter.Models...)
		} else {
			models = intersectStrings(models, filter.Models)
		}
	}
	return models
}

// channelMonitorV2CatalogFilter clears selected display filters while retaining
// configuration and viewer permission scope.
func channelMonitorV2CatalogFilter(filter monitor.ChannelMonitorV2Filter) monitor.ChannelMonitorV2Filter {
	catalog := filter
	catalog.Platforms = nil
	catalog.GroupIDs = nil
	catalog.Models = nil
	return catalog
}

// configuredChannelMonitorV2GroupIDs returns the group identifiers resolved from
// configuration and viewer scope.
func configuredChannelMonitorV2GroupIDs(filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config) []int64 {
	groups, empty := channelMonitorV2ScopedGroupIDs(filter, cfg)
	if empty {
		return []int64{}
	}
	return groups
}

// channelMonitorV2ScopedGroupIDs intersects requested, configured, and server-
// authorized group identifiers. The returned restriction flag distinguishes
// unrestricted administrator scope from a restricted viewer with no permitted
// identifiers; callers use that flag to emit a false SQL predicate for an empty
// allowed set.
func channelMonitorV2ScopedGroupIDs(filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config) ([]int64, bool) {
	groups := append([]int64(nil), cfg.GroupIDs...)
	if filter.RestrictGroups {
		if len(groups) > 0 {
			groups = intersectInt64(groups, filter.AllowedGroupIDs)
		} else {
			groups = append([]int64(nil), filter.AllowedGroupIDs...)
		}
		if len(groups) == 0 {
			return nil, true
		}
	}
	if len(filter.GroupIDs) > 0 {
		if len(groups) > 0 {
			groups = intersectInt64(groups, filter.GroupIDs)
		} else {
			groups = append([]int64(nil), filter.GroupIDs...)
		}
		if len(groups) == 0 {
			return nil, true
		}
	}
	return groups, false
}

// channelMonitorV2RestrictedGroupScopeEmpty reports whether intersected group
// permissions forbid every stored observation.
func channelMonitorV2RestrictedGroupScopeEmpty(filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config) bool {
	if !filter.RestrictGroups {
		return false
	}
	_, empty := channelMonitorV2ScopedGroupIDs(filter, cfg)
	return empty
}

// listActiveGroupIDs loads current active group identifiers used to seed
// administrator matrix rows.
func (r *channelMonitorV2Repository) listActiveGroupIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM groups WHERE deleted_at IS NULL AND status = 'active' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// loadChannelMonitorV2GroupInfo loads names for selected active groups before
// matrix seeding. Group platform hints remain empty because local groups can
// contain multiple provider families; request platform snapshots determine
// historical observation ownership.
func (r *channelMonitorV2Repository) loadChannelMonitorV2GroupInfo(ctx context.Context, groupIDs []int64) (map[int64]channelMonitorV2GroupInfo, error) {
	out := map[int64]channelMonitorV2GroupInfo{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, COALESCE(name, ''), '' FROM groups WHERE id = ANY($1) AND deleted_at IS NULL AND status = 'active'`, pq.Array(groupIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var name, platform string
		if err := rows.Scan(&id, &name, &platform); err != nil {
			return nil, err
		}
		out[id] = channelMonitorV2GroupInfo{name: name, platform: platform}
	}
	return out, rows.Err()
}

// GetErrors loads categorized error counts for the configured taxonomy and
// common coverage. Ignored categories remain visible with an ignored flag even
// though health excludes their error numerator. Rates use the scoped request
// denominator, and administrator mode additionally loads bounded diagnostic
// samples. Ordinary queries omit that original-error scan entirely, so private
// response samples are not fetched merely to be cleared by the HTTP adapter
// later.
func (r *channelMonitorV2Repository) GetErrors(ctx context.Context, filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config, includeAdmin bool) (*monitor.ChannelMonitorV2List[monitor.ChannelMonitorV2ErrorRow], error) {
	coverage, err := r.loadCoverage(ctx, filter)
	if err != nil {
		return nil, err
	}
	filter = channelMonitorV2CommonCoverageFilter(filter, *coverage)
	where, args, _ := channelMonitorV2WhereWithRollup(filter, cfg, "e")
	rows, err := r.db.QueryContext(ctx, `SELECT e.platform,e.model,e.error_category,SUM(e.error_requests) FROM `+channelMonitorV2ErrorMetricsTable(filter)+` e `+where+` AND e.taxonomy_version = `+fmt.Sprint(monitor.ChannelMonitorV2TaxonomyVersion)+` GROUP BY e.platform,e.model,e.error_category`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	counts := map[string]int64{}
	var total int64
	ignoredSet := monitor.ChannelMonitorV2IgnoredCategorySet(cfg)
	for rows.Next() {
		var platform, model, category string
		var count int64
		if err := rows.Scan(&platform, &model, &category, &count); err != nil {
			return nil, err
		}
		if !channelMonitorV2ModelSelected(filter, cfg, platform, model) {
			continue
		}
		if category == "" {
			category = "other"
		}
		counts[category] += count
		total += count
	}
	items := make([]monitor.ChannelMonitorV2ErrorRow, 0, len(counts))
	// Upstream message samples are admin-only: skip the raw ops_error_logs scan
	// for user-facing callers (privacy + avoids multi-million-row online scans).
	var details map[string][]monitor.ChannelMonitorV2ErrorDetail
	if includeAdmin {
		loaded, detailErr := r.loadErrorDetails(ctx, filter, cfg)
		if detailErr != nil {
			return nil, detailErr
		}
		details = loaded
	}
	for category, count := range counts {
		rate := 0.0
		if total > 0 {
			rate = float64(count) / float64(total)
		}
		_, ignored := ignoredSet[category]
		row := monitor.ChannelMonitorV2ErrorRow{Category: category, Count: count, Rate: rate, Ignored: ignored}
		if includeAdmin {
			row.Details = details[category]
		}
		items = append(items, row)
	}
	// Stable: counted categories first (by count), then ignored (grey in UI).
	sort.Slice(items, func(i, j int) bool {
		if items[i].Ignored != items[j].Ignored {
			return !items[i].Ignored && items[j].Ignored
		}
		return items[i].Count > items[j].Count
	})
	return &monitor.ChannelMonitorV2List[monitor.ChannelMonitorV2ErrorRow]{Coverage: *coverage, Items: items}, rows.Err()
}

// loadErrorDetails reads bounded original error samples for administrator
// diagnostic display. Requested and configured scopes remain intersected with
// viewer permissions before SQL execution. Samples are grouped by platform and
// category after applying the same model selection rules as the metric queries.
// Returned messages pass credential-marker redaction and length bounds, while
// counts describe the matched original rows rather than a separate health-
// scoring denominator.
func (r *channelMonitorV2Repository) loadErrorDetails(ctx context.Context, filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config) (map[string][]monitor.ChannelMonitorV2ErrorDetail, error) {
	conditions := []string{
		"current_error.created_at >= $1",
		"current_error.created_at < $2",
		"NOT current_error.is_count_tokens",
		"(COALESCE(current_error.status_code, 0) >= 400 OR current_error.error_type = 'cyber_policy')",
		`(NULLIF(current_error.request_id, '') IS NULL OR NOT EXISTS (
				SELECT 1 FROM ops_error_logs newer
				WHERE newer.request_id = current_error.request_id
				  AND NOT newer.is_count_tokens
				  AND (COALESCE(newer.status_code, 0) >= 400 OR newer.error_type = 'cyber_policy')
				  AND (newer.created_at, newer.id) > (current_error.created_at, current_error.id)
		))`,
	}
	args := []any{filter.Start, filter.End}
	platforms := channelMonitorV2EnabledPlatforms(cfg)
	if len(filter.Platforms) > 0 {
		platforms = intersectStrings(platforms, filter.Platforms)
	}
	if len(platforms) > 0 {
		args = append(args, pq.Array(platforms))
		conditions = append(conditions, fmt.Sprintf("lower(COALESCE(NULLIF(TRIM(current_error.platform), ''), 'unknown')) = ANY($%d)", len(args)))
	} else {
		conditions = append(conditions, "FALSE")
	}
	groups, groupScopeEmpty := channelMonitorV2ScopedGroupIDs(filter, cfg)
	if groupScopeEmpty {
		conditions = append(conditions, "FALSE")
	} else if len(groups) > 0 {
		args = append(args, pq.Array(groups))
		conditions = append(conditions, fmt.Sprintf("COALESCE(current_error.group_id, 0) = ANY($%d)", len(args)))
	}
	query := `SELECT
			lower(COALESCE(NULLIF(TRIM(current_error.platform), ''), 'unknown')) AS platform,
			COALESCE(current_error.group_id, 0) AS group_id,
			COALESCE(NULLIF(TRIM(current_error.requested_model), ''), NULLIF(TRIM(current_error.model), ''), 'unknown') AS model,
			COALESCE(current_error.error_type, '') AS error_type,
			COALESCE(current_error.error_owner, '') AS error_owner,
			COALESCE(current_error.error_source, '') AS error_source,
			COALESCE(current_error.status_code, 0) AS status_code,
			COALESCE(current_error.upstream_status_code, 0) AS upstream_status_code,
			LEFT(COALESCE(NULLIF(current_error.upstream_error_message, ''), NULLIF(current_error.error_message, ''), current_error.error_type, ''), 600) AS message,
			COUNT(*) AS count
		FROM ops_error_logs current_error
		WHERE ` + strings.Join(conditions, " AND ") + `
		GROUP BY 1,2,3,4,5,6,7,8,9
		ORDER BY count DESC
		LIMIT 400`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]monitor.ChannelMonitorV2ErrorDetail{}
	for rows.Next() {
		var platform, model, errorType, owner, source, message string
		var groupID int64
		var statusCode, upstreamStatusCode int
		var count int64
		if err := rows.Scan(&platform, &groupID, &model, &errorType, &owner, &source, &statusCode, &upstreamStatusCode, &message, &count); err != nil {
			return nil, err
		}
		if !channelMonitorV2ModelSelected(filter, cfg, platform, model) {
			continue
		}
		category := monitor.ClassifyChannelMonitorV2Error(monitor.ChannelMonitorV2ErrorInput{
			ErrorType:          errorType,
			ErrorOwner:         owner,
			ErrorSource:        source,
			StatusCode:         statusCode,
			UpstreamStatusCode: upstreamStatusCode,
			Message:            message,
		})
		if len(out[category]) >= 5 {
			continue
		}
		out[category] = append(out[category], monitor.ChannelMonitorV2ErrorDetail{
			Platform:           platform,
			Model:              channelMonitorV2DisplayModel(cfg, platform, model),
			ErrorType:          errorType,
			StatusCode:         statusCode,
			UpstreamStatusCode: upstreamStatusCode,
			Message:            sanitizeChannelMonitorV2ErrorDetail(message),
			Count:              count,
		})
	}
	return out, rows.Err()
}

// sanitizeChannelMonitorV2ErrorDetail collapses whitespace, masks recognized
// credential markers, and bounds diagnostic text. The result is an administrator
// sample rather than original response storage; callers keep raw error records
// in their existing authorized diagnostic path.
func sanitizeChannelMonitorV2ErrorDetail(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	replacements := []string{"Bearer ", "bearer ", "sk-", "sk-proj-"}
	for _, marker := range replacements {
		if idx := strings.Index(message, marker); idx >= 0 {
			end := idx + len(marker)
			for end < len(message) && message[end] != ' ' && message[end] != ',' && message[end] != '"' {
				end++
			}
			message = message[:idx] + marker + "****" + message[end:]
		}
	}
	if len(message) > 240 {
		message = message[:240] + "…"
	}
	return message
}

// GetUsers combines per-user scoped request facts with their latency histograms
// and ignored-error counts. User identity comes from current account labels,
// while platform and model ownership come from stored observations. Per-user
// metrics receive the same rate correction as summary queries before sorting.
// The service subsequently selects visible ranks, retains the authenticated
// viewer when needed, and anonymizes other identities for ordinary responses
// without changing their measured rates.
func (r *channelMonitorV2Repository) GetUsers(ctx context.Context, filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config, admin bool) (*monitor.ChannelMonitorV2List[monitor.ChannelMonitorV2UserRow], error) {
	coverage, err := r.loadCoverage(ctx, filter)
	if err != nil {
		return nil, err
	}
	effectiveFilter := channelMonitorV2CommonCoverageFilter(filter, *coverage)
	filter = effectiveFilter
	where, args, _ := channelMonitorV2WhereWithRollup(filter, cfg, "m")
	query := `SELECT m.user_id,COALESCE(u.email,''),COALESCE(u.username,''),m.platform,m.model,
	SUM(m.success_requests),SUM(m.error_requests),SUM(m.input_tokens),SUM(m.output_tokens),SUM(m.cache_creation_tokens),SUM(m.cache_read_tokens),SUM(m.ttft_sum_ms),SUM(m.ttft_count),SUM(m.duration_sum_ms),SUM(m.duration_count)
	FROM ` + channelMonitorV2UserMetricsTable(filter) + ` m LEFT JOIN users u ON u.id=m.user_id ` + where + ` GROUP BY m.user_id,u.email,u.username,m.platform,m.model`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	// userMeta retains current account labels for service-controlled viewer redaction.
	type userMeta struct {
		email    string // current account email label
		username string // current account display name
	}
	meta := map[int64]userMeta{}
	accs := map[int64]*metricAccumulator{}
	for rows.Next() {
		var uid int64
		var email, username string
		var f channelMonitorV2Fact
		if err := rows.Scan(&uid, &email, &username, &f.Platform, &f.Model, &f.Success, &f.Errors, &f.Input, &f.Output, &f.CacheCreation, &f.CacheRead, &f.TTFTSum, &f.TTFTCount, &f.DurationSum, &f.DurationCount); err != nil {
			return nil, err
		}
		if !channelMonitorV2ModelSelected(filter, cfg, f.Platform, f.Model) {
			continue
		}
		if accs[uid] == nil {
			accs[uid] = newMetricAccumulator()
		}
		accs[uid].addFact(f)
		meta[uid] = userMeta{email, username}
	}
	histograms, err := r.loadHistograms(ctx, filter, cfg, -1, false)
	if err != nil {
		return nil, err
	}
	for _, histogram := range histograms {
		if !channelMonitorV2ModelSelected(filter, cfg, histogram.Platform, histogram.Model) {
			continue
		}
		if acc := accs[histogram.UserID]; acc != nil {
			acc.addHistogram(histogram)
		}
	}
	// Error category facts are not per-user. Approximate ignored-error impact by
	// applying the window-level ignored/error ratio to each user's error count so
	// user-rank rates stay consistent with overview scoring.
	_, ignoredTotal, err := r.loadIgnoredErrorCounts(ctx, effectiveFilter, cfg)
	if err != nil {
		return nil, fmt.Errorf("load ignored error counts for users: %w", err)
	}
	var windowErrors int64
	for _, acc := range accs {
		windowErrors += acc.errors
	}
	ignoreRatio := 0.0
	if windowErrors > 0 && ignoredTotal > 0 {
		ignoreRatio = float64(ignoredTotal) / float64(windowErrors)
		if ignoreRatio > 1 {
			ignoreRatio = 1
		}
	}

	items := make([]monitor.ChannelMonitorV2UserRow, 0, len(accs))
	minutes := channelMonitorV2CoveredMinutes(filter, *coverage)
	for uid, acc := range accs {
		id := uid
		m := meta[uid]
		label := m.username
		if label == "" {
			label = m.email
		}
		metrics := acc.metric(minutes, false)
		if ignoreRatio > 0 && metrics.ErrorRequests > 0 {
			approxIgnored := int64(float64(metrics.ErrorRequests)*ignoreRatio + 0.5)
			applyIgnoredErrors(&metrics, approxIgnored)
		}
		// Recompute health after rate adjustment.
		// (metric() does not attach health; callers that need it recompute.)
		items = append(items, monitor.ChannelMonitorV2UserRow{UserID: &id, Email: m.email, Username: m.username, DisplayLabel: label, CanDrilldown: admin, Metrics: metrics})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Metrics.RequestCount > items[j].Metrics.RequestCount })
	return &monitor.ChannelMonitorV2List[monitor.ChannelMonitorV2UserRow]{Coverage: *coverage, Items: items}, rows.Err()
}

// loadFacts loads minute or fixed-rollup facts within common committed coverage.
// Optional byBucket groups into display intervals, and all dynamic values remain
// SQL parameters while selected table names come from the fixed range catalog.
func (r *channelMonitorV2Repository) loadFacts(ctx context.Context, filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config, byBucket bool) ([]channelMonitorV2Fact, error) {
	where, args, bucketSeconds := channelMonitorV2WhereWithRollup(filter, cfg, "m")
	bucketExpr := "MIN(m.bucket_start)"
	group := "m.platform,m.group_id,g.name,m.model"
	if byBucket {
		if bucketSeconds > 0 {
			bucketExpr = "m.bucket_start"
			group = bucketExpr + "," + group
		} else {
			args = append([]any{fmt.Sprintf("%d seconds", int(filter.Bucket.Seconds()))}, args...)
			where = shiftSQLPlaceholders(where, 1)
			bucketExpr = channelMonitorV2DateBinExpr("m.bucket_start")
			group = bucketExpr + "," + group
		}
	}
	query := `SELECT ` + bucketExpr + `,m.platform,m.group_id,COALESCE(g.name,''),m.model,SUM(m.success_requests),SUM(m.error_requests),SUM(m.upstream_affected_requests),SUM(m.upstream_attempt_count),SUM(m.input_tokens),SUM(m.output_tokens),SUM(m.cache_creation_tokens),SUM(m.cache_read_tokens),SUM(m.ttft_sum_ms),SUM(m.ttft_count),SUM(m.duration_sum_ms),SUM(m.duration_count) FROM ` + channelMonitorV2MetricsTable(filter) + ` m LEFT JOIN groups g ON g.id=NULLIF(m.group_id,0) ` + where + ` GROUP BY ` + group
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	facts := []channelMonitorV2Fact{}
	for rows.Next() {
		var bucket time.Time
		var f channelMonitorV2Fact
		if err := rows.Scan(&bucket, &f.Platform, &f.GroupID, &f.GroupName, &f.Model, &f.Success, &f.Errors, &f.UpstreamAffected, &f.UpstreamAttempts, &f.Input, &f.Output, &f.CacheCreation, &f.CacheRead, &f.TTFTSum, &f.TTFTCount, &f.DurationSum, &f.DurationCount); err != nil {
			return nil, err
		}
		f.BucketStart = bucket.UTC().Format(time.RFC3339Nano)
		facts = append(facts, f)
	}
	return facts, rows.Err()
}

// loadHistograms loads latency histogram bounds for the selected user and
// grouping mode. User zero selects fleet histograms; an explicit positive user
// selects that user's observations under the same configured and authorized
// filters.
func (r *channelMonitorV2Repository) loadHistograms(ctx context.Context, filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config, userID int64, byBucket bool) ([]channelMonitorV2Histogram, error) {
	where, args, bucketSeconds := channelMonitorV2WhereWithRollup(filter, cfg, "h")
	if userID < 0 {
		where += " AND h.user_id > 0"
	} else {
		args = append(args, userID)
		where += fmt.Sprintf(" AND h.user_id = $%d", len(args))
	}
	bucketExpr := "MIN(h.bucket_start)"
	group := "h.platform,h.group_id,h.model,h.user_id,h.metric,h.upper_bound_ms"
	if byBucket {
		if bucketSeconds > 0 {
			bucketExpr = "h.bucket_start"
			group = bucketExpr + "," + group
		} else {
			oldArgs := args
			args = []any{fmt.Sprintf("%d seconds", int(filter.Bucket.Seconds()))}
			args = append(args, oldArgs...)
			where = shiftSQLPlaceholders(where, 1)
			bucketExpr = channelMonitorV2DateBinExpr("h.bucket_start")
			group = bucketExpr + "," + group
		}
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+bucketExpr+`,h.platform,h.group_id,h.model,h.user_id,h.metric,h.upper_bound_ms,SUM(h.sample_count) FROM `+channelMonitorV2HistogramTable(filter)+` h `+where+` GROUP BY `+group, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []channelMonitorV2Histogram{}
	for rows.Next() {
		var bucket time.Time
		var h channelMonitorV2Histogram
		if err := rows.Scan(&bucket, &h.Platform, &h.GroupID, &h.Model, &h.UserID, &h.Metric, &h.UpperBound, &h.Count); err != nil {
			return nil, err
		}
		h.BucketStart = bucket.UTC().Format(time.RFC3339Nano)
		out = append(out, h)
	}
	return out, rows.Err()
}

// GetAggregationWatermark loads committed coverage and historical cursors in
// UTC. A missing row returns an empty watermark with HasData false, allowing the
// worker to bootstrap before the first successful aggregation transaction.
func (r *channelMonitorV2Repository) GetAggregationWatermark(ctx context.Context) (*monitor.ChannelMonitorV2AggregationWatermark, error) {
	var usageStart, errorStart, dataThrough, computed, backfill sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT usage_coverage_start, error_coverage_start, data_through, last_successful_at, backfill_cursor
		FROM channel_monitor_v2_watermarks WHERE id = 1`).Scan(&usageStart, &errorStart, &dataThrough, &computed, &backfill)
	if err == sql.ErrNoRows {
		return &monitor.ChannelMonitorV2AggregationWatermark{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := &monitor.ChannelMonitorV2AggregationWatermark{HasData: dataThrough.Valid}
	if usageStart.Valid {
		out.UsageCoverageStart = usageStart.Time.UTC()
	}
	if errorStart.Valid {
		out.ErrorCoverageStart = errorStart.Time.UTC()
	}
	if dataThrough.Valid {
		out.DataThrough = dataThrough.Time.UTC()
	}
	if computed.Valid {
		out.LastSuccessfulAt = computed.Time.UTC()
	}
	if backfill.Valid {
		out.BackfillCursor = backfill.Time.UTC()
	}
	return out, nil
}

// loadCoverage combines requested display bounds with committed usage and error
// coverage. The common covered start prevents one data source from implying
// complete history while the other remains unbuilt. DataThrough limits
// observation reads to completed aggregation, while RequestedEnd retains the
// full display window including its current partial bucket. Lag compares the
// durable completed minute with current time, and bootstrap progress follows the
// historical cursor toward the product window.
func (r *channelMonitorV2Repository) loadCoverage(ctx context.Context, filter monitor.ChannelMonitorV2Filter) (*monitor.ChannelMonitorV2Coverage, error) {
	wm, err := r.GetAggregationWatermark(ctx)
	if err != nil {
		return nil, err
	}
	if wm == nil {
		wm = &monitor.ChannelMonitorV2AggregationWatermark{}
	}
	bootstrap := monitor.ChannelMonitorV2BootstrapProgress(time.Now().UTC(), wm.BackfillCursor, wm.HasData)
	if !wm.HasData || wm.DataThrough.IsZero() || wm.LastSuccessfulAt.IsZero() {
		return &monitor.ChannelMonitorV2Coverage{
			RequestedStart:        filter.Start,
			RequestedEnd:          filter.End,
			CoverageStart:         filter.End,
			DataThrough:           filter.Start,
			ComputedAt:            time.Time{},
			AggregationLagSeconds: 0,
			CoverageComplete:      false,
			BucketSeconds:         int(filter.Bucket.Seconds()),
			Bootstrap:             bootstrap,
		}, nil
	}
	coverageStart := filter.Start
	if !wm.UsageCoverageStart.IsZero() && wm.UsageCoverageStart.After(coverageStart) {
		coverageStart = wm.UsageCoverageStart
	}
	if !wm.ErrorCoverageStart.IsZero() && wm.ErrorCoverageStart.After(coverageStart) {
		coverageStart = wm.ErrorCoverageStart
	}
	through := filter.End
	if !wm.DataThrough.IsZero() && wm.DataThrough.Before(through) {
		through = wm.DataThrough
	}
	computedAt := wm.LastSuccessfulAt
	lag := int64(0)
	if !through.IsZero() {
		lag = max(int64(time.Since(through).Seconds()), 0)
	}
	// Complete = history depth for this range is filled (backfill reached
	// filter.Start). Do not require data_through >= filter.End: ParseFilter
	// aligns End to the next whole bucket (often in the future), so a healthy
	// minute-level lag would otherwise always show "partial historical coverage".
	return &monitor.ChannelMonitorV2Coverage{
		RequestedStart:        filter.Start,
		RequestedEnd:          filter.End,
		CoverageStart:         coverageStart,
		DataThrough:           through,
		ComputedAt:            computedAt,
		AggregationLagSeconds: lag,
		CoverageComplete:      channelMonitorV2HistoryCoverageComplete(coverageStart, filter.Start),
		BucketSeconds:         int(filter.Bucket.Seconds()),
		Bootstrap:             bootstrap,
	}, nil
}

// channelMonitorV2HistoryCoverageComplete reports whether committed historical
// coverage reaches the requested display start.
func channelMonitorV2HistoryCoverageComplete(coverageStart, filterStart time.Time) bool {
	if coverageStart.IsZero() || filterStart.IsZero() {
		return false
	}
	return !coverageStart.After(filterStart)
}

// channelMonitorV2FixedBucketSeconds selects the persisted fixed-rollup width
// for a supported product window.
func channelMonitorV2FixedBucketSeconds(filter monitor.ChannelMonitorV2Filter) int {
	seconds := int(filter.Bucket.Seconds())
	switch seconds {
	case 300, 3600, 43200, 86400:
		return seconds
	default:
		return 0
	}
}

// channelMonitorV2MetricsTable selects the trusted request-fact table for this
// product window.
func channelMonitorV2MetricsTable(filter monitor.ChannelMonitorV2Filter) string {
	if channelMonitorV2FixedBucketSeconds(filter) > 0 {
		return "channel_monitor_v2_metrics_rollup"
	}
	return "channel_monitor_v2_metrics_1m"
}

// channelMonitorV2UserMetricsTable selects the trusted user-fact table for this
// product window.
func channelMonitorV2UserMetricsTable(filter monitor.ChannelMonitorV2Filter) string {
	if channelMonitorV2FixedBucketSeconds(filter) > 0 {
		return "channel_monitor_v2_user_metrics_rollup"
	}
	return "channel_monitor_v2_user_metrics_1m"
}

// channelMonitorV2ErrorMetricsTable selects the trusted categorized-error table
// for this product window.
func channelMonitorV2ErrorMetricsTable(filter monitor.ChannelMonitorV2Filter) string {
	if channelMonitorV2FixedBucketSeconds(filter) > 0 {
		return "channel_monitor_v2_error_metrics_rollup"
	}
	return "channel_monitor_v2_error_metrics_1m"
}

// channelMonitorV2HistogramTable selects the trusted latency histogram table for
// this product window.
func channelMonitorV2HistogramTable(filter monitor.ChannelMonitorV2Filter) string {
	if channelMonitorV2FixedBucketSeconds(filter) > 0 {
		return "channel_monitor_v2_latency_histograms_rollup"
	}
	return "channel_monitor_v2_latency_histograms_1m"
}

// channelMonitorV2WhereWithRollup adds fixed-bucket selection to the scoped SQL
// predicate and bound values. Internal aliases and trusted table selectors
// determine structure; scope values and bucket width remain positional SQL
// arguments.
func channelMonitorV2WhereWithRollup(filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config, alias string) (string, []any, int) {
	where, args := channelMonitorV2Where(filter, cfg, alias)
	bucketSeconds := channelMonitorV2FixedBucketSeconds(filter)
	if bucketSeconds > 0 {
		args = append(args, bucketSeconds)
		where += fmt.Sprintf(" AND %s.bucket_seconds = $%d", alias, len(args))
	}
	return where, args, bucketSeconds
}

// channelMonitorV2Where builds parameterized time, platform, and group
// predicates for an internal alias. Enabled configuration platforms intersect
// selected platforms, and group restrictions distinguish empty viewer
// permissions from unrestricted administrator scope before any stored facts can
// match.
func channelMonitorV2Where(filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config, alias string) (string, []any) {
	conditions := []string{alias + ".bucket_start >= $1", alias + ".bucket_start < $2"}
	args := []any{filter.Start, filter.End}
	platforms := channelMonitorV2EnabledPlatforms(cfg)
	if len(filter.Platforms) > 0 {
		platforms = intersectStrings(platforms, filter.Platforms)
	}
	if len(platforms) > 0 {
		args = append(args, pq.Array(platforms))
		conditions = append(conditions, fmt.Sprintf("%s.platform = ANY($%d)", alias, len(args)))
	} else {
		conditions = append(conditions, "FALSE")
	}
	groups, groupScopeEmpty := channelMonitorV2ScopedGroupIDs(filter, cfg)
	if groupScopeEmpty {
		conditions = append(conditions, "FALSE")
	} else if len(groups) > 0 {
		args = append(args, pq.Array(groups))
		conditions = append(conditions, fmt.Sprintf("%s.group_id = ANY($%d)", alias, len(args)))
	}
	return "WHERE " + strings.Join(conditions, " AND "), args
}

// channelMonitorV2EnabledPlatforms returns sorted enabled platform names from
// the normalized monitor configuration.
func channelMonitorV2EnabledPlatforms(cfg monitor.ChannelMonitorV2Config) []string {
	out := []string{}
	for _, p := range cfg.Platforms {
		if p.Enabled {
			out = append(out, p.Platform)
		}
	}
	return out
}

// channelMonitorV2DisplayModel resolves an observed name into the configured
// display model bucket. Empty model inventories preserve actual names;
// configured inventories retain selected names and collapse remaining
// observations into the other-model bucket.
func channelMonitorV2DisplayModel(cfg monitor.ChannelMonitorV2Config, platform, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return monitor.ChannelMonitorV2OtherModel
	}
	for _, p := range cfg.Platforms {
		if p.Platform != platform {
			continue
		}
		// Empty allow-list: surface every real model instead of dumping into __other__.
		// Operators opt into the named + __other__ split only by listing models.
		if len(p.Models) == 0 {
			return model
		}
		if slices.Contains(p.Models, model) {
			return model
		}
		return monitor.ChannelMonitorV2OtherModel
	}
	// Platform not configured: still show the real model so traffic is visible.
	return model
}

// channelMonitorV2ModelSelected reports whether the requested model filters
// include an observed display bucket.
func channelMonitorV2ModelSelected(filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config, platform, model string) bool {
	if len(filter.Models) == 0 {
		return true
	}
	display := channelMonitorV2DisplayModel(cfg, platform, model)
	return slices.Contains(filter.Models, display)
}

// channelMonitorV2ModelLabel returns the display label for actual or collapsed
// model names.
func channelMonitorV2ModelLabel(model string) string {
	if model == monitor.ChannelMonitorV2OtherModel {
		return "Other models"
	}
	return model
}

// channelMonitorV2CoveredMinutes calculates the covered duration used by RPM and
// TPM metrics. The span intersects requested bounds with committed coverage, and
// a minimum positive duration prevents division by zero for empty windows.
func channelMonitorV2CoveredMinutes(filter monitor.ChannelMonitorV2Filter, coverage monitor.ChannelMonitorV2Coverage) float64 {
	start := filter.Start
	if coverage.CoverageStart.After(start) {
		start = coverage.CoverageStart
	}
	end := filter.End
	if !coverage.DataThrough.IsZero() && coverage.DataThrough.Before(end) {
		end = coverage.DataThrough
	}
	if !start.Before(end) {
		return 1
	}
	return end.Sub(start).Minutes()
}

// channelMonitorV2CommonCoverageFilter limits source reads to the common
// committed coverage interval. Display bounds remain on the original filter for
// bucket seeding, allowing missing history to remain visibly unknown rather than
// disappearing from the timeline.
func channelMonitorV2CommonCoverageFilter(filter monitor.ChannelMonitorV2Filter, coverage monitor.ChannelMonitorV2Coverage) monitor.ChannelMonitorV2Filter {
	if coverage.CoverageStart.After(filter.Start) {
		filter.Start = coverage.CoverageStart
	}
	if !coverage.DataThrough.IsZero() && coverage.DataThrough.Before(filter.End) {
		filter.End = coverage.DataThrough
	}
	// Fixed rows identify their bucket start even when bootstrap covers only its
	// trailing portion. Include that row while reporting the actual covered start
	// separately, so a partial historical bucket does not erase observed traffic.
	if channelMonitorV2FixedBucketSeconds(filter) > 0 && filter.Start.Before(filter.End) {
		filter.Start = filter.Start.Truncate(filter.Bucket)
	}
	return filter
}

// intersectStrings returns first-slice values present in the second scope
// collection.
func intersectStrings(a, b []string) []string {
	set := map[string]struct{}{}
	for _, v := range b {
		set[v] = struct{}{}
	}
	out := []string{}
	for _, v := range a {
		if _, ok := set[v]; ok {
			out = append(out, v)
		}
	}
	return out
}

// containsString reports whether the supplied collection contains the requested
// exact string.
func containsString(values []string, target string) bool {
	return slices.Contains(values, target)
}

// intersectInt64 returns first-slice identifiers present in the second scope
// collection.
func intersectInt64(a, b []int64) []int64 {
	set := map[int64]struct{}{}
	for _, v := range b {
		set[v] = struct{}{}
	}
	out := []int64{}
	for _, v := range a {
		if _, ok := set[v]; ok {
			out = append(out, v)
		}
	}
	return out
}

// shiftSQLPlaceholders increments trusted positional SQL placeholders when
// adding a leading parameter. Callers supply code-owned SQL, and descending
// replacement prevents a shifted placeholder from being shifted again within the
// same operation.
func shiftSQLPlaceholders(query string, offset int) string {
	for i := 64; i >= 1; i-- {
		query = strings.ReplaceAll(query, fmt.Sprintf("$%d", i), fmt.Sprintf("$%d", i+offset))
	}
	return query
}

// newMetricAccumulator allocates independent TTFT and duration histogram maps
// for one observation group. The returned accumulator is mutable and belongs to
// its query; callers add scoped facts before constructing output metrics.
func newMetricAccumulator() *metricAccumulator {
	return &metricAccumulator{hist: map[string]map[int64]int64{"ttft": {}, "duration": {}}}
}

// addFact adds one scoped fact row to the accumulator's counts and sums.
func (a *metricAccumulator) addFact(f channelMonitorV2Fact) {
	a.success += f.Success
	a.errors += f.Errors
	a.upstreamAffected += f.UpstreamAffected
	a.upstreamAttempts += f.UpstreamAttempts
	a.input += f.Input
	a.output += f.Output
	a.cacheCreation += f.CacheCreation
	a.cacheRead += f.CacheRead
	a.ttftSum += f.TTFTSum
	a.ttftCount += f.TTFTCount
	a.durationSum += f.DurationSum
	a.durationCount += f.DurationCount
}

// addHistogram adds one latency histogram count under its metric and upper
// bound.
func (a *metricAccumulator) addHistogram(h channelMonitorV2Histogram) {
	if a.hist[h.Metric] == nil {
		a.hist[h.Metric] = map[int64]int64{}
	}
	a.hist[h.Metric][h.UpperBound] += h.Count
}

// metric calculates request totals, token totals, rates, and latencies from
// accumulated facts. RPM and TPM divide by covered minutes, using one minute
// when the supplied denominator is nonpositive. Cache rate divides cache-read
// tokens by input plus cache-creation and cache-read tokens. True success rate
// retains every failed request in its denominator; ignored-error correction is
// applied separately, and administrator mode exposes optional upstream
// diagnostic counts.
func (a *metricAccumulator) metric(minutes float64, admin bool) monitor.ChannelMonitorV2Metric {
	requests := a.success + a.errors
	tokens := a.input + a.output + a.cacheCreation + a.cacheRead
	denom := a.input + a.cacheCreation + a.cacheRead
	if minutes <= 0 {
		minutes = 1
	}
	m := monitor.ChannelMonitorV2Metric{SuccessRequests: a.success, ErrorRequests: a.errors, RequestCount: requests, InputTokens: a.input, OutputTokens: a.output, CacheCreationTokens: a.cacheCreation, CacheReadTokens: a.cacheRead, TokenCount: tokens, RPM: float64(requests) / minutes, TPM: float64(tokens) / minutes, CacheRateNumerator: a.cacheRead, CacheRateDenominator: denom, TTFT: latencyMetric(a.ttftSum, a.ttftCount, a.hist["ttft"]), Duration: latencyMetric(a.durationSum, a.durationCount, a.hist["duration"])}
	if requests > 0 {
		m.ErrorRate = float64(a.errors) / float64(requests)
		m.SuccessRate = float64(a.success) / float64(requests)
	}
	if denom > 0 {
		m.CacheRate = float64(a.cacheRead) / float64(denom)
	}
	if admin {
		affected, attempts := a.upstreamAffected, a.upstreamAttempts
		m.UpstreamAffectedRequests = &affected
		m.UpstreamAttemptCount = &attempts
	}
	return m
}

// latencyMetric constructs latency averages and percentile estimates from
// observed samples. An empty count leaves the average absent, and empty
// histograms retain absent percentiles rather than introducing zero-millisecond
// observations into comparisons.
func latencyMetric(sum, count int64, hist map[int64]int64) monitor.ChannelMonitorV2Latency {
	result := monitor.ChannelMonitorV2Latency{SampleCount: count}
	if count > 0 {
		avg := float64(sum) / float64(count)
		result.AvgMs = &avg
	}
	result.P50Ms = histPercentile(hist, 0.50)
	result.P90Ms = histPercentile(hist, 0.90)
	result.P95Ms = histPercentile(hist, 0.95)
	return result
}

// histPercentile finds the enclosing upper bound at the requested histogram
// rank. It sorts bounds, rounds the cumulative target upward, and returns nil
// when no samples exist; returned values are histogram estimates rather than
// exact sorted request latencies.
func histPercentile(hist map[int64]int64, p float64) *int64 {
	var total int64
	keys := make([]int64, 0, len(hist))
	for bound, count := range hist {
		keys = append(keys, bound)
		total += count
	}
	if total == 0 {
		return nil
	}
	slices.Sort(keys)
	target := int64(float64(total)*p + 0.999999)
	var cumulative int64
	for _, bound := range keys {
		cumulative += hist[bound]
		if cumulative >= target {
			v := bound
			return &v
		}
	}
	v := keys[len(keys)-1]
	return &v
}

// applyIgnoredErrors removes configured ignored failures from the scored error
// numerator. Absolute requests, failures, RPM, and the true success ratio retain
// those observations, so exclusion cannot turn a failed request into a success.
// Ignored counts are bounded by actual errors before division, and the output
// success ratio remains within zero and one when historical inputs require
// normalization. Callers apply this before passing metrics to the health scorer.
func applyIgnoredErrors(m *monitor.ChannelMonitorV2Metric, ignoredCount int64) {
	if m == nil || ignoredCount <= 0 || m.RequestCount <= 0 {
		return
	}
	if ignoredCount > m.ErrorRequests {
		ignoredCount = m.ErrorRequests
	}
	countedErrors := max(m.ErrorRequests-ignoredCount, 0)
	m.ErrorRate = float64(countedErrors) / float64(m.RequestCount)
	// Keep SuccessRate = SuccessRequests / RequestCount when absolute success is
	// known; fall back to residual only if SuccessRequests was never populated.
	if m.SuccessRequests > 0 || m.ErrorRequests >= m.RequestCount {
		m.SuccessRate = float64(m.SuccessRequests) / float64(m.RequestCount)
	}
	if m.SuccessRate < 0 {
		m.SuccessRate = 0
	}
	if m.SuccessRate > 1 {
		m.SuccessRate = 1
	}
}

// loadIgnoredErrorCounts loads ignored-category totals and display-bucket counts
// within the current scope. Categories use the configured taxonomy version, and
// bucket alignment matches the corresponding request-fact query so health
// corrections apply to the same intervals.
func (r *channelMonitorV2Repository) loadIgnoredErrorCounts(
	ctx context.Context,
	filter monitor.ChannelMonitorV2Filter,
	cfg monitor.ChannelMonitorV2Config,
) (byBucket map[string]int64, total int64, err error) {
	byBucket = map[string]int64{}
	if len(cfg.IgnoredErrorCategories) == 0 {
		return byBucket, 0, nil
	}
	where, args, bucketSeconds := channelMonitorV2WhereWithRollup(filter, cfg, "e")
	bucketExpr := "e.bucket_start"
	groupBy := "e.bucket_start, e.platform, e.model"
	if filter.Bucket > 0 && bucketSeconds == 0 {
		args = append([]any{fmt.Sprintf("%d seconds", int(filter.Bucket.Seconds()))}, args...)
		where = shiftSQLPlaceholders(where, 1)
		bucketExpr = channelMonitorV2DateBinExpr("e.bucket_start")
		groupBy = bucketExpr + ", e.platform, e.model"
	}
	args = append(args, pq.Array(cfg.IgnoredErrorCategories), monitor.ChannelMonitorV2TaxonomyVersion)
	catIdx := len(args) - 1
	taxIdx := len(args)
	query := fmt.Sprintf(
		`SELECT %s, e.platform, e.model, SUM(e.error_requests)
		 FROM `+channelMonitorV2ErrorMetricsTable(filter)+` e %s
		 AND e.error_category = ANY($%d) AND e.taxonomy_version = $%d
		 GROUP BY %s`,
		bucketExpr, where, catIdx, taxIdx, groupBy,
	)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return byBucket, 0, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var bucket time.Time
		var platform, model string
		var count int64
		if err := rows.Scan(&bucket, &platform, &model, &count); err != nil {
			return byBucket, 0, err
		}
		if !channelMonitorV2ModelSelected(filter, cfg, platform, model) {
			continue
		}
		key := bucket.UTC().Format(time.RFC3339Nano)
		byBucket[key] += count
		total += count
	}
	return byBucket, total, rows.Err()
}

// loadIgnoredErrorCountsByPlatformModel loads ignored-category counts keyed by
// platform and displayed model. Model filtering and collapse rules match the
// corresponding model fact query, so health correction cannot subtract errors
// belonging to another displayed row.
func (r *channelMonitorV2Repository) loadIgnoredErrorCountsByPlatformModel(
	ctx context.Context,
	filter monitor.ChannelMonitorV2Filter,
	cfg monitor.ChannelMonitorV2Config,
) (byPM map[string]int64, total int64, err error) {
	byPM = map[string]int64{}
	if len(cfg.IgnoredErrorCategories) == 0 {
		return byPM, 0, nil
	}
	where, args, _ := channelMonitorV2WhereWithRollup(filter, cfg, "e")
	args = append(args, pq.Array(cfg.IgnoredErrorCategories), monitor.ChannelMonitorV2TaxonomyVersion)
	catIdx := len(args) - 1
	taxIdx := len(args)
	query := fmt.Sprintf(
		`SELECT e.platform, e.model, SUM(e.error_requests)
		 FROM `+channelMonitorV2ErrorMetricsTable(filter)+` e %s
		 AND e.error_category = ANY($%d) AND e.taxonomy_version = $%d
		 GROUP BY e.platform, e.model`,
		where, catIdx, taxIdx,
	)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return byPM, 0, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var platform, model string
		var count int64
		if err := rows.Scan(&platform, &model, &count); err != nil {
			return byPM, 0, err
		}
		if !channelMonitorV2ModelSelected(filter, cfg, platform, model) {
			continue
		}
		display := channelMonitorV2DisplayModel(cfg, platform, model)
		byPM[platform+"\x00"+display] += count
		total += count
	}
	return byPM, total, rows.Err()
}

// loadIgnoredErrorCountsByMatrixKey loads ignored-category counts for each
// matrix dimension and display bucket. The dimension key and time alignment
// match fact and histogram queries; returned totals allow scoring both the
// complete row and individual history cells consistently.
func (r *channelMonitorV2Repository) loadIgnoredErrorCountsByMatrixKey(
	ctx context.Context,
	filter monitor.ChannelMonitorV2Filter,
	cfg monitor.ChannelMonitorV2Config,
	groupBy monitor.ChannelMonitorV2GroupBy,
) (byDimBucket map[channelMonitorV2MatrixKey]map[string]int64, byDim map[channelMonitorV2MatrixKey]int64, err error) {
	byDimBucket = map[channelMonitorV2MatrixKey]map[string]int64{}
	byDim = map[channelMonitorV2MatrixKey]int64{}
	if len(cfg.IgnoredErrorCategories) == 0 {
		return byDimBucket, byDim, nil
	}
	where, args, bucketSeconds := channelMonitorV2WhereWithRollup(filter, cfg, "e")
	bucketExpr := "e.bucket_start"
	groupSQL := "e.bucket_start, e.platform, e.group_id, e.model"
	if filter.Bucket > 0 && bucketSeconds == 0 {
		args = append([]any{fmt.Sprintf("%d seconds", int(filter.Bucket.Seconds()))}, args...)
		where = shiftSQLPlaceholders(where, 1)
		bucketExpr = channelMonitorV2DateBinExpr("e.bucket_start")
		groupSQL = bucketExpr + ", e.platform, e.group_id, e.model"
	}
	args = append(args, pq.Array(cfg.IgnoredErrorCategories), monitor.ChannelMonitorV2TaxonomyVersion)
	catIdx := len(args) - 1
	taxIdx := len(args)
	query := fmt.Sprintf(
		`SELECT %s, e.platform, e.group_id, e.model, SUM(e.error_requests)
		 FROM `+channelMonitorV2ErrorMetricsTable(filter)+` e %s
		 AND e.error_category = ANY($%d) AND e.taxonomy_version = $%d
		 GROUP BY %s`,
		bucketExpr, where, catIdx, taxIdx, groupSQL,
	)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return byDimBucket, byDim, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var bucket time.Time
		var platform, model string
		var groupID, count int64
		if err := rows.Scan(&bucket, &platform, &groupID, &model, &count); err != nil {
			return byDimBucket, byDim, err
		}
		if !channelMonitorV2ModelSelected(filter, cfg, platform, model) {
			continue
		}
		bucketKey := bucket.UTC().Format(time.RFC3339Nano)
		key := channelMonitorV2MatrixDimensionKey(groupBy, cfg, platform, groupID, model)
		byDim[key] += count
		if byDimBucket[key] == nil {
			byDimBucket[key] = map[string]int64{}
		}
		byDimBucket[key][bucketKey] += count
	}
	return byDimBucket, byDim, rows.Err()
}
