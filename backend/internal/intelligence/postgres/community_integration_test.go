//go:build integration

package postgres

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/migrations"
)

// TestCommunityAdmissionSeparatesSourcesAndSerializesWork applies the migration
// over an existing site identity and races community submissions. One job wins,
// site settings stay intact, and a later idle submission extends the public row.
func TestCommunityAdmissionSeparatesSourcesAndSerializesWork(t *testing.T) {
	db, repo, site := intelligenceFixture(t)
	ctx := context.Background()
	site.ServiceTier = "priority"
	site.Prompt = "system drawing prompt"
	require.NoError(t, repo.SaveConfig(ctx, site))
	migration, err := migrations.FS.ReadFile("307_intelligence_user_tests.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	var winners atomic.Int32
	var work sync.WaitGroup
	for range 12 {
		work.Go(func() {
			input := *site
			input.ID = 0
			input.Source = "user"
			_, err := repo.CreateCommunityRun(ctx, &input, time.Now().UTC())
			if err == nil {
				winners.Add(1)
			}
		})
	}
	work.Wait()
	require.EqualValues(t, 1, winners.Load())
	sites, err := repo.ListConfigs(ctx)
	require.NoError(t, err)
	require.Len(t, sites, 1)
	require.Equal(t, site.ID, sites[0].ID)
	community, err := repo.ListCommunityConfigs(ctx)
	require.NoError(t, err)
	require.Len(t, community, 1)
	require.NotEqual(t, site.ID, community[0].ID)
	require.Equal(t, "user", community[0].Source)
	require.Equal(t, site.ServiceTier, community[0].ServiceTier)
	require.Equal(t, site.Prompt, community[0].Prompt)
	claimed, err := repo.ClaimRun(ctx, time.Now().UTC(), time.Now().UTC().Add(time.Minute), uuid.NewString())
	require.NoError(t, err)
	require.NotNil(t, claimed)
	claimed.Status = "completed"
	ok, err := repo.SaveRun(ctx, claimed, time.Now().UTC(), false)
	require.NoError(t, err)
	require.True(t, ok)
	input := community[0]
	_, err = repo.CreateCommunityRun(ctx, &input, time.Now().UTC())
	require.Error(t, err, "the shared-row interval applies after a completed run")
	input.APIKeyID = site.APIKeyID + 100000
	_, err = repo.CreateCommunityRun(ctx, &input, time.Now().UTC())
	require.Equal(t, "INTELLIGENCE_SELF_TEST_BUSY", apperror.Reason(err), "changing the selected Key cannot bypass the shared-row interval")
	input.APIKeyID = site.APIKeyID
	_, err = db.ExecContext(ctx, `UPDATE intelligence_test_runs SET created_at=NOW()-INTERVAL '61 seconds' WHERE id=$1`, claimed.ID)
	require.NoError(t, err)
	input.ServiceTier = ""
	input.Prompt = "updated system drawing prompt"
	next, err := repo.CreateCommunityRun(ctx, &input, time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, community[0].ID, next.ConfigID)
	saved, err := repo.GetConfig(ctx, next.ConfigID)
	require.NoError(t, err)
	require.Empty(t, saved.ServiceTier)
	require.Equal(t, input.Prompt, saved.Prompt)
	runs, err := repo.ListRuns(ctx, next.ConfigID)
	require.NoError(t, err)
	require.Len(t, runs, 2)
}
