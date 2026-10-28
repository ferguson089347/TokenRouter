package monitor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// service.go queries passive channel health using Kedaya's V2 metrics and
// scoring rules. Configuration chooses visible platforms, models, groups, and
// ignored error categories. Successful usage and categorized request errors are
// aggregated separately, while request-count eligibility controls whether a
// health score can be published. Ordinary viewers receive scoped results with
// traffic volumes and administrator error details removed before serialization.
//
// The repository owns database access and bounded historical coverage. Query
// windows align to the same fixed buckets used by the background worker, and
// missing or insufficient observations retain unknown health. Model display
// grouping and error exclusions apply consistently to summaries, trends, and
// matrices so those views can be compared using the same configured scope.
// Adapted from kiss-kedaya/sub2api at 0ff60acabffe06a8de2d82f8b0ba657f5551512f.

// Monitor labels identify collapsed models, taxonomy revisions, and supported
// dimension combinations used consistently by SQL and HTTP response builders.
const (
	ChannelMonitorV2OtherModel      = "__other__"
	ChannelMonitorV2TaxonomyVersion = 1

	ChannelMonitorV2GroupByPlatform           ChannelMonitorV2GroupBy = "platform"
	ChannelMonitorV2GroupByPlatformGroup      ChannelMonitorV2GroupBy = "platform_group"
	ChannelMonitorV2GroupByPlatformModel      ChannelMonitorV2GroupBy = "platform_model"
	ChannelMonitorV2GroupByPlatformGroupModel ChannelMonitorV2GroupBy = "platform_group_model"

	// ChannelMonitorV2BootstrapProductWindow is the longest UI range that must be
	// filled on first upgrade before the bootstrap banner disappears (30d).
	ChannelMonitorV2BootstrapProductWindow = 30 * 24 * time.Hour
)

// Monitor errors distinguish disabled reads, invalid scope, and concurrent
// configuration edits so HTTP adapters can select their response status.
var (
	ErrChannelMonitorDisabled         = errors.New("channel monitoring is disabled")
	ErrChannelMonitorV2InvalidRange   = errors.New("invalid channel monitor v2 range")
	ErrChannelMonitorV2InvalidGroupBy = errors.New("invalid channel monitor v2 group_by")
	ErrChannelMonitorV2InvalidConfig  = errors.New("invalid channel monitor v2 config")
	ErrChannelMonitorV2ConfigConflict = errors.New("channel monitor v2 config was modified")

	// ChannelMonitorV2ErrorCategories is the ordered, versioned taxonomy used by the
	// classifier and admin settings UI. Unmatched errors become "other".
	ChannelMonitorV2ErrorCategories = []string{
		"content_policy",
		"authentication",
		"context_limit",
		"invalid_request",
		"model_unsupported",
		"group_access",
		"quota_or_balance",
		"account_pool_unavailable",
		"rate_or_capacity",
		"timeout",
		"transport_or_stream",
		"upstream_forbidden",
		"not_found",
		"client_cancelled",
		"upstream_5xx",
		"internal",
		"other",
	}

	// ChannelMonitorV2KnownPlatforms is the complete V2 settings catalog.
	// Platform entries describe supported provider families and their display scopes.
	ChannelMonitorV2KnownPlatforms = []string{
		"anthropic",
		"openai",
		"grok",
		"kiro",
		"gemini",
		"antigravity",
		"kimi",
		"zhipu",
		"deepseek",
		"qoder",
		"opencode",
		"jev",
	}

	// DefaultChannelMonitorV2IgnoredErrorCategories are factory defaults for
	// ignored_error_categories: excluded from error_rate / health scoring only.
	// Operators can clear or extend via admin config.
	DefaultChannelMonitorV2IgnoredErrorCategories = []string{
		"authentication",
		"client_cancelled",
		"content_policy",
		"context_limit",
		"group_access",
		"model_unsupported",
		"not_found",
		"quota_or_balance",
	}
)

// ChannelMonitorV2GroupBy represents passive monitor group by data used by passive channel queries.
type ChannelMonitorV2GroupBy string

// ChannelMonitorV2PlatformConfig contains one provider family and its visible model selection.
type ChannelMonitorV2PlatformConfig struct {
	Platform string   `json:"platform"` // saved execution platform name
	Enabled  bool     `json:"enabled"`  // configured passive monitoring switch
	Models   []string `json:"models"`   // selected display model identifiers
}

// ChannelMonitorV2Config contains versioned monitor scopes, scoring thresholds, and refresh cadence.
type ChannelMonitorV2Config struct {
	// optimistic configuration revision number
	Version int `json:"version"`
	// configured passive monitoring switch
	Enabled                bool `json:"enabled"`
	RefreshIntervalSeconds int  `json:"refresh_interval_seconds"` // seconds
	// enabled platform scope entries
	Platforms []ChannelMonitorV2PlatformConfig `json:"platforms"`
	// configured group identifier scope
	GroupIDs []int64 `json:"group_ids"`
	// stored health thresholds value
	HealthThresholds ChannelMonitorV2HealthThresholds `json:"health_thresholds"`
	// IgnoredErrorCategories are excluded from error_rate / health scoring.
	// They still appear in the error breakdown with ignored=true (greyed in UI).
	// Unknown categories always roll into "other" via the taxonomy classifier.
	IgnoredErrorCategories []string  `json:"ignored_error_categories"` // stored ignored error categories value
	UpdatedAt              time.Time `json:"updated_at"`               // last configuration update time
	UpdatedBy              *int64    `json:"updated_by,omitempty"`     // last administrator operator identifier
}

// ChannelMonitorV2Filter contains a product window and intersectable request and viewer scopes.
type ChannelMonitorV2Filter struct {
	Range     string   // selected product time window
	Platforms []string // enabled platform scope entries
	GroupIDs  []int64  // configured group identifier scope
	// AllowedGroupIDs is the server-derived group scope for an ordinary viewer.
	// RestrictGroups distinguishes an explicitly restricted viewer with no
	// allowed groups from the unrestricted administrator/configuration scope
	// represented by an empty slice.
	AllowedGroupIDs []int64       // server-authorized group identifier scope
	RestrictGroups  bool          // enforce server-derived group restrictions
	Models          []string      // selected display model identifiers
	Start           time.Time     // inclusive UTC query start
	End             time.Time     // exclusive UTC query end
	Bucket          time.Duration // fixed display bucket duration
}

// ChannelMonitorV2Metric contains measured request ratios, token rates, and upstream latency metrics.
type ChannelMonitorV2Metric struct {
	// observed successful eligible requests
	SuccessRequests int64 `json:"success_requests"`
	// all observed failed requests
	ErrorRequests int64 `json:"error_requests"`
	// successful plus failed requests
	RequestCount int64 `json:"request_count"`
	// uncached input token count
	InputTokens int64 `json:"input_tokens"`
	// generated output token count
	OutputTokens int64 `json:"output_tokens"`
	// cache creation token count
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	// cached input token count
	CacheReadTokens int64 `json:"cache_read_tokens"`
	// combined measured token count
	TokenCount int64 `json:"token_count"`
	// requests per covered minute
	RPM float64 `json:"rpm"`
	// tokens per covered minute
	TPM float64 `json:"tpm"`
	// scored nonignored error ratio
	ErrorRate float64 `json:"error_rate"`
	// actual successful request ratio
	SuccessRate float64 `json:"success_rate"`
	// cached input token ratio
	CacheRate float64 `json:"cache_rate"`
	// cache-read token count
	CacheRateNumerator int64 `json:"cache_rate_numerator"`
	// input plus cache token count
	CacheRateDenominator int64 `json:"cache_rate_denominator"`
	// upstream first-output latency metrics
	TTFT ChannelMonitorV2Latency `json:"ttft"`
	// upstream exchange latency metrics
	Duration ChannelMonitorV2Latency `json:"duration"`
	// requests with recorded upstream failures
	UpstreamAffectedRequests *int64 `json:"upstream_affected_requests,omitempty"`
	// recorded upstream error attempt count
	UpstreamAttemptCount *int64 `json:"upstream_attempt_count,omitempty"`
}

