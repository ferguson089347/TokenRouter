package postgres

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/intelligence"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// community.go persists public user-test configurations and their queue admission.
// A source-aware identity keeps each group and model's community history apart
// from administrator observations. Submissions retain a Key reference and execution
// settings while the existing run queue provides generation, evaluation, and retention.
//
// Advisory locks serialize community configuration creation and Key-level admission,
// and configuration row locks protect execution settings from active work. A single
// transaction enforces the configuration cap, a per-Key active limit, and a brief
// shared-row interval before updating settings and publishing the new job. Failed
// admission rolls back the settings together with the queue record and schedule.

// ListCommunityConfigs reads up to two hundred user-test rows in display order.
func (r *intelligenceRepository) ListCommunityConfigs(ctx context.Context) ([]intelligence.IntelligenceConfig, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+intelligenceConfigColumns+` FROM intelligence_test_configs c JOIN groups g ON g.id=c.group_id WHERE c.source='user' ORDER BY g.sort_order,g.id,c.id LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	configs := make([]intelligence.IntelligenceConfig, 0)
	for rows.Next() {
		config, err := scanIntelligenceConfig(rows)
		if err != nil {
			return nil, err
		}
		configs = append(configs, *config)
	}
	return configs, rows.Err()
}

// CreateCommunityRun queues one user drawing while replacing its idle settings.
// Community identity uses source, group, and model. One active job per selected
// Key, a sixty-second shared-row interval, and a two-hundred-configuration limit
// apply before any chargeable work is published to workers.
//
// The transaction holds advisory and configuration locks through the final queue
// insert. Every failure rolls back settings and schedule changes, so a rejected
// concurrent submission cannot replace the credential selected for active work.
// The caller has already validated Key ownership, group access, and request fields.
func (r *intelligenceRepository) CreateCommunityRun(ctx context.Context, config *intelligence.IntelligenceConfig, now time.Time) (*intelligence.IntelligenceRun, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Keep replaced settings and queue insertion together if any admission or
	// persistence operation fails, releasing both advisory and row locks on rollback.
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(30720261021)`); err != nil {
		return nil, err
	}
	var busy bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM intelligence_test_runs r JOIN intelligence_test_configs c ON c.id=r.config_id WHERE c.source='user' AND ((c.api_key_id=$1 AND r.status IN ('queued','submitting','running')) OR (c.group_id=$3 AND c.model=$4 AND r.created_at>$2::timestamptz-INTERVAL '60 seconds')))`, config.APIKeyID, now, config.GroupID, config.Model).Scan(&busy)
	if err != nil {
		return nil, err
	}
	if busy {
		return nil, apperror.Conflict("INTELLIGENCE_SELF_TEST_BUSY", "此 Key 正在测试，或当前渠道和模型刚刚提交，请稍后再试")
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM intelligence_test_configs WHERE source='user'`).Scan(&count); err != nil {
		return nil, err
	}
	var existing int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM intelligence_test_configs WHERE source='user' AND group_id=$1 AND model=$2 AND benchmark='drawing'`, config.GroupID, config.Model).Scan(&existing); err != nil {
		return nil, err
	}
	if existing == 0 && count >= 200 {
		return nil, apperror.Conflict("INTELLIGENCE_CONFIG_LIMIT", "用户测试配置已达到上限")
	}
	if existing > 0 {
		if err := lockIntelligenceConfig(ctx, tx, existing); err != nil {
			return nil, err
		}
		if err := checkIntelligenceIdle(ctx, tx, existing); err != nil {
			return nil, err
		}
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO intelligence_test_configs(source,group_id,model,benchmark,base_url,api_key_id,protocol,reasoning_effort,service_tier,prompt,enabled,schedule_enabled,interval_minutes)
VALUES('user',$1,$2,'drawing',$3,$4,$5,$6,$7,$8,true,false,60)
ON CONFLICT(source,group_id,model,benchmark) DO UPDATE SET base_url=EXCLUDED.base_url,api_key_id=EXCLUDED.api_key_id,protocol=EXCLUDED.protocol,reasoning_effort=EXCLUDED.reasoning_effort,service_tier=EXCLUDED.service_tier,prompt=EXCLUDED.prompt,enabled=true,schedule_enabled=false,updated_at=NOW()
RETURNING id,created_at,updated_at`, config.GroupID, config.Model, config.BaseURL, config.APIKeyID, config.Protocol, config.ReasoningEffort, config.ServiceTier, config.Prompt).Scan(&config.ID, &config.CreatedAt, &config.UpdatedAt)
	if err != nil {
		return nil, err
	}
	run, err := insertIntelligenceRun(ctx, tx, config, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return run, nil
}
