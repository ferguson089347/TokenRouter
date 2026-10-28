//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/ops/monitor"
)

// TestChannelMonitorPassiveAggregation creates eligible and excluded streaming
// usage plus repeated errors in migrated PostgreSQL. It protects sample rules,
// saved platform ownership, timing extraction, error deduplication, permissions,
// and idempotent minute and fixed-rollup rebuilding through production queries.
func TestChannelMonitorPassiveAggregation(t *testing.T) {
	ctx := t.Context()
	id := uuid.NewString()
	user, err := integrationEntClient.User.Create().SetEmail(id + "@monitor.example.com").SetPasswordHash("fixture").Save(ctx)
	require.NoError(t, err)
	key, err := integrationEntClient.APIKey.Create().SetUserID(user.ID).SetKey(id).SetName("monitor").Save(ctx)
	require.NoError(t, err)
	provider, err := integrationEntClient.Provider.Create().SetName(id).SetPlatform("openai").SetType("apikey").SetCredentials(map[string]any{}).Save(ctx)
	require.NoError(t, err)
	group, err := integrationEntClient.Group.Create().SetName(id).Save(ctx)
	require.NoError(t, err)
	// Remove committed fixture rows before their foreign-key owners. Monitor
	// tables retain only this test's group, and the disposable suite owns storage.
	t.Cleanup(func() {
		for _, table := range []string{"channel_monitor_v2_metrics_1m", "channel_monitor_v2_user_metrics_1m", "channel_monitor_v2_error_metrics_1m", "channel_monitor_v2_latency_histograms_1m", "channel_monitor_v2_metrics_rollup", "channel_monitor_v2_user_metrics_rollup", "channel_monitor_v2_error_metrics_rollup", "channel_monitor_v2_latency_histograms_rollup", "usage_logs", "ops_error_logs"} {
			_, cleanupErr := integrationDB.Exec("DELETE FROM "+table+" WHERE group_id=$1", group.ID)
			require.NoError(t, cleanupErr)
		}
	})
	now := time.Now().UTC().Truncate(time.Minute)
	start := now.Add(-time.Hour).Truncate(time.Hour)
	at := start.Add(time.Minute)
	performance := timing.NewPerformance(timing.FirstOutput, 0)
	performance.Stream, performance.Complete, performance.UsageComplete = true, true, true
	performance.Values["upstream_first_output_ms"] = 100
	performance.Values["upstream_finished_ms"] = 2000
	raw, err := json.Marshal(performance)
	require.NoError(t, err)
	for _, sample := range []struct {
		input  int     // input tokens before cache
		stream bool    // request streaming eligibility flag
		cost   float64 // settled positive-cost eligibility value
	}{{11000, true, 1}, {10000, true, 1}, {11000, false, 1}, {11000, true, 0}} {
		_, err = integrationDB.ExecContext(ctx, `INSERT INTO usage_logs
 (user_id,api_key_id,provider_id,group_id,platform,request_id,model,requested_model,
 input_tokens,output_tokens,actual_cost,stream,timings,created_at)
 VALUES($1,$2,$3,$4,'openai',$5,'upstream-model','requested-model',$6,100,$7,$8,$9,$10)`,
			user.ID, key.ID, provider.ID, group.ID, uuid.NewString(), sample.input, sample.cost, sample.stream, string(raw), at)
		require.NoError(t, err)
	}
	// Two stored errors share a logical request. Only its latest observation may
	// contribute a failed request, while a local authentication error is excluded
	// from configured health scoring but remains in the categorized breakdown.
	errorID := uuid.NewString()
	for _, sample := range []struct {
		request string // logical failed request identity
		status  int    // observed upstream HTTP status
	}{{errorID, 500}, {errorID, 503}, {uuid.NewString(), 401}} {
		_, err = integrationDB.ExecContext(ctx, `INSERT INTO ops_error_logs
 (request_id,group_id,provider_id,user_id,platform,model,requested_model,error_phase,error_type,
 error_owner,status_code,upstream_status_code,error_message,stream,created_at)
 VALUES($1,$2,$3,$4,'openai','upstream-model','requested-model','upstream','api_error','provider',$5,$5,'upstream fixture',true,$6)`,
			sample.request, group.ID, provider.ID, user.ID, sample.status, at)
		require.NoError(t, err)
	}
	repo := NewChannelMonitorV2Repository(integrationDB)
	require.NoError(t, repo.RecomputeRange(ctx, start, now))
	require.NoError(t, repo.RecomputeRange(ctx, start, now))
	cfg, err := repo.GetConfig(ctx)
	require.NoError(t, err)
	updated, err := repo.UpdateConfig(ctx, *cfg, cfg.Version)
	require.NoError(t, err)
	require.Greater(t, updated.Version, cfg.Version)
	_, err = repo.UpdateConfig(ctx, *cfg, cfg.Version)
	require.ErrorIs(t, err, monitor.ErrChannelMonitorV2ConfigConflict)
	filter := monitor.ChannelMonitorV2Filter{Range: "90m", GroupIDs: []int64{group.ID}, Start: start, End: now, Bucket: 5 * time.Minute}
	result, err := repo.GetSnapshot(ctx, filter, *cfg, true)
	require.NoError(t, err)
	require.EqualValues(t, 1, result.Metrics.SuccessRequests)
	require.EqualValues(t, 2, result.Metrics.ErrorRequests)
	require.EqualValues(t, 3, result.Metrics.RequestCount)
	require.InDelta(t, 1.0/3.0, result.Metrics.ErrorRate, 0.000001)
	require.EqualValues(t, 1, result.Metrics.TTFT.SampleCount)
	require.Equal(t, "unknown", result.Health.Overall)
	coarse := filter
	coarse.Range = "7d"
	coarse.Bucket = 12 * time.Hour
	coarse.Start = start.Truncate(12 * time.Hour)
	coarse.End = coarse.Start.Add(12 * time.Hour)
	historical, err := repo.GetSnapshot(ctx, coarse, *cfg, true)
	require.NoError(t, err)
	require.EqualValues(t, 3, historical.Metrics.RequestCount)
	// Fail an insert after the transaction deletes old facts. Rollback must
	// restore those facts together with the watermark instead of publishing an
	// empty interval after the failed replacement.
	triggerName := fmt.Sprintf("monitor_fixture_%d", group.ID)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.group_id=%d THEN RAISE EXCEPTION 'monitor fixture rollback'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER %s BEFORE INSERT ON channel_monitor_v2_metrics_1m FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, group.ID, triggerName, triggerName))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := integrationDB.Exec("DROP TRIGGER IF EXISTS " + triggerName + " ON channel_monitor_v2_metrics_1m; DROP FUNCTION IF EXISTS " + triggerName + "()")
		require.NoError(t, cleanupErr)
	})
	require.ErrorContains(t, repo.RecomputeRange(ctx, start, now), "monitor fixture rollback")
	retained, err := repo.GetSnapshot(ctx, filter, *cfg, true)
	require.NoError(t, err)
	require.Equal(t, result.Metrics.RequestCount, retained.Metrics.RequestCount)
	_, err = integrationDB.ExecContext(ctx, "DROP TRIGGER "+triggerName+" ON channel_monitor_v2_metrics_1m; DROP FUNCTION "+triggerName+"()")
	require.NoError(t, err)
	rows, err := repo.GetMatrix(ctx, filter, *cfg, monitor.ChannelMonitorV2GroupByPlatformGroup, true)
	require.NoError(t, err)
	require.NotEmpty(t, rows.Items)
	filter.RestrictGroups = true
	filter.AllowedGroupIDs = []int64{group.ID + 100000}
	forbidden, err := repo.GetSnapshot(ctx, filter, *cfg, false)
	require.NoError(t, err)
	require.Zero(t, forbidden.Metrics.RequestCount)
	// A canceled rebuild must leave its durable cursor unchanged; retrying with
	// an active context can then resume the same previously committed coverage.
	before, err := repo.GetAggregationWatermark(ctx)
	require.NoError(t, err)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.Error(t, repo.RecomputeRange(canceled, start.Add(-time.Hour), now))
	after, err := repo.GetAggregationWatermark(ctx)
	require.NoError(t, err)
	require.Equal(t, before.BackfillCursor, after.BackfillCursor)
}
