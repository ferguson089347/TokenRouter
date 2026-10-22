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

// TestExplicitAPIKeyRoutingMigration restores converted fixed groups and retains
// composite prefixes after administrator defaults are retired. It also checks
// empty and managed credentials, later-created keys, and unchanged spending fields.
func TestExplicitAPIKeyRoutingMigration(t *testing.T) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("explicit_keys"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(ctx)) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	// Close database connections before container termination so cleanup can
	// complete its protocol exchanges while the PostgreSQL server is still available.
	defer func() { require.NoError(t, db.Close()) }()
	before := fstest.MapFS{}
	converted := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.Name() > "308_universal_api_key_routing.sql" {
			continue
		}
		data, err := migrations.FS.ReadFile(entry.Name())
		require.NoError(t, err)
		converted[entry.Name()] = &fstest.MapFile{Data: data}
		if entry.Name() != "308_universal_api_key_routing.sql" {
			before[entry.Name()] = &fstest.MapFile{Data: data}
		}
	}
	require.NoError(t, postgres.ApplyMigrations(ctx, db, before))
	_, err = db.ExecContext(ctx, `
INSERT INTO users(id,email,password_hash,balance) VALUES(901,'explicit@example.test','fixture',100);
INSERT INTO groups(id,name) VALUES(901,'first'),(902,'second');
INSERT INTO api_keys(id,user_id,key,name,group_id,is_composite,managed_by,quota,quota_used) VALUES
 (901,901,'explicit-single','single',901,false,NULL,100,12),
 (902,901,'explicit-multi','multi',NULL,true,NULL,100,12),
 (903,901,'explicit-empty','empty',NULL,false,NULL,100,12),
 (904,901,'explicit-managed','managed',902,false,'creative_studio',100,12);
INSERT INTO api_key_composite_groups(api_key_id,group_id,prefix,normalized_prefix,sort_order) VALUES
 (902,902,'GPT','gpt',0),(902,901,'Claude','claude',1);`)
	require.NoError(t, err)
	require.NoError(t, postgres.ApplyMigrations(ctx, db, converted))
	_, err = db.ExecContext(ctx, `
INSERT INTO api_keys(id,user_id,key,name,group_id,is_composite,use_default_groups,quota,quota_used) VALUES
 (905,901,'explicit-later-multi','later multi',NULL,true,true,100,12),
 (906,901,'explicit-later-single','later single',NULL,true,true,100,12),
 (907,901,'explicit-selected','selected',902,false,false,100,12);
INSERT INTO api_key_composite_groups(api_key_id,group_id,prefix,normalized_prefix,sort_order) VALUES
 (905,901,'901','901',0),(905,902,'902','902',1),(906,902,'902','902',0);
INSERT INTO settings(key,value) VALUES('api_key_routing_cooldown_seconds','60');`)
	require.NoError(t, err)
	require.NoError(t, postgres.ApplyMigrations(ctx, db, migrations.FS))
	require.NoError(t, postgres.ApplyMigrations(ctx, db, migrations.FS))
	for _, tc := range []struct {
		id    int64         // Persisted credential identifier.
		multi bool          // Expected composite mode.
		group sql.NullInt64 // Expected fixed group.
	}{{901, false, sql.NullInt64{Int64: 901, Valid: true}}, {902, true, sql.NullInt64{}}, {903, false, sql.NullInt64{}}, {904, false, sql.NullInt64{Int64: 902, Valid: true}}, {905, true, sql.NullInt64{}}, {906, false, sql.NullInt64{Int64: 902, Valid: true}}, {907, false, sql.NullInt64{Int64: 902, Valid: true}}} {
		var multi bool
		var group sql.NullInt64
		var quota, used float64
		require.NoError(t, db.QueryRowContext(ctx, `SELECT is_composite,group_id,quota,quota_used FROM api_keys WHERE id=$1`, tc.id).Scan(&multi, &group, &quota, &used))
		require.Equal(t, tc.multi, multi)
		require.Equal(t, tc.group, group)
		require.Equal(t, float64(100), quota)
		require.Equal(t, float64(12), used)
	}
	var prefixes string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT string_agg(prefix,',' ORDER BY sort_order,id) FROM api_key_composite_groups WHERE api_key_id=902`).Scan(&prefixes))
	require.Equal(t, "GPT,Claude", prefixes)
	var remaining int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM api_key_composite_groups WHERE api_key_id IN(901,906)`).Scan(&remaining))
	require.Zero(t, remaining)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM settings WHERE key IN('api_key_default_groups','api_key_routing_cooldown_seconds')`).Scan(&remaining))
	require.Zero(t, remaining)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name='api_keys' AND column_name='use_default_groups'`).Scan(&remaining))
	require.Zero(t, remaining)
	var balance float64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT balance FROM users WHERE id=901`).Scan(&balance))
	require.Equal(t, float64(100), balance)
}
