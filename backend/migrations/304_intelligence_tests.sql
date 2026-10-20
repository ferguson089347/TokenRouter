-- 304_intelligence_tests.sql stores model quality configurations and durable remote
-- observations in dedicated tables. An active-run uniqueness constraint prevents
-- concurrent manual or scheduled submissions, while separate record and payload
-- columns keep history queries independent of large drawing documents.
CREATE TABLE IF NOT EXISTS intelligence_test_configs (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id),
    model VARCHAR(255) NOT NULL,
    benchmark VARCHAR(16) NOT NULL CHECK (benchmark IN ('candy','drawing')),
    base_url TEXT NOT NULL,
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    protocol VARCHAR(32) NOT NULL,
    reasoning_effort VARCHAR(32) NOT NULL DEFAULT '',
    service_tier VARCHAR(32) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    schedule_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    interval_minutes INTEGER NOT NULL DEFAULT 60 CHECK (interval_minutes BETWEEN 5 AND 10080),
    next_run_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(group_id,model,benchmark)
);
CREATE TABLE IF NOT EXISTS intelligence_test_runs (
    id VARCHAR(64) PRIMARY KEY,
    config_id BIGINT NOT NULL REFERENCES intelligence_test_configs(id) ON DELETE CASCADE,
    status VARCHAR(16) NOT NULL,
    remote_id VARCHAR(256) NOT NULL DEFAULT '',
    -- Read histories from the small record column so status polling avoids
    -- decompressing large HTML and answer payloads stored by PostgreSQL TOAST.
    record JSONB NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    lease_token VARCHAR(64) NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    next_poll_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- Manual and scheduled work share one active slot per configuration, so
-- concurrent application instances cannot admit duplicate chargeable submissions.
CREATE UNIQUE INDEX IF NOT EXISTS idx_intelligence_one_active ON intelligence_test_runs(config_id)
    WHERE status IN ('queued','submitting','running');
CREATE INDEX IF NOT EXISTS idx_intelligence_due ON intelligence_test_runs(next_poll_at,lease_until)
    WHERE status IN ('queued','submitting','running');
CREATE INDEX IF NOT EXISTS idx_intelligence_history ON intelligence_test_runs(config_id,created_at DESC,id);
