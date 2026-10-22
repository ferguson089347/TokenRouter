//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"
	"testing/fstest"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/migrations"
)

// TestUniversalAPIKeyRoutingMigration upgrades single, multi-channel, unbound and
// managed credentials through the production runner. Stored channel order survives,
// managed groups remain fixed, and inheritance changes publish cache invalidation.
func TestUniversalAPIKeyRoutingMigration(t *testing.T) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("universal_keys"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(ctx)) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	// Close the pool before terminating the container so every connection finishes
	// while PostgreSQL remains available to the test's deferred cleanup operations.
	defer func() { require.NoError(t, db.Close()) }()
	before := fstest.MapFS{}
	upgrade := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.Name() > "308_universal_api_key_routing.sql" {
			continue
		}
		data, err := migrations.FS.ReadFile(entry.Name())
		require.NoError(t, err)
		upgrade[entry.Name()] = &fstest.MapFile{Data: data}
		if entry.Name() == "308_universal_api_key_routing.sql" {
			continue
		}
		before[entry.Name()] = &fstest.MapFile{Data: data}
	}
	require.NoError(t, postgres.ApplyMigrations(ctx, db, before))
	_, err = db.ExecContext(ctx, `INSERT INTO users(id,email,password_hash) VALUES(901,'universal@example.test','fixture');
 INSERT INTO groups(id,name) VALUES(901,'first'),(902,'second');
 INSERT INTO api_keys(id,user_id,key,name,group_id,is_composite,managed_by) VALUES
 (901,901,'universal-single','single',901,false,NULL),
 (902,901,'universal-multi','multi',NULL,true,NULL),
 (903,901,'universal-unbound','unbound',NULL,false,NULL),
 (904,901,'universal-managed','managed',902,false,'creative_studio');
 INSERT INTO api_key_composite_groups(api_key_id,group_id,prefix,normalized_prefix,sort_order) VALUES
 (902,902,'provider','provider',0),(902,901,'group_902','group_902',1);`)
	require.NoError(t, err)
	require.NoError(t, postgres.ApplyMigrations(ctx, db, upgrade))
	require.NoError(t, postgres.ApplyMigrations(ctx, db, upgrade))
	for _, tc := range []struct {
		id      int64         // Stored credential identifier.
		inherit bool          // Expected default inheritance.
		multi   bool          // Expected channel-list routing.
		group   sql.NullInt64 // Expected fixed managed group.
	}{{901, true, true, sql.NullInt64{}}, {902, true, true, sql.NullInt64{}}, {903, true, true, sql.NullInt64{}}, {904, false, false, sql.NullInt64{Int64: 902, Valid: true}}} {
		var inherit, multi bool
		var group sql.NullInt64
		require.NoError(t, db.QueryRowContext(ctx, `SELECT use_default_groups,is_composite,group_id FROM api_keys WHERE id=$1`, tc.id).Scan(&inherit, &multi, &group))
		require.Equal(t, tc.inherit, inherit)
		require.Equal(t, tc.multi, multi)
		require.Equal(t, tc.group, group)
	}
	var order string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT string_agg(group_id::text,',' ORDER BY sort_order,id) FROM api_key_composite_groups WHERE api_key_id=902`).Scan(&order))
	require.Equal(t, "902,901", order)
	var single int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT group_id FROM api_key_composite_groups WHERE api_key_id=901`).Scan(&single))
	require.Equal(t, int64(901), single)
	// Clearing the outbox separates upgrade notifications from the tested mode
	// switch, which changes neither credential identity nor existing binding rows.
	_, err = db.ExecContext(ctx, `DELETE FROM auth_cache_invalidation_outbox;
 UPDATE api_keys SET use_default_groups=false WHERE id=901;`)
	require.NoError(t, err)
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_cache_invalidation_outbox`).Scan(&count))
	require.Equal(t, 1, count)
}