// ChannelMonitorV2Latency contains nullable histogram percentile estimates with observed latency counts.
type ChannelMonitorV2Latency struct {
	SampleCount int64    `json:"sample_count"` // observed valid latency count
	P50Ms       *int64   `json:"p50_ms"`       // milliseconds
	P90Ms       *int64   `json:"p90_ms"`       // milliseconds
	P95Ms       *int64   `json:"p95_ms"`       // milliseconds
	AvgMs       *float64 `json:"avg_ms"`       // milliseconds
}

// ChannelMonitorV2Health contains sample-gated health categories and weighted signal score values.
type ChannelMonitorV2Health struct {
	Overall   string `json:"overall"`    // combined weighted health category
	ErrorRate string `json:"error_rate"` // scored error health category
	TTFT      string `json:"ttft"`       // upstream first-output latency metrics
	Cache     string `json:"cache"`      // cache ratio health category
	// Score is 0–100 when samples are sufficient; omitted/null when unknown.
	// Overall blends error-rate, TTFT p50, and cache rate (weights in Thresholds).
	Score          *float64 `json:"score,omitempty"`            // combined weighted health score
	ErrorRateScore *float64 `json:"error_rate_score,omitempty"` // error ratio health score
	// first-output latency health score
	TTFTScore     *float64 `json:"ttft_score,omitempty"`
	CacheScore    *float64 `json:"cache_score,omitempty"` // cache ratio health score
	MinimumSample int64    `json:"minimum_sample"`        // minimum eligible request count
	// normalized configured health thresholds
	Thresholds ChannelMonitorV2HealthThresholds `json:"thresholds"`
}

// ChannelMonitorV2HealthThresholds defines request eligibility and weighted error, latency, and cache scoring.
type ChannelMonitorV2HealthThresholds struct {
	// MinimumSample is required before scoring request/latency/cache signals.
	MinimumSample int64 `json:"minimum_sample"` // minimum eligible request count
	// WarningErrorRate / CriticalErrorRate map to discrete bands for legacy UI.
	WarningErrorRate  float64 `json:"warning_error_rate"`  // warning scored error ratio
	CriticalErrorRate float64 `json:"critical_error_rate"` // critical scored error ratio
	// TargetTTFTMs is the primary TTFT p50 budget (ms). At or below this is full TTFT score.
	TargetTTFTMs   int64 `json:"target_ttft_ms"`   // milliseconds
	WarningTTFTMs  int64 `json:"warning_ttft_ms"`  // milliseconds
	CriticalTTFTMs int64 `json:"critical_ttft_ms"` // milliseconds
	// WarningCacheRate / CriticalCacheRate: cache rate below these → warning/critical bands.
	// Higher cache rate is better; zero floors disable penalties for cache misses.
	WarningCacheRate  float64 `json:"warning_cache_rate"`  // warning cache ratio floor
	CriticalCacheRate float64 `json:"critical_cache_rate"` // critical cache ratio floor
	// ErrorWeight + TTFTWeight + CacheWeight should sum to 1.0.
	ErrorWeight float64 `json:"error_weight"` // configured error scoring weight
	TTFTWeight  float64 `json:"ttft_weight"`  // configured latency scoring weight
	CacheWeight float64 `json:"cache_weight"` // configured cache scoring weight
}

// ChannelMonitorV2Coverage contains requested display bounds and shared committed observation coverage.
type ChannelMonitorV2Coverage struct {
	RequestedStart time.Time `json:"requested_start"` // requested display interval start
	// RequestedEnd is the exclusive upper bound of the UI-selected window
	// (filter.End). Charts/matrices should plot [RequestedStart, RequestedEnd)
	// even when CoverageStart is later (partial backfill).
	RequestedEnd          time.Time `json:"requested_end"`           // exclusive display interval end
	CoverageStart         time.Time `json:"coverage_start"`          // earliest shared covered time
	DataThrough           time.Time `json:"data_through"`            // latest committed covered minute
	ComputedAt            time.Time `json:"computed_at"`             // latest successful aggregation time
	AggregationLagSeconds int64     `json:"aggregation_lag_seconds"` // seconds
	// CoverageComplete is true when aggregated history reaches the requested
	// window start (backfill / product coverage for this range is done).
	// Trailing lag — data_through behind filter.End because UI ends align to
	// the next bucket and may sit slightly in the future — does not make this
	// false once history is covered.
	CoverageComplete bool `json:"coverage_complete"` // requested history fully covered
	BucketSeconds    int  `json:"bucket_seconds"`    // seconds
	// Bootstrap is set while the first-upgrade historical backfill toward the
	// product windows (90m / 24h / 7d / 30d) is still running. Omitted or
	// inactive once the 30d UI range is fully covered; longer retention (90d)
	// may continue silently without a progress banner.
	Bootstrap *ChannelMonitorV2Bootstrap `json:"bootstrap,omitempty"` // optional initial history progress
}

// ChannelMonitorV2Bootstrap reports first-upgrade aggregation progress for the UI.
// Progress is measured against the longest product window (30d), not full 90d retention.
type ChannelMonitorV2Bootstrap struct {
	// Active is true while historical backfill has not yet covered the 30d product window.
	Active bool `json:"active"` // historical product coverage incomplete
	// ProgressPercent is 0–100 of history covered from now back toward TargetStart.
	ProgressPercent int `json:"progress_percent"` // completed product history percentage
	// CoveredFrom is the earliest minute already recomputed (backfill cursor).
	CoveredFrom time.Time `json:"covered_from,omitempty"` // earliest recomputed historical minute
	// TargetStart is now−30d (product bootstrap goal for 90m/24h/7d/30d).
	TargetStart time.Time `json:"target_start,omitempty"` // required product history start
}

// ChannelMonitorV2AggregationWatermark is the durable aggregator cursor state.
type ChannelMonitorV2AggregationWatermark struct {
	UsageCoverageStart time.Time // earliest rebuilt usage minute
	ErrorCoverageStart time.Time // earliest rebuilt error minute
	DataThrough        time.Time // latest committed covered minute
	LastSuccessfulAt   time.Time // latest successful database commit
	// BackfillCursor is the earliest start already recomputed; zero when never set.
	BackfillCursor time.Time // earliest rebuilt historical interval
	// HasData is true once any successful recompute wrote data_through.
	HasData bool // committed aggregation watermark present
}

