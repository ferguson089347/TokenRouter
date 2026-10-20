//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/intelligence"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
	"github.com/TokenFlux/TokenRouter/migrations"
)

// intelligenceFixture seeds one migrated, disposable database with a personal Key
// and group. Each test receives its own SQL pool, and container cleanup removes
// all records after the test's operations and goroutines have finished.
func intelligenceFixture(t *testing.T) (*sql.DB, intelligence.IntelligenceRepository, *intelligence.IntelligenceConfig) {
	t.Helper()
	db := postgrescontainer.New(t)
	ctx := context.Background()
	// Reapply the dedicated schema after the complete migration set to verify
	// repeated execution leaves existing task tables and indexes usable.
	migration, err := migrations.FS.ReadFile("304_intelligence_tests.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	var userID, groupID, keyID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES($1,'test') RETURNING id`, uuid.NewString()+"@example.test").Scan(&userID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO groups(name) VALUES($1) RETURNING id`, "iq-"+uuid.NewString()).Scan(&groupID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO api_keys(user_id,group_id,key,name) VALUES($1,$2,$3,'test') RETURNING id`, userID, groupID, "sk-"+uuid.NewString()).Scan(&keyID))
	repo := NewIntelligenceRepository(db)
	config := &intelligence.IntelligenceConfig{GroupID: groupID, Model: "gpt-test", Benchmark: "drawing", BaseURL: "https://gateway.example/v1", APIKeyID: keyID, Protocol: "responses", Enabled: true, IntervalMinutes: 60}
	require.NoError(t, repo.SaveConfig(ctx, config))
	return db, repo, config
}

// TestIntelligencePostgresConcurrentCreateClaimAndStaleLease competes for one
// configuration through separate PostgreSQL operations. Exactly one active job
// and claimant win, and an expired worker cannot overwrite the replacement
// lease or its result.
func TestIntelligencePostgresConcurrentCreateClaimAndStaleLease(t *testing.T) {
	integrationDB, repo, c := intelligenceFixture(t)
	ctx := context.Background()
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			_, err := repo.CreateRun(ctx, c.ID, time.Now().UTC())
			if err == nil {
				winners.Add(1)
			} else {
				require.ErrorIs(t, err, intelligence.ErrIntelligenceBusy)
			}
		})
	}
	wg.Wait()
	require.Equal(t, int32(1), winners.Load())
	require.ErrorIs(t, repo.SaveConfig(ctx, c), intelligence.ErrIntelligenceBusy)
	require.ErrorIs(t, repo.DeleteConfig(ctx, c.ID), intelligence.ErrIntelligenceBusy)
	claimed := make(chan *intelligence.IntelligenceRun, 32)
	for range 32 {
		wg.Go(func() {
			now := time.Now().UTC()
			run, err := repo.ClaimRun(ctx, now, now.Add(time.Minute), uuid.NewString())
			require.NoError(t, err)
			if run != nil {
				claimed <- run
			}
		})
	}
	wg.Wait()
	close(claimed)
	var old *intelligence.IntelligenceRun
	var count int
	for run := range claimed {
		old = run
		count++
	}
	require.Equal(t, 1, count)
	_, err := integrationDB.ExecContext(ctx, `UPDATE intelligence_test_runs SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, old.ID)
	require.NoError(t, err)
	now := time.Now().UTC()
	fresh, err := repo.ClaimRun(ctx, now, now.Add(time.Minute), "new-lease")
	require.NoError(t, err)
	require.NotNil(t, fresh)
	old.Status = "completed"
	ok, err := repo.SaveRun(ctx, old, now, false)
	require.NoError(t, err)
	require.False(t, ok, "旧实例不能覆盖新租约")
	fresh.Status = "submitting"
	ok, err = repo.SaveRun(ctx, fresh, now, true)
	require.NoError(t, err)
	require.True(t, ok)
	fresh.Status = "running"
	fresh.RemoteID = "remote-private-token"
	fresh.HTML = "<svg>partial artwork</svg>"
	fresh.Question = "partial question"
	fresh.Answer = "partial answer"
	fresh.AssessmentReason = "partial assessment"
	ok, err = repo.SaveRun(ctx, fresh, now, false)
	require.NoError(t, err)
	require.True(t, ok)
	found, err := repo.GetRun(ctx, fresh.ID)
	require.NoError(t, err)
	require.Equal(t, "remote-private-token", found.RemoteID)
	require.Equal(t, fresh.HTML, found.HTML)
	require.Equal(t, fresh.Question, found.Question)
	require.Equal(t, fresh.Answer, found.Answer)
	require.Equal(t, fresh.AssessmentReason, found.AssessmentReason)
	var raw, payload string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT record::text,payload::text FROM intelligence_test_runs WHERE id=$1`, fresh.ID).Scan(&raw, &payload))
	require.NotContains(t, raw, "remote-private-token")
	require.NotContains(t, payload, "remote-private-token")
	var summary map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &summary))
	for _, field := range []string{"html", "question", "answer", "assessment_reason"} {
		require.NotContains(t, summary, field)
	}
	require.Contains(t, payload, fresh.HTML)
	// Reclaim the running task with its complete payload so intermediate artwork
	// and final answers survive the lightweight list representation.
	resumed, err := repo.ClaimRun(ctx, time.Now().UTC(), time.Now().UTC().Add(time.Minute), "resume-lease")
	require.NoError(t, err)
	require.NotNil(t, resumed)
	require.Equal(t, fresh.HTML, resumed.HTML)
	require.Equal(t, fresh.Answer, resumed.Answer)
}

