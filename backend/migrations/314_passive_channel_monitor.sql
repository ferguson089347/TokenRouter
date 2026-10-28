-- Passive channel metrics adapted from kiss-kedaya/sub2api V2.
-- Empty tables are populated by bounded background aggregation after startup.

-- Passive channel monitor V2. This migration creates empty rollup tables only;
-- backfill is intentionally handled by a bounded background worker.

CREATE TABLE IF NOT EXISTS channel_monitor_v2_config (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    version INTEGER NOT NULL DEFAULT 1,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    refresh_interval_seconds INTEGER NOT NULL DEFAULT 60
        CHECK (refresh_interval_seconds IN (60, 300)),
    platforms JSONB NOT NULL DEFAULT '[{"platform":"anthropic","enabled":true,"models":[]},{"platform":"openai","enabled":true,"models":[]},{"platform":"grok","enabled":true,"models":[]},{"platform":"kiro","enabled":true,"models":[]},{"platform":"gemini","enabled":true,"models":[]},{"platform":"antigravity","enabled":true,"models":[]}]'::jsonb,
    group_ids BIGINT[] NOT NULL DEFAULT '{}',
    updated_by BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO channel_monitor_v2_config (id) VALUES (1)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS channel_monitor_v2_metrics_1m (
    bucket_start TIMESTAMPTZ NOT NULL,
    platform TEXT NOT NULL,
    group_id BIGINT NOT NULL DEFAULT 0,
    model TEXT NOT NULL,
    success_requests BIGINT NOT NULL DEFAULT 0,
    error_requests BIGINT NOT NULL DEFAULT 0,
    upstream_affected_requests BIGINT NOT NULL DEFAULT 0,
    upstream_attempt_count BIGINT NOT NULL DEFAULT 0,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cache_creation_tokens BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ttft_sum_ms BIGINT NOT NULL DEFAULT 0,
    ttft_count BIGINT NOT NULL DEFAULT 0,
    duration_sum_ms BIGINT NOT NULL DEFAULT 0,
    duration_count BIGINT NOT NULL DEFAULT 0,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (bucket_start, platform, group_id, model)
);

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_metrics_platform_time
    ON channel_monitor_v2_metrics_1m (platform, bucket_start DESC);
CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_metrics_group_time
    ON channel_monitor_v2_metrics_1m (group_id, bucket_start DESC);
CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_metrics_model_time
    ON channel_monitor_v2_metrics_1m (model, bucket_start DESC);

CREATE TABLE IF NOT EXISTS channel_monitor_v2_user_metrics_1m (
    bucket_start TIMESTAMPTZ NOT NULL,
    platform TEXT NOT NULL,
    group_id BIGINT NOT NULL DEFAULT 0,
    model TEXT NOT NULL,
    user_id BIGINT NOT NULL,
    success_requests BIGINT NOT NULL DEFAULT 0,
    error_requests BIGINT NOT NULL DEFAULT 0,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cache_creation_tokens BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ttft_sum_ms BIGINT NOT NULL DEFAULT 0,
    ttft_count BIGINT NOT NULL DEFAULT 0,
    duration_sum_ms BIGINT NOT NULL DEFAULT 0,
    duration_count BIGINT NOT NULL DEFAULT 0,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (bucket_start, platform, group_id, model, user_id)
);

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_user_metrics_user_time
    ON channel_monitor_v2_user_metrics_1m (user_id, bucket_start DESC);
CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_user_metrics_time
    ON channel_monitor_v2_user_metrics_1m (bucket_start DESC);

CREATE TABLE IF NOT EXISTS channel_monitor_v2_error_metrics_1m (
    bucket_start TIMESTAMPTZ NOT NULL,
    platform TEXT NOT NULL,
    group_id BIGINT NOT NULL DEFAULT 0,
    model TEXT NOT NULL,
    error_category TEXT NOT NULL,
    taxonomy_version SMALLINT NOT NULL,
    error_requests BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket_start, platform, group_id, model, error_category, taxonomy_version)
);

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_errors_time
    ON channel_monitor_v2_error_metrics_1m (bucket_start DESC);
CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_errors_category_time
    ON channel_monitor_v2_error_metrics_1m (error_category, bucket_start DESC);