// ChannelMonitorV2TrendPoint contains metrics and health for one fixed historical display interval.
type ChannelMonitorV2TrendPoint struct {
	BucketStart time.Time              `json:"bucket_start"` // UTC display bucket start
	Metrics     ChannelMonitorV2Metric `json:"metrics"`      // measured rates and latency
	Health      ChannelMonitorV2Health `json:"health"`       // sample-gated channel health decision
}

// ChannelMonitorV2Snapshot combines current metrics, settings, health, coverage, and ordered trends.
type ChannelMonitorV2Snapshot struct {
	Config   ChannelMonitorV2Config       `json:"config"`   // normalized saved monitor settings
	Coverage ChannelMonitorV2Coverage     `json:"coverage"` // shared committed coverage details
	Metrics  ChannelMonitorV2Metric       `json:"metrics"`  // measured rates and latency
	Health   ChannelMonitorV2Health       `json:"health"`   // sample-gated channel health decision
	Trend    []ChannelMonitorV2TrendPoint `json:"trend"`    // ordered display bucket metrics
}

// ChannelMonitorV2Dimension contains a scoped filter choice and its administrator-visible request count.
type ChannelMonitorV2Dimension struct {
	Value        string `json:"value"`              // filter option stable value
	Label        string `json:"label"`              // filter option display label
	Platform     string `json:"platform,omitempty"` // saved execution platform name
	RequestCount int64  `json:"request_count"`      // successful plus failed requests
}

// ChannelMonitorV2GroupDimension contains a current group label and its scoped observation count.
type ChannelMonitorV2GroupDimension struct {
	ID           int64  `json:"id"`                 // stored group database identifier
	Name         string `json:"name"`               // current group display name
	Platform     string `json:"platform,omitempty"` // saved execution platform name
	RequestCount int64  `json:"request_count"`      // successful plus failed requests
}

// ChannelMonitorV2Dimensions contains authorized platform, model, and group choices for page filters.
type ChannelMonitorV2Dimensions struct {
	Platforms []ChannelMonitorV2Dimension      `json:"platforms"` // enabled platform scope entries
	Groups    []ChannelMonitorV2GroupDimension `json:"groups"`    // available group filter choices
	Models    []ChannelMonitorV2Dimension      `json:"models"`    // selected display model identifiers
}

// ChannelMonitorV2ModelRow contains health and metrics for one displayed platform-specific model.
type ChannelMonitorV2ModelRow struct {
	Platform string                 `json:"platform"` // saved execution platform name
	Model    string                 `json:"model"`    // stored or displayed model
	Metrics  ChannelMonitorV2Metric `json:"metrics"`  // measured rates and latency
	Health   ChannelMonitorV2Health `json:"health"`   // sample-gated channel health decision
}

// ChannelMonitorV2MatrixRow contains one dimension total and its ordered fixed history cells.
type ChannelMonitorV2MatrixRow struct {
	Platform  string                       `json:"platform"`             // saved execution platform name
	GroupID   *int64                       `json:"group_id,omitempty"`   // observed group database identifier
	GroupName string                       `json:"group_name,omitempty"` // current group display name
	Model     string                       `json:"model,omitempty"`      // stored or displayed model
	Metrics   ChannelMonitorV2Metric       `json:"metrics"`              // measured rates and latency
	Health    ChannelMonitorV2Health       `json:"health"`               // sample-gated channel health decision
	Buckets   []ChannelMonitorV2TrendPoint `json:"buckets"`              // ordered fixed history cells
}

// ChannelMonitorV2Matrix contains fixed history rows and the common committed coverage interval.
type ChannelMonitorV2Matrix struct {
	GroupBy  ChannelMonitorV2GroupBy     `json:"group_by"` // selected matrix grouping dimensions
	Coverage ChannelMonitorV2Coverage    `json:"coverage"` // shared committed coverage details
	Items    []ChannelMonitorV2MatrixRow `json:"items"`    // ordered query result rows
}

// ChannelMonitorV2ErrorRow contains categorized rates, scoring exclusion, and optional administrator diagnostics.
type ChannelMonitorV2ErrorRow struct {
	Category string                        `json:"category"`          // versioned error taxonomy label
	Count    int64                         `json:"count"`             // observed categorized error count
	Rate     float64                       `json:"rate"`              // categorized request error ratio
	Details  []ChannelMonitorV2ErrorDetail `json:"details,omitempty"` // administrator diagnostic error samples
	// Ignored is true when this category is excluded from error_rate scoring
	// via config.ignored_error_categories. Still shown in breakdown (UI greys it).
	Ignored bool `json:"ignored"` // excluded from health numerator
}

// ChannelMonitorV2ErrorDetail contains a bounded redacted failure sample for authorized administrator display.
type ChannelMonitorV2ErrorDetail struct {
	Platform           string `json:"platform,omitempty"`             // saved execution platform name
	Model              string `json:"model,omitempty"`                // stored or displayed model
	ErrorType          string `json:"error_type,omitempty"`           // stored gateway error category
	StatusCode         int    `json:"status_code,omitempty"`          // observed downstream HTTP status
	UpstreamStatusCode int    `json:"upstream_status_code,omitempty"` // observed upstream HTTP status
	Message            string `json:"message,omitempty"`              // bounded redacted diagnostic text
	Count              int64  `json:"count"`                          // observed categorized error count
}

// ChannelMonitorV2UserRow contains ranked observations and viewer-authorized identity for account display.
type ChannelMonitorV2UserRow struct {
	UserID       *int64                 `json:"user_id,omitempty"`  // observed acting user identifier
	Rank         int                    `json:"rank"`               // one-based visible rank position
	Email        string                 `json:"email,omitempty"`    // current account email label
	Username     string                 `json:"username,omitempty"` // current account display name
	DisplayLabel string                 `json:"display_label"`      // viewer-safe ranking display label
	IsSelf       bool                   `json:"is_self"`            // authenticated current viewer row
	CanDrilldown bool                   `json:"can_drilldown"`      // viewer can inspect identity
	Metrics      ChannelMonitorV2Metric `json:"metrics"`            // measured rates and latency
}

// ChannelMonitorV2List contains ordered typed results and their shared committed history coverage.
type ChannelMonitorV2List[T any] struct {
	Coverage ChannelMonitorV2Coverage `json:"coverage"` // shared committed coverage details
	Items    []T                      `json:"items"`    // ordered query result rows
}