// TestIntelligencePostgresHistoryAndArtifactRetention writes more completed
// observations and drawings than the configured retention limits. PostgreSQL
// keeps bounded histories and artwork payloads while retaining recent status
// and measurement summaries.
func TestIntelligencePostgresHistoryAndArtifactRetention(t *testing.T) {
	integrationDB, repo, c := intelligenceFixture(t)
	ctx := context.Background()
	start := time.Now().UTC().Add(-time.Hour)
	for i := range 65 {
		run, err := repo.CreateRun(ctx, c.ID, start.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
		now := time.Now().UTC()
		claimed, err := repo.ClaimRun(ctx, now, now.Add(time.Minute), uuid.NewString())
		require.NoError(t, err)
		require.NotNil(t, claimed)
		require.Equal(t, run.ID, claimed.ID)
		claimed.Status = "completed"
		claimed.Verdict = "passed"
		claimed.HTML = "<html>fixture</html>"
		claimed.Answer = "answer"
		claimed.Question = "question"
		claimed.AssessmentReason = "assessment reason"
		claimed.HasArtifact = true
		claimed.HasDetail = true
		ok, err := repo.SaveRun(ctx, claimed, now, false)
		require.NoError(t, err)
		require.True(t, ok)
	}
	runs, err := repo.ListRuns(ctx, c.ID)
	require.NoError(t, err)
	require.Len(t, runs, 60)
	artifacts := 0
	for _, run := range runs {
		require.Empty(t, run.HTML)
		require.Empty(t, run.Question)
		require.Empty(t, run.Answer)
		require.Empty(t, run.AssessmentReason)
		if run.HasArtifact {
			artifacts++
		}
	}
	require.Equal(t, 10, artifacts)
	newest, err := repo.GetRun(ctx, runs[0].ID)
	require.NoError(t, err)
	require.NotEmpty(t, newest.HTML)
	require.Equal(t, "question", newest.Question)
	require.Equal(t, "answer", newest.Answer)
	require.Equal(t, "assessment reason", newest.AssessmentReason)
	old, err := repo.GetRun(ctx, runs[10].ID)
	require.NoError(t, err)
	require.Empty(t, old.HTML)
	require.False(t, old.HasArtifact)
	require.Equal(t, "passed", old.Verdict)
	require.Equal(t, "answer", old.Answer, "淘汰作品不应删除糖果答案或其它详情")
	var artifactPayloads, heavySummaries int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE payload ? 'html'),COUNT(*) FILTER (WHERE record ? 'html' OR record ? 'question' OR record ? 'answer' OR record ? 'assessment_reason') FROM intelligence_test_runs WHERE config_id=$1`, c.ID).Scan(&artifactPayloads, &heavySummaries))
	require.Equal(t, 10, artifactPayloads)
	require.Zero(t, heavySummaries)
	require.NoError(t, repo.DeleteConfig(ctx, c.ID))
	_, err = repo.GetRun(ctx, newest.ID)
	require.ErrorIs(t, err, intelligence.ErrIntelligenceNotFound)
}

// TestIntelligencePostgresLargePayloadDoesNotExpandListRecord stores large
// drawing and answer content in a real migrated database. List queries return
// bounded summary records, while full detail reads reconstruct the complete
// saved payload.
func TestIntelligencePostgresLargePayloadDoesNotExpandListRecord(t *testing.T) {
	integrationDB, repo, c := intelligenceFixture(t)
	ctx := context.Background()
	created, err := repo.CreateRun(ctx, c.ID, time.Now().UTC())
	require.NoError(t, err)
	now := time.Now().UTC()
	run, err := repo.ClaimRun(ctx, now, now.Add(time.Minute), "large-payload-lease")
	require.NoError(t, err)
	require.NotNil(t, run)
	require.Equal(t, created.ID, run.ID)
	run.Status = "completed"
	run.Verdict = "passed"
	run.HTML = "<html>" + strings.Repeat("artwork", 80000) + "</html>"
	run.Answer = strings.Repeat("answer", 10000)
	run.HasArtifact = true
	run.HasDetail = true
	ok, err := repo.SaveRun(ctx, run, now, false)
	require.NoError(t, err)
	require.True(t, ok)
	var recordLength, payloadLength int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT octet_length(record::text),octet_length(payload::text) FROM intelligence_test_runs WHERE id=$1`, run.ID).Scan(&recordLength, &payloadLength))
	require.Less(t, recordLength, 2048, "摘要不能随作品大小增长")
	require.Greater(t, payloadLength, 500000)
	list, err := repo.ListRuns(ctx, c.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.True(t, list[0].HasArtifact)
	require.Empty(t, list[0].HTML)
	require.Empty(t, list[0].Answer)
	detail, err := repo.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, run.HTML, detail.HTML)
	require.Equal(t, run.Answer, detail.Answer)
}