CREATE TABLE IF NOT EXISTS channel_monitor_v2_latency_histograms_1m (
    bucket_start TIMESTAMPTZ NOT NULL,
    platform TEXT NOT NULL,
    group_id BIGINT NOT NULL DEFAULT 0,
    model TEXT NOT NULL,
    user_id BIGINT NOT NULL DEFAULT 0,
    metric TEXT NOT NULL CHECK (metric IN ('ttft', 'duration')),
    upper_bound_ms INTEGER NOT NULL,
    sample_count BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket_start, platform, group_id, model, user_id, metric, upper_bound_ms)
);

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_histograms_time
    ON channel_monitor_v2_latency_histograms_1m (bucket_start DESC, metric);

CREATE TABLE IF NOT EXISTS channel_monitor_v2_watermarks (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    usage_coverage_start TIMESTAMPTZ,
    error_coverage_start TIMESTAMPTZ,
    data_through TIMESTAMPTZ,
    last_successful_at TIMESTAMPTZ,
    backfill_cursor TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO channel_monitor_v2_watermarks (id) VALUES (1)
ON CONFLICT (id) DO NOTHING;

COMMENT ON TABLE channel_monitor_v2_metrics_1m IS
    'One-minute passive channel health facts derived from real user requests; never from active probes.';
COMMENT ON COLUMN channel_monitor_v2_error_metrics_1m.taxonomy_version IS
    'Version of the ordered error classification rules used for this aggregate.';


-- Categories excluded from error_rate / health scoring (still shown in error breakdown).
ALTER TABLE channel_monitor_v2_config
    ADD COLUMN IF NOT EXISTS ignored_error_categories TEXT[] NOT NULL DEFAULT '{}';


-- Configurable Channel Monitor V2 health scoring thresholds.
-- Defaults are intentionally tolerant: small amounts of real traffic errors or
-- zero cache rate should not immediately paint the user page as "abnormal".
ALTER TABLE channel_monitor_v2_config
    ADD COLUMN IF NOT EXISTS health_thresholds JSONB NOT NULL DEFAULT '{
      "minimum_sample": 50,
      "warning_error_rate": 0.05,
      "critical_error_rate": 0.20,
      "target_ttft_ms": 3000,
      "warning_ttft_ms": 8000,
      "critical_ttft_ms": 20000,
      "warning_cache_rate": 0,
      "critical_cache_rate": 0,
      "error_weight": 0.60,
      "ttft_weight": 0.20,
      "cache_weight": 0.20
    }'::jsonb;

UPDATE channel_monitor_v2_config
SET health_thresholds = '{
      "minimum_sample": 50,
      "warning_error_rate": 0.05,
      "critical_error_rate": 0.20,
      "target_ttft_ms": 3000,
      "warning_ttft_ms": 8000,
      "critical_ttft_ms": 20000,
      "warning_cache_rate": 0,
      "critical_cache_rate": 0,
      "error_weight": 0.60,
      "ttft_weight": 0.20,
      "cache_weight": 0.20
    }'::jsonb || health_thresholds
WHERE id = 1;


-- Fixed-size Channel Monitor V2 rollups for the 24h / 7d / 30d views.
-- Source of truth remains the 1-minute passive rollup; these tables cache the
-- UI bucket sizes so historical buckets do not need to be re-aggregated on each
-- filter change. The background aggregator overwrites affected buckets.

CREATE TABLE IF NOT EXISTS channel_monitor_v2_metrics_rollup (
    bucket_start TIMESTAMPTZ NOT NULL,
    bucket_seconds INTEGER NOT NULL CHECK (bucket_seconds IN (300, 3600, 43200, 86400)),
    platform TEXT NOT NULL,
    group_id BIGINT NOT NULL DEFAULT 0,
    model TEXT NOT NULL,
    success_requests BIGINT NOT NULL DEFAULT 0,
    error_requests BIGINT NOT NULL DEFAULT 0,
    upstream_affected_requests BIGINT NOT NULL DEFAULT 0,
    upstream_attempt_count BIGINT NOT NULL DEFAULT 0,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cache_creation_tokens BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ttft_sum_ms BIGINT NOT NULL DEFAULT 0,
    ttft_count BIGINT NOT NULL DEFAULT 0,
    duration_sum_ms BIGINT NOT NULL DEFAULT 0,
    duration_count BIGINT NOT NULL DEFAULT 0,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (bucket_seconds, bucket_start, platform, group_id, model)
);

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_metrics_rollup_platform_time
    ON channel_monitor_v2_metrics_rollup (bucket_seconds, platform, bucket_start DESC);
CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_metrics_rollup_group_time
    ON channel_monitor_v2_metrics_rollup (bucket_seconds, group_id, bucket_start DESC);
CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_metrics_rollup_model_time
    ON channel_monitor_v2_metrics_rollup (bucket_seconds, model, bucket_start DESC);

CREATE TABLE IF NOT EXISTS channel_monitor_v2_user_metrics_rollup (
    bucket_start TIMESTAMPTZ NOT NULL,
    bucket_seconds INTEGER NOT NULL CHECK (bucket_seconds IN (300, 3600, 43200, 86400)),
    platform TEXT NOT NULL,
    group_id BIGINT NOT NULL DEFAULT 0,
    model TEXT NOT NULL,
    user_id BIGINT NOT NULL,
    success_requests BIGINT NOT NULL DEFAULT 0,
    error_requests BIGINT NOT NULL DEFAULT 0,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cache_creation_tokens BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ttft_sum_ms BIGINT NOT NULL DEFAULT 0,
    ttft_count BIGINT NOT NULL DEFAULT 0,
    duration_sum_ms BIGINT NOT NULL DEFAULT 0,
    duration_count BIGINT NOT NULL DEFAULT 0,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (bucket_seconds, bucket_start, platform, group_id, model, user_id)
);

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_user_rollup_user_time
    ON channel_monitor_v2_user_metrics_rollup (bucket_seconds, user_id, bucket_start DESC);
CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_user_rollup_time
    ON channel_monitor_v2_user_metrics_rollup (bucket_seconds, bucket_start DESC);

CREATE TABLE IF NOT EXISTS channel_monitor_v2_error_metrics_rollup (
    bucket_start TIMESTAMPTZ NOT NULL,
    bucket_seconds INTEGER NOT NULL CHECK (bucket_seconds IN (300, 3600, 43200, 86400)),
    platform TEXT NOT NULL,
    group_id BIGINT NOT NULL DEFAULT 0,
    model TEXT NOT NULL,
    error_category TEXT NOT NULL,
    taxonomy_version SMALLINT NOT NULL,
    error_requests BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket_seconds, bucket_start, platform, group_id, model, error_category, taxonomy_version)
);

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_errors_rollup_time
    ON channel_monitor_v2_error_metrics_rollup (bucket_seconds, bucket_start DESC);
CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_errors_rollup_category_time
    ON channel_monitor_v2_error_metrics_rollup (bucket_seconds, error_category, bucket_start DESC);

CREATE TABLE IF NOT EXISTS channel_monitor_v2_latency_histograms_rollup (
    bucket_start TIMESTAMPTZ NOT NULL,
    bucket_seconds INTEGER NOT NULL CHECK (bucket_seconds IN (300, 3600, 43200, 86400)),
    platform TEXT NOT NULL,
    group_id BIGINT NOT NULL DEFAULT 0,
    model TEXT NOT NULL,
    user_id BIGINT NOT NULL DEFAULT 0,
    metric TEXT NOT NULL CHECK (metric IN ('ttft', 'duration')),
    upper_bound_ms INTEGER NOT NULL,
    sample_count BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket_seconds, bucket_start, platform, group_id, model, user_id, metric, upper_bound_ms)
);

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_histograms_rollup_time
    ON channel_monitor_v2_latency_histograms_rollup (bucket_seconds, bucket_start DESC, metric);

COMMENT ON TABLE channel_monitor_v2_metrics_rollup IS
    'Precomputed fixed UI buckets derived from channel_monitor_v2_metrics_1m for 24h/7d/30d views.';


-- Channel Monitor V2 is designed around fixed UI buckets; refresh passive
-- aggregation every 5 minutes and recompute the trailing bucket for each range.
-- Only migrate the untouched factory row.  A row with a newer version or an
-- updater identity reflects an operator choice and must retain its 60-second
-- interval (or any other valid setting).
UPDATE channel_monitor_v2_config
SET refresh_interval_seconds = 300,
    updated_at = NOW()