// ChannelMonitorV2Repository provides versioned configuration, scoped observation queries, and transactional history rebuilding.
type ChannelMonitorV2Repository interface {
	// GetConfig returns the persisted versioned scope and health settings. A
	// disabled configuration remains readable for administration; callers decide
	// whether the requested metric operation requires monitoring to be enabled.
	GetConfig(ctx context.Context) (*ChannelMonitorV2Config, error)
	// UpdateConfig replaces settings when the persisted version matches
	// expectedVersion. A concurrent edit returns ErrChannelMonitorV2ConfigConflict;
	// storage increments the saved revision and returns the committed configuration.
	UpdateConfig(ctx context.Context, config ChannelMonitorV2Config, expectedVersion int) (*ChannelMonitorV2Config, error)
	// GetDimensions returns catalog choices within configured and authorized
	// scope. Implementations retain counts for administrator adapters, while the
	// service or HTTP adapter removes volume data before ordinary-viewer delivery.
	GetDimensions(ctx context.Context, filter ChannelMonitorV2Filter, config ChannelMonitorV2Config) (*ChannelMonitorV2Dimensions, error)
	// GetSnapshot returns combined metrics and trend buckets under committed
	// common coverage. Implementations apply the supplied configuration and scope;
	// the service subsequently redacts ordinary-viewer volumes and embedded policies.
	GetSnapshot(ctx context.Context, filter ChannelMonitorV2Filter, config ChannelMonitorV2Config, includeAdmin bool) (*ChannelMonitorV2Snapshot, error)
	// GetModels returns platform-specific model metrics under the selected scope.
	// Display model collapse and ignored-error scoring use the supplied configuration;
	// includeAdmin controls optional upstream diagnostic counters in returned metrics.
	GetModels(ctx context.Context, filter ChannelMonitorV2Filter, config ChannelMonitorV2Config, includeAdmin bool) (*ChannelMonitorV2List[ChannelMonitorV2ModelRow], error)
	// GetMatrix returns dimension totals and fixed history cells under authorized
	// scope. Unobserved cells retain unknown health, and the service removes raw
	// volumes from ordinary-viewer totals and their nested bucket metrics.
	GetMatrix(ctx context.Context, filter ChannelMonitorV2Filter, config ChannelMonitorV2Config, groupBy ChannelMonitorV2GroupBy, includeAdmin bool) (*ChannelMonitorV2Matrix, error)
	// GetErrors loads category rates. When includeAdmin is false, implementations
	// must omit error Details (no ops_error_logs sample scan) for privacy.
	GetErrors(ctx context.Context, filter ChannelMonitorV2Filter, config ChannelMonitorV2Config, includeAdmin bool) (*ChannelMonitorV2List[ChannelMonitorV2ErrorRow], error)
	// GetUsers returns ranked user metrics under the selected authorized scope.
	// The service identifies the current viewer and applies identity redaction;
	// implementations return query-owned collections safe for that in-place mutation.
	GetUsers(ctx context.Context, filter ChannelMonitorV2Filter, config ChannelMonitorV2Config, includeAdmin bool) (*ChannelMonitorV2List[ChannelMonitorV2UserRow], error)
	// GetAggregationWatermark loads durable backfill / coverage cursors for the
	// passive aggregator (and bootstrap progress). Missing row → zero value, nil error.
	GetAggregationWatermark(ctx context.Context) (*ChannelMonitorV2AggregationWatermark, error)
	// RecomputeRange replaces historical facts and fixed rollups within one
	// transaction before applying retention and advancing cursors. Callers provide
	// bounded contexts and retain exclusive cluster leadership until the call returns.
	RecomputeRange(ctx context.Context, start, end time.Time) error
	// RecomputeLiveRange refreshes the trailing live window for the 90m page.
	// Implementations must use a short error-dedup lookback and skip retention
	// prune so a busy Postgres does not stall channel status.
	RecomputeLiveRange(ctx context.Context, start, end time.Time) error
}

// ChannelMonitorV2Service coordinates scoped passive queries and applies ordinary-viewer identity and volume redaction.
type ChannelMonitorV2Service struct {
	repo ChannelMonitorV2Repository // borrowed passive storage interface
	now  func() time.Time           // query UTC wall clock
}

// ChannelMonitorV2BootstrapProgress builds the optional UI progress payload.
// now and coveredFrom should be UTC minute-truncated when possible.
// When history is complete for the 30d product window, returns nil (no banner).
func ChannelMonitorV2BootstrapProgress(now, coveredFrom time.Time, hasData bool) *ChannelMonitorV2Bootstrap {
	now = now.UTC().Truncate(time.Minute)
	targetStart := now.Add(-ChannelMonitorV2BootstrapProductWindow)
	if !hasData {
		return &ChannelMonitorV2Bootstrap{
			Active:          true,
			ProgressPercent: 0,
			TargetStart:     targetStart,
		}
	}
	if coveredFrom.IsZero() {
		// Have data_through but no cursor yet — treat as barely started.
		return &ChannelMonitorV2Bootstrap{
			Active:          true,
			ProgressPercent: 0,
			TargetStart:     targetStart,
		}
	}
	coveredFrom = coveredFrom.UTC().Truncate(time.Minute)
	if !coveredFrom.After(targetStart) {
		// 30d product windows fully covered; hide progress (90d retention may continue).
		return nil
	}
	total := now.Sub(targetStart).Seconds()
	if total <= 0 {
		return nil
	}
	done := now.Sub(coveredFrom).Seconds()
	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}
	// Round to nearest percent; any non-zero history shows at least 1% so the
	// bar moves after the first 2h bootstrap tick (2h/30d ≈ 0.3%).
	pct := int((done/total)*100 + 0.5)
	if done > 0 && pct < 1 {
		pct = 1
	}
	if pct > 100 {
		pct = 100
	}
	if pct >= 100 {
		// Cursor still slightly after target due to rounding — keep active until
		// coveredFrom <= targetStart so the banner does not flash 100% then vanish.
		pct = 99
	}
	return &ChannelMonitorV2Bootstrap{
		Active:          true,
		ProgressPercent: pct,
		CoveredFrom:     coveredFrom,
		TargetStart:     targetStart,
	}
}

// NewChannelMonitorV2Service retains passive storage and a UTC wall clock for
// queries. Construct it before mounting handlers; it starts no worker, and the
// application keeps its repository available through the handler lifetime.
func NewChannelMonitorV2Service(repo ChannelMonitorV2Repository) *ChannelMonitorV2Service {
	return &ChannelMonitorV2Service{repo: repo, now: func() time.Time { return time.Now().UTC() }}
}

// hideThroughputForViewer hides fleet traffic volumes from ordinary viewers.
func (s *ChannelMonitorV2Service) hideThroughputForViewer(_ context.Context, admin bool) bool {
	return !admin
}

// hideUserRankingForViewer keeps anonymized ranking available to authenticated viewers.
func (s *ChannelMonitorV2Service) hideUserRankingForViewer(_ context.Context, _ bool) bool {
	return false
}

// GetConfig loads persisted monitor settings and appends missing known platform
// entries. Returned platform defaults affect this query result; persisting them
// requires a separate version-checked configuration update by an authenticated
// administrator.
func (s *ChannelMonitorV2Service) GetConfig(ctx context.Context) (*ChannelMonitorV2Config, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	ensureChannelMonitorV2KnownPlatforms(cfg)
	return cfg, nil
}

// getEnabledConfig loads normalized settings and rejects disabled monitoring
// before querying metrics.
func (s *ChannelMonitorV2Service) getEnabledConfig(ctx context.Context) (*ChannelMonitorV2Config, error) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if cfg == nil || !cfg.Enabled {
		return nil, ErrChannelMonitorDisabled
	}
	return cfg, nil
}

// UpdateConfig normalizes platform scopes, groups, ignored categories, and
// health thresholds before replacing configuration. The actor identifies the
// administrator, and expectedVersion makes concurrent edits fail with a conflict
// instead of replacing a newer save.
func (s *ChannelMonitorV2Service) UpdateConfig(ctx context.Context, cfg ChannelMonitorV2Config, expectedVersion int, actorID int64) (*ChannelMonitorV2Config, error) {
	ensureChannelMonitorV2KnownPlatforms(&cfg)
	if err := normalizeChannelMonitorV2Config(&cfg); err != nil {
		return nil, err
	}
	cfg.UpdatedBy = &actorID
	return s.repo.UpdateConfig(ctx, cfg, expectedVersion)
}

