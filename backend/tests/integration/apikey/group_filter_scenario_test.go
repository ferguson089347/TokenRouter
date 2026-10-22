package apikey_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// group_filter_scenario_test.go checks console filtering against persisted Key
// bindings and soft-deleted groups. The SQLite fixture runs the Ent relationship
// queries used by list reads, including missing group views whose stored IDs
// remain available to authentication.

// TestAPIKeyRepositoryListByUserIDUngroupedIncludesDeletedGroups deletes a group
// still referenced by an ordinary Key. Ungrouped filtering includes that Key
// and an unbound Key, while excluding live bindings and composite Keys.
func TestAPIKeyRepositoryListByUserIDUngroupedIncludesDeletedGroups(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "group-filter@test.com")
	liveGroup, err := client.Group.Create().SetName("live-group").Save(ctx)
	require.NoError(t, err)
	deletedGroup, err := client.Group.Create().SetName("deleted-group").Save(ctx)
	require.NoError(t, err)

	for _, fixture := range []struct {
		name      string // persisted Key name
		groupID   *int64 // ordinary group reference
		composite bool   // composite binding mode
	}{
		{name: "unbound"},
		{name: "deleted-group", groupID: &deletedGroup.ID},
		{name: "live-group", groupID: &liveGroup.ID},
		{name: "composite", composite: true},
	} {
		key, err := client.APIKey.Create().
			SetUserID(user.ID).
			SetName(fixture.name).
			SetKey("sk-filter-" + fixture.name).
			SetNillableGroupID(fixture.groupID).
			SetIsComposite(fixture.composite).
			Save(ctx)
		require.NoError(t, err)
		if fixture.composite {
			_, err := client.APIKeyCompositeGroup.Create().
				SetAPIKeyID(key.ID).
				SetGroupID(liveGroup.ID).
				SetPrefix("live").
				SetNormalizedPrefix("live").
				Save(ctx)
			require.NoError(t, err)
		}
	}

	// Soft deletion leaves the stored binding intact while list hydration hides
	// its group, reproducing Keys displayed as ungrouped after administrator deletion.
	require.NoError(t, client.Group.DeleteOneID(deletedGroup.ID).Exec(ctx))
	ungrouped := int64(0)
	keys, page, err := repo.ListByUserID(ctx, user.ID,
		pagination.PaginationParams{Page: 1, PageSize: 10},
		apikey.APIKeyListFilters{GroupID: &ungrouped})
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Total)
	var names []string
	for _, key := range keys {
		names = append(names, key.Name)
		require.Nil(t, key.Group)
		if key.Name == "deleted-group" {
			require.Equal(t, &deletedGroup.ID, key.GroupID)
		}
	}
	require.ElementsMatch(t, []string{"unbound", "deleted-group"}, names)
}

// TestAPIKeyRepositoryInheritedGroupFilter changes resolved administrator membership
// while inherited credentials keep stale saved rows. Filtering follows current
// membership for inherited Keys and saved bindings for custom Keys.
func TestAPIKeyRepositoryInheritedGroupFilter(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "inherited-filter@example.test")
	group, err := client.Group.Create().SetName("channel").Save(ctx)
	require.NoError(t, err)
	for _, inherit := range []bool{true, false} {
		name := "custom"
		if inherit {
			name = "inherited"
		}
		key, err := client.APIKey.Create().SetUserID(user.ID).SetName(name).
			SetKey("filter-" + name).SetIsComposite(true).SetUseDefaultGroups(inherit).Save(ctx)
		require.NoError(t, err)
		_, err = client.APIKeyCompositeGroup.Create().SetAPIKeyID(key.ID).
			SetGroupID(group.ID).SetPrefix("stored").SetNormalizedPrefix("stored").Save(ctx)
		require.NoError(t, err)
	}
	for _, matches := range []bool{false, true} {
		keys, page, err := repo.ListByUserID(ctx, user.ID,
			pagination.PaginationParams{Page: 1, PageSize: 10},
			apikey.APIKeyListFilters{GroupID: &group.ID, MatchInheritedGroups: matches})
		require.NoError(t, err)
		if matches {
			require.EqualValues(t, 2, page.Total)
		} else {
			require.EqualValues(t, 1, page.Total)
			require.Equal(t, "custom", keys[0].Name)
		}
	}
}