WHERE id = 1
  AND version = 1
  AND updated_by IS NULL
  AND refresh_interval_seconds = 60;


-- Factory presets for Channel Monitor V2 config:
-- 1) ignored_error_categories: non-ops client/policy failures that should not
--    dominate health error_rate by default (still shown greyed in breakdown).
-- 2) health_thresholds cache floors: 85% watch / 60% critical (higher is better).
--
-- Only apply when the row still looks like factory empty ignore list and/or
-- zero cache thresholds (operator customizations are left alone).

UPDATE channel_monitor_v2_config
SET ignored_error_categories = ARRAY[
    'authentication',
    'client_cancelled',
    'content_policy',
    'context_limit',
    'group_access',
    'model_unsupported',
    'not_found',
    'quota_or_balance'
]::text[]
WHERE id = 1
  AND COALESCE(cardinality(ignored_error_categories), 0) = 0;

UPDATE channel_monitor_v2_config
SET health_thresholds = health_thresholds
    || jsonb_build_object(
        'warning_cache_rate', 0.85,
        'critical_cache_rate', 0.60
    )
WHERE id = 1
  AND COALESCE((health_thresholds->>'warning_cache_rate')::float8, 0) = 0
  AND COALESCE((health_thresholds->>'critical_cache_rate')::float8, 0) = 0;


-- Keep factory cache scoring tolerant. Migration 203 briefly wrote 0.85/0.60;
-- reset only that exact factory pair, preserving operator-customized values.
UPDATE channel_monitor_v2_config
SET health_thresholds = health_thresholds
    || jsonb_build_object(
        'warning_cache_rate', 0,
        'critical_cache_rate', 0
    )
WHERE id = 1
  AND COALESCE((health_thresholds->>'warning_cache_rate')::float8, 0) = 0.85
  AND COALESCE((health_thresholds->>'critical_cache_rate')::float8, 0) = 0.60
  AND version = 1
  AND updated_by IS NULL;


-- Append kimi/zhipu/deepseek to channel_monitor_v2_config.platforms.
-- Factory seed (194/197) only listed the original six platforms, so CN
-- OpenAI-compatible providers never appeared in V2 settings or query scope.
-- Existing platform objects (enabled flags, model allow-lists) are left intact.

UPDATE channel_monitor_v2_config
SET
    platforms = (
        SELECT COALESCE(jsonb_agg(elem), '[]'::jsonb)
        FROM (
            SELECT elem
            FROM jsonb_array_elements(platforms) AS existing(elem)
            UNION ALL
            SELECT elem
            FROM jsonb_array_elements(
                '[{"platform":"kimi","enabled":true,"models":[]},{"platform":"zhipu","enabled":true,"models":[]},{"platform":"deepseek","enabled":true,"models":[]}]'::jsonb
            ) AS missing(elem)
            WHERE NOT EXISTS (
                SELECT 1
                FROM jsonb_array_elements(channel_monitor_v2_config.platforms) AS have(elem)
                WHERE lower(have.elem->>'platform') = lower(missing.elem->>'platform')
            )
        ) combined
    ),
    version = version + 1,
    updated_at = NOW()
WHERE id = 1
  AND (
      NOT (platforms @> '[{"platform":"kimi"}]'::jsonb)
      OR NOT (platforms @> '[{"platform":"zhipu"}]'::jsonb)
      OR NOT (platforms @> '[{"platform":"deepseek"}]'::jsonb)
  );

-- Platform scopes follow the local provider catalog; performance comes from usage.timings.
UPDATE channel_monitor_v2_config SET platforms = platforms ||
 '[{"platform":"qoder","enabled":true,"models":[]},{"platform":"opencode","enabled":true,"models":[]},{"platform":"jev","enabled":true,"models":[]}]'::jsonb
WHERE id=1;

-- Retire recurring group health probes in favor of passive channel metrics.
-- Keep model, prompt, and timing preferences for explicitly requested diagnostics.
UPDATE groups
SET availability_probe_config = jsonb_set(availability_probe_config, '{enabled}', 'false'::jsonb)
WHERE availability_probe_config @> '{"enabled":true}'::jsonb;