// ParseFilter builds aligned product windows for the four supported ranges.
// Empty range selects ninety minutes, and other values outside the catalog
// return an invalid-range error. Filters normalize duplicate scope entries while
// keeping group authorization for the HTTP adapter. The exclusive end includes
// the current partial bucket, so it can be slightly later than the current
// minute without requesting future observations from storage.
func (s *ChannelMonitorV2Service) ParseFilter(rangeValue string, platforms, models []string, groupIDs []int64) (ChannelMonitorV2Filter, error) {
	now := s.now().UTC()
	var window, bucket time.Duration
	switch strings.TrimSpace(rangeValue) {
	case "", "90m":
		rangeValue, window, bucket = "90m", 90*time.Minute, 5*time.Minute
	case "24h":
		window, bucket = 24*time.Hour, time.Hour
	case "7d":
		window, bucket = 7*24*time.Hour, 12*time.Hour
	case "30d":
		window, bucket = 30*24*time.Hour, 24*time.Hour
	default:
		return ChannelMonitorV2Filter{}, fmt.Errorf("%w: %s", ErrChannelMonitorV2InvalidRange, rangeValue)
	}
	start, end := now.Add(-window), now
	if bucket > time.Minute {
		// Align display windows to a fixed number of whole buckets. The trailing
		// bucket may point slightly into the future; SQL simply has no future rows,
		// while the current partial bucket can still be shown and recomputed often.
		end = now.Truncate(bucket).Add(bucket)
		start = end.Add(-window)
	}
	return ChannelMonitorV2Filter{
		Range: rangeValue, Platforms: normalizeStringSet(platforms), Models: normalizeStringSet(models), GroupIDs: normalizeInt64Set(groupIDs),
		Start: start, End: end, Bucket: bucket,
	}, nil
}

// Dimensions loads filter choices within configuration and server-authorized
// group scope. Absolute counts remain available to administrator adapters;
// ordinary adapters call RedactChannelMonitorV2Dimensions before serializing the
// shared result to authenticated viewers.
func (s *ChannelMonitorV2Service) Dimensions(ctx context.Context, filter ChannelMonitorV2Filter) (*ChannelMonitorV2Dimensions, error) {
	cfg, err := s.getEnabledConfig(ctx)
	if err != nil {
		return nil, err
	}
	dims, err := s.repo.GetDimensions(ctx, filter, *cfg)
	if err != nil {
		return nil, err
	}
	// Dimension request_count is operational volume; strip for non-admin callers
	// at the API edge. Dimensions is shared by user/admin routes — redaction is
	// applied in the handler for user routes only, so keep raw here.
	return dims, nil
}

// Snapshot loads metrics, health, trends, and coverage for the selected scope.
// Administrator results retain counters and configuration details; ordinary
// results clear volumes and private configuration fields before returning
// repository-owned response values.
func (s *ChannelMonitorV2Service) Snapshot(ctx context.Context, filter ChannelMonitorV2Filter, admin bool) (*ChannelMonitorV2Snapshot, error) {
	cfg, err := s.getEnabledConfig(ctx)
	if err != nil {
		return nil, err
	}
	snap, err := s.repo.GetSnapshot(ctx, filter, *cfg, admin)
	if err != nil {
		return nil, err
	}
	if !admin && snap != nil {
		redactChannelMonitorV2Snapshot(snap, s.hideThroughputForViewer(ctx, admin))
	}
	return snap, nil
}

// Models loads model metrics with the same configured scope as the summary.
// Ordinary viewer results clear volume counters while retaining rates and
// percentiles; administrator results keep the diagnostic counts returned by
// storage.
func (s *ChannelMonitorV2Service) Models(ctx context.Context, filter ChannelMonitorV2Filter, admin bool) (*ChannelMonitorV2List[ChannelMonitorV2ModelRow], error) {
	cfg, err := s.getEnabledConfig(ctx)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.GetModels(ctx, filter, *cfg, admin)
	if err != nil {
		return nil, err
	}
	if !admin && list != nil {
		hideTP := s.hideThroughputForViewer(ctx, admin)
		for i := range list.Items {
			redactChannelMonitorV2Metric(&list.Items[i].Metrics, hideTP)
		}
	}
	return list, nil
}

// Matrix validates the requested grouping and loads its fixed history buckets.
// Platform, group, and model dimensions are combined by the repository using the
// same scope as summary queries. Ordinary viewers receive rate and percentile
// observations with absolute counts removed from totals and individual buckets.
// Missing observations remain unknown through the stored health decision, and
// this method never dispatches a provider test to fill an empty bucket.
func (s *ChannelMonitorV2Service) Matrix(ctx context.Context, filter ChannelMonitorV2Filter, groupBy ChannelMonitorV2GroupBy, admin bool) (*ChannelMonitorV2Matrix, error) {
	if !groupBy.Valid() {
		return nil, fmt.Errorf("%w: %s", ErrChannelMonitorV2InvalidGroupBy, groupBy)
	}
	cfg, err := s.getEnabledConfig(ctx)
	if err != nil {
		return nil, err
	}
	matrix, err := s.repo.GetMatrix(ctx, filter, *cfg, groupBy, admin)
	if err != nil {
		return nil, err
	}
	if !admin && matrix != nil {
		hideTP := s.hideThroughputForViewer(ctx, admin)
		for i := range matrix.Items {
			redactChannelMonitorV2Metric(&matrix.Items[i].Metrics, hideTP)
			for j := range matrix.Items[i].Buckets {
				redactChannelMonitorV2Metric(&matrix.Items[i].Buckets[j].Metrics, hideTP)
			}
		}
	}
	return matrix, nil
}

// ParseChannelMonitorV2GroupBy parses a supported matrix dimension, defaulting
// empty input to platform grouping.
func ParseChannelMonitorV2GroupBy(value string) (ChannelMonitorV2GroupBy, error) {
	groupBy := ChannelMonitorV2GroupBy(strings.TrimSpace(value))
	if groupBy == "" {
		// Default presentation: platform / group (matches operator mental model).
		groupBy = ChannelMonitorV2GroupByPlatformGroup
	}
	if !groupBy.Valid() {
		return "", fmt.Errorf("%w: %s", ErrChannelMonitorV2InvalidGroupBy, value)
	}
	return groupBy, nil
}

// Valid reports whether the dimension belongs to the supported grouping catalog.
func (g ChannelMonitorV2GroupBy) Valid() bool {
	switch g {
	case ChannelMonitorV2GroupByPlatform, ChannelMonitorV2GroupByPlatformGroup, ChannelMonitorV2GroupByPlatformModel, ChannelMonitorV2GroupByPlatformGroupModel:
		return true
	default:
		return false
	}
}

// Errors returns categorized errors using ordinary-viewer detail and volume
// redaction.
func (s *ChannelMonitorV2Service) Errors(ctx context.Context, filter ChannelMonitorV2Filter) (*ChannelMonitorV2List[ChannelMonitorV2ErrorRow], error) {
	return s.ErrorsForViewer(ctx, filter, false)
}

