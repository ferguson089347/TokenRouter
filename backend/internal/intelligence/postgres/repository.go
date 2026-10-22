package postgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/TokenFlux/TokenRouter/internal/intelligence"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// repository.go persists configurations, claims due work, and stores quality-
// check observations in PostgreSQL. Configurations and active tasks share row
// locks, and a unique partial index enforces one active task for each
// configuration across processes. New configurations also share a transaction
// advisory lock so the list limit and creation limit stay consistent.
//
// Each claim records a token and expiry. Result updates compare both values
// before changing any observation or applying retention. Small summaries stay
// separate from question, answer, assessment, and HTML payloads, letting list
// queries avoid large stored values. Completed history is bounded to sixty
// records and ten retained drawing bodies. PNG snapshots stay in private
// payload fields, survive worker updates, and follow the sixty-record history
// lifetime even after full HTML is removed.

// intelligenceConfigColumns fixes the joined column order shared by configuration
// listing, row locking and scanners, keeping persisted and display identities aligned.
const intelligenceConfigColumns = `c.id,c.group_id,g.name,c.model,c.benchmark,c.base_url,c.api_key_id,c.protocol,c.reasoning_effort,c.service_tier,c.enabled,c.schedule_enabled,c.interval_minutes,c.next_run_at,c.created_at,c.updated_at,c.prompt,c.source`

// intelligenceRepository implements durable quality-check persistence using a
// shared SQL connection pool.
type intelligenceRepository struct {
	db *sql.DB // shared SQL pool
}

// intelligenceScanner decodes the selected SQL columns from rows or a single
// row.
type intelligenceScanner interface {
	// Scan copies selected SQL columns into caller-provided destinations.
	Scan(...any) error
}

// intelligenceRunPayload holds large text read by detail and worker recovery
// queries.
type intelligenceRunPayload struct {
	HTML             string `json:"html,omitempty"`              // untrusted drawing document
	Question         string `json:"question,omitempty"`          // reported benchmark prompt
	Answer           string `json:"answer,omitempty"`            // reported model answer
	AssessmentReason string `json:"assessment_reason,omitempty"` // remote assessment explanation
	// LocalGeneration and AssessmentAttempts are restored by detail and worker
	// queries. They identify reusable artwork and bound evaluation retries across
	// process restarts and worker replacements.
	LocalGeneration    bool `json:"local_generation,omitempty"`    // local artwork ownership
	AssessmentAttempts int  `json:"assessment_attempts,omitempty"` // HTML submission attempt count
}

// NewIntelligenceRepository constructs persistence using the caller's shared
// SQL pool. The caller retains connection ownership and closes the pool after
// all workers and HTTP requests using this repository have finished.
func NewIntelligenceRepository(db *sql.DB) intelligence.IntelligenceRepository {
	return &intelligenceRepository{db: db}
}

// scanIntelligenceConfig decodes the selected configuration columns and marks
// whether a Key is configured. SQL no-rows becomes the module not-found error,
// keeping detail and mutation callers consistent.
func scanIntelligenceConfig(row intelligenceScanner) (*intelligence.IntelligenceConfig, error) {
	c := &intelligence.IntelligenceConfig{}
	err := row.Scan(
		&c.ID, &c.GroupID, &c.GroupName, &c.Model, &c.Benchmark,
		&c.BaseURL, &c.APIKeyID, &c.Protocol, &c.ReasoningEffort, &c.ServiceTier,
		&c.Enabled, &c.ScheduleEnabled, &c.IntervalMinutes, &c.NextRunAt,
		&c.CreatedAt, &c.UpdatedAt, &c.Prompt, &c.Source,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, intelligence.ErrIntelligenceNotFound
	}
	c.APIKeyConfigured = c.APIKeyID > 0
	return c, err
}