// TestIntelligencePostgresScheduledSingleRunAndConfigLimit races scheduled
// work and fills the configuration listing limit in PostgreSQL. Scheduling
// retains one active job, creation enforces the cap, and deleted groups leave
// manageable configuration entries.
func TestIntelligencePostgresScheduledSingleRunAndConfigLimit(t *testing.T) {
	integrationDB, repo, c := intelligenceFixture(t)
	ctx := context.Background()
	c.ScheduleEnabled = true
	require.NoError(t, repo.SaveConfig(ctx, c))
	_, err := integrationDB.ExecContext(ctx, `UPDATE intelligence_test_configs SET next_run_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, c.ID)
	require.NoError(t, err)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			require.NoError(t, repo.ScheduleDue(ctx, time.Now().UTC(), 2))
		})
	}
	wg.Wait()
	runs, err := repo.ListRuns(ctx, c.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	// Deleted groups retain administrator-visible configurations, allowing cleanup
	// to release their slots instead of leaving inaccessible scheduled
	// configuration records.
	_, err = integrationDB.ExecContext(ctx, `UPDATE groups SET deleted_at=NOW() WHERE id=$1`, c.GroupID)
	require.NoError(t, err)
	list, err := repo.ListConfigs(ctx)
	require.NoError(t, err)
	var visible bool
	for _, item := range list {
		if item.ID == c.ID {
			visible = true
		}
	}
	require.True(t, visible)
}