// ErrorsForViewer loads categorized errors and applies viewer-specific
// diagnostic visibility. Ordinary viewers receive category rates and ignored
// flags with counts and samples removed; administrator adapters retain the
// repository diagnostic detail for their authorized scope.
func (s *ChannelMonitorV2Service) ErrorsForViewer(ctx context.Context, filter ChannelMonitorV2Filter, admin bool) (*ChannelMonitorV2List[ChannelMonitorV2ErrorRow], error) {
	cfg, err := s.getEnabledConfig(ctx)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.GetErrors(ctx, filter, *cfg, admin)
	if err != nil {
		return nil, err
	}
	if !admin && list != nil {
		for i := range list.Items {
			list.Items[i].Count = 0
			list.Items[i].Details = nil
		}
	}
	return list, nil
}

// RedactChannelMonitorV2Dimensions clears filter-option request counts while
// retaining available dimension identities. Nil input is accepted; callers own
// the result and invoke this before serializing dimensions for an ordinary
// authenticated viewer.
func RedactChannelMonitorV2Dimensions(dims *ChannelMonitorV2Dimensions) {
	if dims == nil {
		return
	}
	for i := range dims.Platforms {
		dims.Platforms[i].RequestCount = 0
	}
	for i := range dims.Models {
		dims.Models[i].RequestCount = 0
	}
	for i := range dims.Groups {
		dims.Groups[i].RequestCount = 0
	}
}

// redactChannelMonitorV2Snapshot clears snapshot and trend volumes together with
// private configuration details. The operation mutates its response in place;
// callers provide a query-owned result that is not shared with administrator
// responses.
func redactChannelMonitorV2Snapshot(snap *ChannelMonitorV2Snapshot, hideThroughput bool) {
	if snap == nil {
		return
	}
	redactChannelMonitorV2Metric(&snap.Metrics, hideThroughput)
	for i := range snap.Trend {
		redactChannelMonitorV2Metric(&snap.Trend[i].Metrics, hideThroughput)
	}
	// Public snapshot only needs display thresholds + refresh cadence, not
	// operational allow-lists (group_ids, model inventories, ignored categories).
	redactChannelMonitorV2PublicConfig(&snap.Config)
}

// redactChannelMonitorV2PublicConfig removes group policy, model inventories,
// ignored categories, and operator identity from embedded settings. Nil input is
// accepted; health thresholds and refresh cadence remain available for rendering
// an ordinary-viewer snapshot.
func redactChannelMonitorV2PublicConfig(cfg *ChannelMonitorV2Config) {
	if cfg == nil {
		return
	}
	cfg.GroupIDs = nil
	cfg.IgnoredErrorCategories = nil
	cfg.UpdatedBy = nil
	for i := range cfg.Platforms {
		cfg.Platforms[i].Models = nil
	}
}

// redactChannelMonitorV2Metric clears absolute counts and optional upstream
// diagnostics while retaining rates and percentiles. The caller owns the mutable
// metric; hideThroughput additionally clears RPM and TPM to prevent estimates of
// fleet volume from rate and window length.
func redactChannelMonitorV2Metric(m *ChannelMonitorV2Metric, hideThroughput bool) {
	if m == nil {
		return
	}
	m.SuccessRequests = 0
	m.ErrorRequests = 0
	m.RequestCount = 0
	m.InputTokens = 0
	m.OutputTokens = 0
	m.CacheCreationTokens = 0
	m.CacheReadTokens = 0
	m.TokenCount = 0
	m.CacheRateNumerator = 0
	m.CacheRateDenominator = 0
	// Latency sample_count is also a volume signal.
	m.TTFT.SampleCount = 0
	m.Duration.SampleCount = 0
	m.UpstreamAffectedRequests = nil
	m.UpstreamAttemptCount = nil
	if hideThroughput {
		m.RPM = 0
		m.TPM = 0
	}
}

// Users loads a ranking and identifies the authenticated viewer within it.
// Administrator rows retain account identifiers; ordinary rows retain identity
// only for the current viewer and label other users anonymously. The visible top
// ten also includes the current viewer when ranked below that limit, and an
// unused account receives an unranked self row. Redaction clears volume counters
// before results return to ordinary handlers.
func (s *ChannelMonitorV2Service) Users(ctx context.Context, filter ChannelMonitorV2Filter, viewerID int64, admin bool) (*ChannelMonitorV2List[ChannelMonitorV2UserRow], error) {
	cfg, err := s.getEnabledConfig(ctx)
	if err != nil {
		return nil, err
	}
	if s.hideUserRankingForViewer(ctx, admin) {
		return &ChannelMonitorV2List[ChannelMonitorV2UserRow]{Items: []ChannelMonitorV2UserRow{}}, nil
	}
	result, err := s.repo.GetUsers(ctx, filter, *cfg, admin)
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = &ChannelMonitorV2List[ChannelMonitorV2UserRow]{}
	}
	selfIndex := -1
	for i := range result.Items {
		result.Items[i].Rank = i + 1
		if result.Items[i].UserID != nil && *result.Items[i].UserID == viewerID {
			selfIndex = i
			result.Items[i].IsSelf = true
		}
	}
	// Viewer with no traffic in this window is still shown (and highlighted) so
	// ranking always answers "where am I?" — not only when already in the top list.
	if selfIndex < 0 && viewerID > 0 {
		id := viewerID
		selfRow := ChannelMonitorV2UserRow{
			UserID:       &id,
			Rank:         0, // unranked / no traffic in window
			IsSelf:       true,
			CanDrilldown: true,
			DisplayLabel: "Me",
			Metrics:      ChannelMonitorV2Metric{},
		}
		result.Items = append(result.Items, selfRow)
		selfIndex = len(result.Items) - 1
	}
	result.Items = channelMonitorV2TopUsersWithSelf(result.Items, selfIndex, 10)
	hideTP := s.hideThroughputForViewer(ctx, admin)
	if admin {
		// Keep identity for admin; still mark self for UI highlight.
		for i := range result.Items {
			if result.Items[i].UserID != nil && *result.Items[i].UserID == viewerID {
				result.Items[i].IsSelf = true
				if result.Items[i].DisplayLabel == "" || result.Items[i].DisplayLabel == "Me" {
					// Prefer real label when available from repo.
					if result.Items[i].Username != "" {
						result.Items[i].DisplayLabel = result.Items[i].Username
					} else if result.Items[i].Email != "" {
						result.Items[i].DisplayLabel = result.Items[i].Email
					} else {
						result.Items[i].DisplayLabel = "Me"
					}
				}
			}
		}
		return result, nil
	}
	for i := range result.Items {
		redactChannelMonitorV2Metric(&result.Items[i].Metrics, hideTP)
	}
	for i := range result.Items {
		row := &result.Items[i]
		if row.UserID != nil && *row.UserID == viewerID {
			row.IsSelf, row.CanDrilldown, row.DisplayLabel = true, true, "Me"
			continue
		}
		row.UserID, row.Email, row.Username, row.CanDrilldown = nil, "", "", false
		row.DisplayLabel = fmt.Sprintf("Other user #%d", i+1)
	}
	return result, nil
}

// channelMonitorV2TopUsersWithSelf limits a ranking while keeping a lower-ranked
// viewer row visible. A nonpositive limit returns the input unchanged; otherwise
// the returned slice copies the selected rows before optionally appending that
// viewer.
func channelMonitorV2TopUsersWithSelf(items []ChannelMonitorV2UserRow, selfIndex int, limit int) []ChannelMonitorV2UserRow {
	if limit <= 0 {
		return items
	}
	if len(items) <= limit {
		return items
	}
	out := append([]ChannelMonitorV2UserRow(nil), items[:limit]...)
	if selfIndex >= limit && selfIndex < len(items) {
		out = append(out, items[selfIndex])
	}
	return out
}