// ListConfigs returns up to two hundred configurations in group display order.
func (r *intelligenceRepository) ListConfigs(ctx context.Context) ([]intelligence.IntelligenceConfig, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+intelligenceConfigColumns+` FROM intelligence_test_configs c JOIN groups g ON g.id=c.group_id WHERE c.source='site' ORDER BY g.sort_order,g.id,c.id LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]intelligence.IntelligenceConfig, 0)
	for rows.Next() {
		c, err := scanIntelligenceConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetConfig reads one configuration together with its current group name.
func (r *intelligenceRepository) GetConfig(ctx context.Context, id int64) (*intelligence.IntelligenceConfig, error) {
	return scanIntelligenceConfig(r.db.QueryRowContext(ctx, `SELECT `+intelligenceConfigColumns+` FROM intelligence_test_configs c JOIN groups g ON g.id=c.group_id WHERE c.id=$1`, id))
}

// SaveConfig persists a configuration and fills its generated identity and
// timestamps. Existing configurations are locked and checked for active jobs
// before their credentials or target can change. Creation obtains a
// transaction advisory lock before counting records, keeping concurrent
// inserts within the same two-hundred-record limit used by listing.
//
// The transaction rolls back if validation or SQL fails. Uniqueness violations
// become module conflicts, and successful commit publishes the complete
// configuration with its next scheduled time.
func (r *intelligenceRepository) SaveConfig(ctx context.Context, c *intelligence.IntelligenceConfig) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Roll back failed configuration writes before releasing the lock, keeping
	// saved credentials and next scheduled submission time consistent.
	defer func() { _ = tx.Rollback() }()
	if c.ID > 0 {
		if err = lockIntelligenceConfig(ctx, tx, c.ID); err != nil {
			return err
		}
		if err = checkIntelligenceIdle(ctx, tx, c.ID); err != nil {
			return err
		}
	}
	args := []any{c.GroupID, c.Model, c.Benchmark, c.BaseURL, c.APIKeyID, c.Protocol, c.ReasoningEffort, c.ServiceTier, c.Enabled, c.ScheduleEnabled, c.IntervalMinutes, c.Prompt}
	if c.ID == 0 {
		// Serialize counting with insertion so concurrent administrators cannot create
		// chargeable configurations beyond the two-hundred-item limit exposed by
		// listing.
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(29420261003)`); err != nil {
			return err
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM intelligence_test_configs WHERE source='site'`).Scan(&count); err != nil {
			return err
		}
		if count >= 200 {
			return apperror.Conflict("INTELLIGENCE_CONFIG_LIMIT", "最多支持200条检测配置")
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO intelligence_test_configs(group_id,model,benchmark,base_url,api_key_id,protocol,reasoning_effort,service_tier,enabled,schedule_enabled,interval_minutes,prompt,next_run_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,CASE WHEN $10 THEN NOW()+($11::integer * INTERVAL '1 minute') ELSE NULL END)
RETURNING id,created_at,updated_at,next_run_at`, args...).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt, &c.NextRunAt)
	} else {
		args = append(args, c.ID)
		err = tx.QueryRowContext(ctx, `UPDATE intelligence_test_configs
SET group_id=$1,model=$2,benchmark=$3,base_url=$4,api_key_id=$5,protocol=$6,reasoning_effort=$7,service_tier=$8,enabled=$9,schedule_enabled=$10,interval_minutes=$11,prompt=$12,next_run_at=CASE WHEN $10 THEN NOW()+($11::integer * INTERVAL '1 minute') ELSE NULL END,updated_at=NOW()
WHERE id=$13
RETURNING created_at,updated_at,next_run_at`, args...).Scan(&c.CreatedAt, &c.UpdatedAt, &c.NextRunAt)
	}
	if err != nil {
		return intelligenceDBError(err)
	}
	return tx.Commit()
}

// lockIntelligenceConfig locks an existing configuration until its caller's
// transaction ends. Missing IDs become the module not-found error; callers
// perform the idle check and subsequent mutation on the same transaction.
func lockIntelligenceConfig(ctx context.Context, tx *sql.Tx, id int64) error {
	var got int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM intelligence_test_configs WHERE id=$1 FOR UPDATE`, id).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return intelligence.ErrIntelligenceNotFound
	}
	return err
}

// checkIntelligenceIdle rejects configurations with queued, submitting, or
// running jobs. Callers hold the configuration row lock so job creation cannot
// race the subsequent configuration update or deletion.
func checkIntelligenceIdle(ctx context.Context, tx *sql.Tx, id int64) error {
	var busy bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM intelligence_test_runs WHERE config_id=$1 AND status IN ('queued','submitting','running'))`, id).Scan(&busy)
	if err != nil {
		return err
	}
	if busy {
		return intelligence.ErrIntelligenceBusy
	}
	return nil
}

// intelligenceDBError maps unique-constraint violations to a client-visible
// configuration conflict. Other errors retain their original identity so
// ordinary persistence failures reach the caller's existing error handling.
func intelligenceDBError(err error) error {
	var p *pq.Error
	if errors.As(err, &p) && p.Code == "23505" {
		return apperror.Conflict("INTELLIGENCE_DUPLICATE", "该分组和模型已有同类型检测配置，或正在检测")
	}
	return err
}

