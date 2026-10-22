-- 307_intelligence_user_tests.sql separates site and community result histories.
-- Existing configurations retain the site source; user rows share the
-- durable generation queue while keeping their submitted credentials server-side.
ALTER TABLE intelligence_test_configs
    ADD COLUMN IF NOT EXISTS source VARCHAR(16) NOT NULL DEFAULT 'site'
    CHECK (source IN ('site','user'));
ALTER TABLE intelligence_test_configs
    DROP CONSTRAINT IF EXISTS intelligence_test_configs_group_id_model_benchmark_key;
CREATE UNIQUE INDEX IF NOT EXISTS idx_intelligence_source_identity
    ON intelligence_test_configs(source,group_id,model,benchmark);