// normalizeChannelMonitorV2Config validates refresh cadence and group
// identifiers while normalizing display scopes. The configuration is caller-
// owned and mutated before persistence; duplicate platforms or invalid refresh
// values return a configuration error, while repeated models and ignored
// categories are deduplicated.
func normalizeChannelMonitorV2Config(cfg *ChannelMonitorV2Config) error {
	if cfg.RefreshIntervalSeconds == 0 {
		cfg.RefreshIntervalSeconds = 300
	}
	if cfg.RefreshIntervalSeconds != 60 && cfg.RefreshIntervalSeconds != 300 {
		return fmt.Errorf("%w: refresh_interval_seconds must be 60 or 300", ErrChannelMonitorV2InvalidConfig)
	}
	var err error
	cfg.GroupIDs, err = normalizeChannelMonitorV2GroupIDs(cfg.GroupIDs)
	if err != nil {
		return err
	}
	cfg.IgnoredErrorCategories = normalizeChannelMonitorV2IgnoredCategories(cfg.IgnoredErrorCategories)
	cfg.HealthThresholds = NormalizeChannelMonitorV2HealthThresholds(cfg.HealthThresholds)
	seen := make(map[string]struct{}, len(cfg.Platforms))
	for i := range cfg.Platforms {
		p := &cfg.Platforms[i]
		p.Platform = strings.ToLower(strings.TrimSpace(p.Platform))
		if p.Platform == "" {
			return fmt.Errorf("%w: empty platform", ErrChannelMonitorV2InvalidConfig)
		}
		if _, ok := seen[p.Platform]; ok {
			return fmt.Errorf("%w: duplicate platform %s", ErrChannelMonitorV2InvalidConfig, p.Platform)
		}
		seen[p.Platform] = struct{}{}
		p.Models = normalizeStringSet(p.Models)
	}
	sort.Slice(cfg.Platforms, func(i, j int) bool { return cfg.Platforms[i].Platform < cfg.Platforms[j].Platform })
	return nil
}