// DeleteConfig removes one configuration after locking its row and confirming
// it is idle. Both checks and deletion commit together; SQL failures roll back
// and retain the configuration and its history.
func (r *intelligenceRepository) DeleteConfig(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Keep the configuration and its dependent history together when deletion
	// fails; rollback also releases the row lock protecting its idle check.
	defer func() { _ = tx.Rollback() }()
	if err = lockIntelligenceConfig(ctx, tx, id); err != nil {
		return err
	}
	if err = checkIntelligenceIdle(ctx, tx, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM intelligence_test_configs WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// insertIntelligenceRun inserts a queued job and advances its configuration's
// next scheduled time. The caller holds the configuration lock and commits
// both changes in the supplied transaction. The generated local ID is also
// used as the remote idempotency key once workers claim the job.
//
// The job snapshots group, model and benchmark identity into its record,
// allowing later result reads to describe the actual submitted configuration
// and validate current access against that identity.
func insertIntelligenceRun(ctx context.Context, tx *sql.Tx, c *intelligence.IntelligenceConfig, now time.Time) (*intelligence.IntelligenceRun, error) {
	run := &intelligence.IntelligenceRun{ID: "iq_" + uuid.NewString(), ConfigID: c.ID, GroupID: c.GroupID, Model: c.Model, Benchmark: c.Benchmark, Status: "queued", Verdict: "pending", CreatedAt: now, UpdatedAt: now}
	raw, payload, err := marshalIntelligenceRun(run)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO intelligence_test_runs(id,config_id,status,record,payload,next_poll_at,created_at,updated_at) VALUES($1,$2,'queued',$3::jsonb,$4::jsonb,$5,$5,$5)`, run.ID, c.ID, string(raw), string(payload), now)
	if err != nil {
		return nil, intelligenceDBError(err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE intelligence_test_configs SET next_run_at=$2::timestamptz+(interval_minutes * INTERVAL '1 minute') WHERE id=$1`, c.ID, now)
	return run, err
}

// CreateRun atomically creates a manual job for an enabled, active group
// configuration. It locks the configuration before checking for existing
// active jobs and inserting the queued record. The same transaction advances
// the next scheduled time, preventing an immediate scheduled duplicate after a
// manual request.
//
// Missing or deleted groups cannot enter the queue. Commit failures return an
// error before the job is reported to the administrator; ordinary rollback
// releases the held configuration lock.
func (r *intelligenceRepository) CreateRun(ctx context.Context, id int64, now time.Time) (*intelligence.IntelligenceRun, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Roll back job insertion and schedule advancement together when either
	// fails, allowing a later manual submission to claim the same configuration.
	defer func() { _ = tx.Rollback() }()
	c, err := scanIntelligenceConfig(tx.QueryRowContext(ctx, `SELECT `+intelligenceConfigColumns+` FROM intelligence_test_configs c JOIN groups g ON g.id=c.group_id WHERE c.id=$1 AND g.deleted_at IS NULL AND g.status='active' FOR UPDATE OF c`, id))
	if err != nil {
		return nil, err
	}
	if !c.Enabled {
		return nil, intelligence.ErrIntelligenceDisabled
	}
	if err = checkIntelligenceIdle(ctx, tx, id); err != nil {
		return nil, err
	}
	run, err := insertIntelligenceRun(ctx, tx, c, now)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return run, nil
}

// ScheduleDue queues due enabled configurations using row locks and SKIP
// LOCKED. Limits outside one through ten use two, bounding each scheduler
// tick. Selected configurations and inserted jobs remain in one transaction so
// competing instances cannot schedule the same configuration round together.
//
// The cursor is consumed and closed before insertions use the transaction
// connection. Each insertion advances its configuration's next time, and any
// failed insertion rolls back the entire selected batch.
func (r *intelligenceRepository) ScheduleDue(ctx context.Context, now time.Time, limit int) error {
	if limit < 1 || limit > 10 {
		limit = 2
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Roll back the complete selected batch when any queued insertion fails,
	// leaving every affected configuration due for a later scheduler tick.
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT `+intelligenceConfigColumns+`
FROM intelligence_test_configs c JOIN groups g ON g.id=c.group_id
WHERE c.source='site' AND c.enabled AND c.schedule_enabled AND g.deleted_at IS NULL AND g.status='active' AND (c.next_run_at IS NULL OR c.next_run_at<=$1)
AND NOT EXISTS(SELECT 1
FROM intelligence_test_runs r
WHERE r.config_id=c.id AND r.status IN ('queued','submitting','running'))
ORDER BY c.next_run_at NULLS FIRST,c.id LIMIT $2 FOR
UPDATE OF c SKIP LOCKED`, now, limit)
	if err != nil {
		return err
	}
	var configs []*intelligence.IntelligenceConfig
	for rows.Next() {
		c, e := scanIntelligenceConfig(rows)
		if e != nil {
			_ = rows.Close()
			return e
		}
		configs = append(configs, c)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, c := range configs {
		if _, err = insertIntelligenceRun(ctx, tx, c, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// marshalIntelligenceRun encodes a result into a small record and separate
// large payload. Public JSON tags exclude remote IDs and lease tokens from
// both objects, while dedicated SQL columns retain them for recovery. Local
// generation and assessment retry state are stored in the private payload. The
// summary derives artifact availability from the actual HTML content.
//
// A copied record loses question, answer, HTML, and assessment text before
// encoding, keeping the caller's result intact. Nil input returns the module
// not-found error.
func marshalIntelligenceRun(run *intelligence.IntelligenceRun) ([]byte, []byte, error) {
	if run == nil {
		return nil, nil, intelligence.ErrIntelligenceNotFound
	}
	summary := *run
	summary.HTML = ""
	summary.Question = ""
	summary.Answer = ""
	summary.AssessmentReason = ""
	summary.HasArtifact = run.HTML != ""
	record, err := json.Marshal(summary)
	if err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(intelligenceRunPayload{
		HTML:               run.HTML,
		Question:           run.Question,
		Answer:             run.Answer,
		AssessmentReason:   run.AssessmentReason,
		LocalGeneration:    run.LocalGeneration,
		AssessmentAttempts: run.AssessmentAttempts,
	})
	return record, payload, err
}

// scanIntelligenceRun decodes result JSON and restores private state from SQL
// columns. Status, remote ID and lease token come from their authoritative
// columns after decoding, including when payload JSON was combined for a
// detail or recovery query. Local generation and assessment attempts are restored
// from payload fields excluded by the public run representation.
//
// List callers select only the small record, while workers and detail readers
// request record plus payload. SQL no-rows becomes the module not-found error;
// malformed stored JSON is returned as an error.
func scanIntelligenceRun(row intelligenceScanner) (*intelligence.IntelligenceRun, error) {
	var raw []byte
	var status, remote, lease string
	if err := row.Scan(&raw, &status, &remote, &lease); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, intelligence.ErrIntelligenceNotFound
		}
		return nil, err
	}
	run := &intelligence.IntelligenceRun{}
	if err := json.Unmarshal(raw, run); err != nil {
		return nil, err
	}
	var private struct {
		LocalGeneration    bool `json:"local_generation"`    // local artwork ownership
		AssessmentAttempts int  `json:"assessment_attempts"` // HTML submission attempt count
	}
	if err := json.Unmarshal(raw, &private); err != nil {
		return nil, err
	}
	run.LocalGeneration = private.LocalGeneration
	run.AssessmentAttempts = private.AssessmentAttempts
	run.Status = status
	run.RemoteID = remote
	run.LeaseToken = lease
	return run, nil
}

// ClaimRun leases one due active job whose preceding lease is absent or
// expired. Selection and update share a single SQL statement with row locking
// and SKIP LOCKED, so competing instances claim separate work. A successful
// claim returns the complete payload and private recovery fields together with
// the supplied token.
//
// No available job returns nil with no error, allowing idle workers to wait
// for their next tick. The caller supplies the observation time and lease
// expiry.
func (r *intelligenceRepository) ClaimRun(ctx context.Context, now, until time.Time, token string) (*intelligence.IntelligenceRun, error) {
	run, err := scanIntelligenceRun(r.db.QueryRowContext(ctx, `WITH due AS(SELECT id
FROM intelligence_test_runs
WHERE status IN ('queued','submitting','running') AND next_poll_at<=$1 AND (lease_until IS NULL OR lease_until<=$1)
ORDER BY next_poll_at,created_at LIMIT 1 FOR
UPDATE SKIP LOCKED)
UPDATE intelligence_test_runs r
SET lease_token=$3,lease_until=$2
FROM due
WHERE r.id=due.id
RETURNING (r.record||r.payload),r.status,r.remote_id,r.lease_token`, now, until, token))
	// An empty eligible queue is an idle worker result, allowing the next
	// scheduled tick to claim work without reporting a missing business record.
	if errors.Is(err, intelligence.ErrIntelligenceNotFound) {
		return nil, nil
	}
	return run, err
}

// SaveRun conditionally persists a claimed observation and applies bounded
// history retention. Both the token and an unexpired lease must match before
// the update can succeed; stale workers receive false and cannot prune another
// worker's data. keepLease retains ownership while a durable submitting state
// precedes remote creation.
//
// Releasing a lease and trimming older payloads occur in one transaction. Ten
// newest artifacts retain HTML and sixty newest runs retain history; active
// runs survive historical pruning. A successful commit returns true.
func (r *intelligenceRepository) SaveRun(ctx context.Context, run *intelligence.IntelligenceRun, next time.Time, keepLease bool) (bool, error) {
	raw, payload, err := marshalIntelligenceRun(run)
	if err != nil {
		return false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	// Roll back the observation with its retention changes on failure so
	// artifact flags and stored HTML remain consistent with the saved history.
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE intelligence_test_runs
SET status=$3,remote_id=$4,record=$5::jsonb||CASE WHEN record->>'has_thumbnail'='true' THEN '{"has_thumbnail":true}'::jsonb ELSE '{}'::jsonb END,payload=$6::jsonb||CASE WHEN payload ? 'thumbnail' THEN jsonb_build_object('thumbnail',payload->'thumbnail') ELSE '{}'::jsonb END,next_poll_at=$7,updated_at=NOW(),lease_until=CASE WHEN $8 THEN lease_until ELSE NULL END,lease_token=CASE WHEN $8 THEN lease_token ELSE '' END
WHERE id=$1 AND lease_token=$2 AND lease_until>NOW()`, run.ID, run.LeaseToken, run.Status, run.RemoteID, string(raw), string(payload), next, keepLease)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	if !keepLease {
		// Prune older HTML and terminal history in the same transaction as this
		// observation so display metadata matches retained payloads. Screenshot bytes
		// and their availability flag survive HTML pruning for older history nodes.
		if _, err = tx.ExecContext(ctx, `UPDATE intelligence_test_runs
SET payload=payload-'html',record=record||'{"has_artifact":false}'::jsonb
WHERE id IN(SELECT id
FROM intelligence_test_runs
WHERE config_id=$1 AND record->>'has_artifact'='true'
ORDER BY created_at DESC,id DESC OFFSET 10)`, run.ConfigID); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM intelligence_test_runs WHERE config_id=$1 AND status NOT IN ('queued','submitting','running') AND id IN(SELECT id FROM intelligence_test_runs WHERE config_id=$1 ORDER BY created_at DESC,id DESC OFFSET 60)`, run.ConfigID); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// GetRun returns the full stored payload and private worker recovery fields.
func (r *intelligenceRepository) GetRun(ctx context.Context, id string) (*intelligence.IntelligenceRun, error) {
	return scanIntelligenceRun(r.db.QueryRowContext(ctx, `SELECT (record||payload),status,remote_id,lease_token FROM intelligence_test_runs WHERE id=$1`, id))
}

// GetThumbnail returns the saved PNG decoded from the private payload field.
// An existing observation with no screenshot returns nil, while a deleted run
// returns the module not-found error used by authorized detail reads.
func (r *intelligenceRepository) GetThumbnail(ctx context.Context, id string) ([]byte, error) {
	var encoded sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT payload->>'thumbnail' FROM intelligence_test_runs WHERE id=$1`, id).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, intelligence.ErrIntelligenceNotFound
	}
	if err != nil || !encoded.Valid {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(encoded.String)
}

// SaveThumbnail stores a captured PNG beside its retained drawing document.
// The update keeps worker state and measurement fields intact. Empty or oversized
// PNGs are rejected, and a removed document prevents storing a newly rendered frame.
func (r *intelligenceRepository) SaveThumbnail(ctx context.Context, id string, png []byte) error {
	if len(png) == 0 || len(png) > 2<<20 {
		return apperror.BadRequest("INTELLIGENCE_THUMBNAIL_INVALID", "作品截图大小无效")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE intelligence_test_runs SET payload=payload||jsonb_build_object('thumbnail',$2::text),record=record||'{"has_thumbnail":true}'::jsonb WHERE id=$1 AND record->>'has_artifact'='true'`, id, base64.StdEncoding.EncodeToString(png))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return intelligence.ErrIntelligenceNotFound
	}
	return err
}

// ListRuns returns the sixty newest lightweight summaries for one
// configuration.
func (r *intelligenceRepository) ListRuns(ctx context.Context, id int64) ([]intelligence.IntelligenceRun, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT record,status,remote_id,lease_token FROM intelligence_test_runs WHERE config_id=$1 ORDER BY created_at DESC,id DESC LIMIT 60`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]intelligence.IntelligenceRun, 0)
	for rows.Next() {
		run, err := scanIntelligenceRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}