// ensureChannelMonitorV2KnownPlatforms appends enabled defaults for supported
// platforms absent from the configuration. Existing entries retain their flags
// and model choices; nil input is accepted and persistence remains the caller's
// responsibility.
func ensureChannelMonitorV2KnownPlatforms(cfg *ChannelMonitorV2Config) {
	if cfg == nil {
		return
	}
	seen := make(map[string]struct{}, len(cfg.Platforms))
	for _, p := range cfg.Platforms {
		name := strings.ToLower(strings.TrimSpace(p.Platform))
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	for _, name := range ChannelMonitorV2KnownPlatforms {
		if _, ok := seen[name]; ok {
			continue
		}
		cfg.Platforms = append(cfg.Platforms, ChannelMonitorV2PlatformConfig{
			Platform: name,
			Enabled:  true,
			Models:   []string{},
		})
	}
}

// DefaultChannelMonitorV2HealthThresholds returns Kedaya scoring defaults with
// disabled cache-miss penalties and fifty required requests.
func DefaultChannelMonitorV2HealthThresholds() ChannelMonitorV2HealthThresholds {
	return ChannelMonitorV2HealthThresholds{
		MinimumSample:     50,
		WarningErrorRate:  0.05,
		CriticalErrorRate: 0.20,
		TargetTTFTMs:      3000,
		WarningTTFTMs:     3000,
		CriticalTTFTMs:    10000,
		// A zero/zero cache threshold means cache misses do not affect health
		// until an operator explicitly configures cache scoring.
		WarningCacheRate:  0,
		CriticalCacheRate: 0,
		ErrorWeight:       0.60,
		TTFTWeight:        0.20,
		CacheWeight:       0.20,
	}
}

// NormalizeChannelMonitorV2HealthThresholds bounds sample requirements, error
// ratios, latency thresholds, cache floors, and scoring weights. Missing
// positive settings use their defaults, and warning thresholds cannot exceed
// critical thresholds for lower-is-better signals. Cache floors use the reverse
// ordering because higher cache rate is preferable. Zero cache floors remain
// valid to disable cache-miss penalties, while an entirely nonpositive weight
// set uses the default blend for available observations.
func NormalizeChannelMonitorV2HealthThresholds(in ChannelMonitorV2HealthThresholds) ChannelMonitorV2HealthThresholds {
	def := DefaultChannelMonitorV2HealthThresholds()
	if in.MinimumSample <= 0 {
		in.MinimumSample = def.MinimumSample
	}
	if in.MinimumSample < 1 {
		in.MinimumSample = 1
	}
	if in.MinimumSample > 10000 {
		in.MinimumSample = 10000
	}
	if in.WarningErrorRate <= 0 {
		in.WarningErrorRate = def.WarningErrorRate
	}
	if in.CriticalErrorRate <= 0 {
		in.CriticalErrorRate = def.CriticalErrorRate
	}
	if in.CriticalErrorRate < in.WarningErrorRate {
		in.CriticalErrorRate = in.WarningErrorRate
	}
	if in.TargetTTFTMs <= 0 {
		in.TargetTTFTMs = def.TargetTTFTMs
	}
	if in.WarningTTFTMs <= 0 {
		in.WarningTTFTMs = def.WarningTTFTMs
	}
	if in.WarningTTFTMs < in.TargetTTFTMs {
		in.WarningTTFTMs = in.TargetTTFTMs + 1
	}
	if in.CriticalTTFTMs <= 0 {
		in.CriticalTTFTMs = def.CriticalTTFTMs
	}
	if in.CriticalTTFTMs < in.WarningTTFTMs {
		in.CriticalTTFTMs = in.WarningTTFTMs
	}
	if in.WarningCacheRate < 0 {
		in.WarningCacheRate = 0
	}
	if in.CriticalCacheRate < 0 {
		in.CriticalCacheRate = 0
	}
	if in.WarningCacheRate > 1 {
		in.WarningCacheRate = 1
	}
	if in.CriticalCacheRate > 1 {
		in.CriticalCacheRate = 1
	}
	if in.CriticalCacheRate > in.WarningCacheRate {
		in.CriticalCacheRate = in.WarningCacheRate
	}
	if in.ErrorWeight <= 0 && in.TTFTWeight <= 0 && in.CacheWeight <= 0 {
		in.ErrorWeight, in.TTFTWeight, in.CacheWeight = def.ErrorWeight, def.TTFTWeight, def.CacheWeight
	}
	return in
}

// normalizeChannelMonitorV2IgnoredCategories sorts known unique error categories
// after trimming and case normalization.
func normalizeChannelMonitorV2IgnoredCategories(values []string) []string {
	allowed := make(map[string]struct{}, len(ChannelMonitorV2ErrorCategories))
	for _, c := range ChannelMonitorV2ErrorCategories {
		allowed[c] = struct{}{}
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := allowed[value]; !ok {
			// Unknown labels are dropped so a typo cannot silently create a new category.
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// ChannelMonitorV2IgnoredCategorySet indexes configured ignored categories for
// constant-time membership checks during aggregation.
func ChannelMonitorV2IgnoredCategorySet(cfg ChannelMonitorV2Config) map[string]struct{} {
	set := make(map[string]struct{}, len(cfg.IgnoredErrorCategories))
	for _, c := range cfg.IgnoredErrorCategories {
		set[c] = struct{}{}
	}
	return set
}

// normalizeStringSet returns sorted unique nonempty strings after trimming
// surrounding whitespace.
func normalizeStringSet(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// normalizeInt64Set returns sorted unique positive identifiers for optional
// query scope.
func normalizeInt64Set(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

// normalizeChannelMonitorV2GroupIDs validates configured group identifiers and
// returns sorted unique values. Any nonpositive identifier rejects the
// configuration; an empty list remains the unrestricted configured group scope
// rather than an empty viewer permission set.
func normalizeChannelMonitorV2GroupIDs(values []int64) ([]int64, error) {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			return nil, fmt.Errorf("%w: group_ids must be positive", ErrChannelMonitorV2InvalidConfig)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	slices.Sort(out)
	return out, nil
}

// ChannelMonitorV2HealthFor scores observed channel metrics with the standard
// Kedaya threshold defaults.
func ChannelMonitorV2HealthFor(metrics ChannelMonitorV2Metric) ChannelMonitorV2Health {
	return ChannelMonitorV2HealthForWithThresholds(metrics, DefaultChannelMonitorV2HealthThresholds())
}

// ChannelMonitorV2HealthForWithThresholds scores eligible request, latency, and
// cache observations using normalized thresholds. Request count gates all
// scoring before secondary signals are considered, keeping low-traffic channels
// unknown even when token or latency samples exist. Available signals contribute
// their configured weights; missing latency cannot act as a zero-delay sample.
// Cache floors of zero grant a full cache score, and a nonpositive available
// weight sum leaves overall health unknown.
func ChannelMonitorV2HealthForWithThresholds(metrics ChannelMonitorV2Metric, thresholds ChannelMonitorV2HealthThresholds) ChannelMonitorV2Health {
	thresholds = NormalizeChannelMonitorV2HealthThresholds(thresholds)
	result := ChannelMonitorV2Health{
		Overall: "unknown", ErrorRate: "unknown", TTFT: "unknown", Cache: "unknown",
		MinimumSample: thresholds.MinimumSample, Thresholds: thresholds,
	}
	// Require enough logical requests before scoring secondary signals; otherwise
	// a few latency or cache observations could incorrectly imply healthy traffic.
	if metrics.RequestCount < result.MinimumSample {
		return result
	}

	// scored retains one available signal and its configured contribution weight.
	type scored struct {
		score  float64 // bounded signal health score
		weight float64 // configured available signal weight
		band   string  // individual signal health category
	}
	parts := make([]scored, 0, 3)

	if metrics.RequestCount >= result.MinimumSample {
		s := errorRateScore(metrics.ErrorRate, thresholds.CriticalErrorRate)
		result.ErrorRateScore = &s
		result.ErrorRate = healthBand(metrics.ErrorRate, thresholds.WarningErrorRate, thresholds.CriticalErrorRate)
		parts = append(parts, scored{score: s, weight: thresholds.ErrorWeight, band: result.ErrorRate})
	}
	// Prefer p50 for TTFT scoring; fall back to p95 only if p50 is missing.
	if metrics.TTFT.SampleCount >= result.MinimumSample {
		var ttftMs *int64
		if metrics.TTFT.P50Ms != nil {
			ttftMs = metrics.TTFT.P50Ms
		} else if metrics.TTFT.P95Ms != nil {
			ttftMs = metrics.TTFT.P95Ms
		}
		if ttftMs != nil {
			s := ttftP50Score(float64(*ttftMs), float64(thresholds.TargetTTFTMs), float64(thresholds.CriticalTTFTMs))
			result.TTFTScore = &s
			result.TTFT = healthBand(float64(*ttftMs), float64(thresholds.WarningTTFTMs), float64(thresholds.CriticalTTFTMs))
			parts = append(parts, scored{score: s, weight: thresholds.TTFTWeight, band: result.TTFT})
		}
	}
	// Cache: need a meaningful denominator; higher rate is better.
	if metrics.CacheRateDenominator >= result.MinimumSample {
		s := cacheRateScore(metrics.CacheRate)
		if thresholds.WarningCacheRate <= 0 && thresholds.CriticalCacheRate <= 0 {
			// A zero/zero cache threshold means "do not penalize cache misses".
			s = 100
		}
		result.CacheScore = &s
		// Invert for healthBand (lower is worse): use (1 - rate) against warning/critical floors.
		result.Cache = cacheRateBand(metrics.CacheRate, thresholds.WarningCacheRate, thresholds.CriticalCacheRate)
		parts = append(parts, scored{score: s, weight: thresholds.CacheWeight, band: result.Cache})
	}

	if len(parts) == 0 {
		return result
	}
	var weightSum, scoreSum float64
	for _, p := range parts {
		weightSum += p.weight
		scoreSum += p.weight * p.score
	}
	if weightSum <= 0 {
		return result
	}
	overall := scoreSum / weightSum
	result.Score = &overall
	result.Overall = scoreBand(overall)
	return result
}

// errorRateScore maps error ratio linearly from one hundred at zero errors to
// zero at the critical ratio. Nonpositive critical input uses a five-percent
// default; the result is bounded by the endpoint cases.
func errorRateScore(errorRate, critical float64) float64 {
	if critical <= 0 {
		critical = 0.05
	}
	if errorRate <= 0 {
		return 100
	}
	if errorRate >= critical {
		return 0
	}
	return 100 * (1 - errorRate/critical)
}

// ttftP50Score maps latency between target and critical milliseconds to a
// bounded score. Missing positive target input uses 2500 milliseconds; a
// critical value below target is replaced by a multiple before interpolation.
func ttftP50Score(p50Ms, targetMs, criticalMs float64) float64 {
	if targetMs <= 0 {
		targetMs = 2500
	}
	if criticalMs <= targetMs {
		criticalMs = targetMs * 2.4
	}
	if p50Ms <= targetMs {
		return 100
	}
	if p50Ms >= criticalMs {
		return 0
	}
	return 100 * (1 - (p50Ms-targetMs)/(criticalMs-targetMs))
}

// cacheRateScore converts cache ratio into a score bounded between zero and one
// hundred.
func cacheRateScore(cacheRate float64) float64 {
	if cacheRate <= 0 {
		return 0
	}
	if cacheRate >= 1 {
		return 100
	}
	return 100 * cacheRate
}

// cacheRateBand classifies cache ratio against lower warning and critical
// floors.
func cacheRateBand(cacheRate, warning, critical float64) string {
	if cacheRate < critical {
		return "critical"
	}
	if cacheRate < warning {
		return "warning"
	}
	return "healthy"
}

// scoreBand classifies a bounded health score using eighty and fifty cutoffs.
func scoreBand(score float64) string {
	switch {
	case score >= 80:
		return "healthy"
	case score >= 50:
		return "warning"
	default:
		return "critical"
	}
}

// healthBand classifies a measured value against upper warning and critical
// thresholds.
func healthBand(value, warning, critical float64) string {
	if value >= critical {
		return "critical"
	}
	if value >= warning {
		return "warning"
	}
	return "healthy"
}
