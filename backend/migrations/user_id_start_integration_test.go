//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/migrations"
)

// user_id_start_integration_test.go checks user generator migration 305 against
// PostgreSQL rows and connection-local sequence caches. Existing identifiers and
// foreign keys remain stable while fresh allocations follow the configured floor
// and retain higher reserved sequence positions.

// TestUserIDStartMigration exercises empty, populated, advanced, and cached user
// generators. It checks transactional rollback, stable existing references, the
// next two allocated IDs, and repeated application through the migration runner.
func TestUserIDStartMigration(t *testing.T) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("user_ids"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	// Register container cleanup before schema pools so every subtest closes its
	// connections before the shared PostgreSQL fixture is terminated by the parent.
	t.Cleanup(func() { require.NoError(t, pg.Terminate(ctx)) })
	const migrationName = "305_users_id_start_at_1000.sql"
	content, err := migrations.FS.ReadFile(migrationName)
	require.NoError(t, err)
	for i, tc := range []struct {
		name   string // named generator scenario
		setup  string // initial generator SQL
		cached bool   // keep a reserved connection
		want   int64  // first new user ID
	}{
		{name: "empty", want: 1000},
		{name: "existing users", setup: "INSERT INTO users(id) VALUES(1),(4); SELECT setval('users_id_seq',4,true)", want: 1000},
		{name: "ready at floor", setup: "SELECT setval('users_id_seq',1000,false)", want: 1000},
		{name: "advanced called", setup: "SELECT setval('users_id_seq',2000,true)", want: 2001},
		{name: "advanced uncalled", setup: "SELECT setval('users_id_seq',2000,false)", want: 2000},
		{name: "manual user ahead", setup: "INSERT INTO users(id) VALUES(4000)", want: 4001},
		{name: "cached connection", cached: true, want: 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := fmt.Sprintf("user_ids_%d", i)
			dsn, err := pg.ConnectionString(ctx, "sslmode=disable", "search_path="+schema)
			require.NoError(t, err)
			db, err := sql.Open("postgres", dsn)
			require.NoError(t, err)
			// Keep each schema pool alive through its assertions, then release its
			// sessions before parent cleanup removes the PostgreSQL container.
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			_, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema+"; CREATE TABLE users(id bigserial PRIMARY KEY, marker text DEFAULT 'legacy'); CREATE TABLE user_refs(user_id bigint REFERENCES users(id))")
			require.NoError(t, err)
			if tc.setup != "" {
				_, err = db.ExecContext(ctx, tc.setup)
				require.NoError(t, err)
			}
			var reserved *sql.Conn
			if tc.cached {
				reserved, err = db.Conn(ctx)
				require.NoError(t, err)
				// Hold the allocating session across migration so its cached values
				// can be checked after another connection restarts the shared sequence.
				t.Cleanup(func() { require.NoError(t, reserved.Close()) })
				_, err = reserved.ExecContext(ctx, "ALTER SEQUENCE users_id_seq CACHE 20; INSERT INTO users DEFAULT VALUES")
				require.NoError(t, err)
			}
			_, err = db.ExecContext(ctx, "INSERT INTO user_refs SELECT id FROM users")
			require.NoError(t, err)
			var before []int64
			require.NoError(t, db.QueryRowContext(ctx, "SELECT COALESCE(array_agg(id ORDER BY id),ARRAY[]::bigint[]) FROM users").Scan(pq.Array(&before)))
			var sequenceBefore, sequenceAfter string
			require.NoError(t, db.QueryRowContext(ctx, "SELECT last_value::text || ':' || is_called::text FROM users_id_seq").Scan(&sequenceBefore))
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			// Exercise a migration rollback before the recorded application so a
			// failed startup cannot leave its generator advance committed separately.
			_, applyErr := tx.ExecContext(ctx, string(content))
			rollbackErr := tx.Rollback()
			require.NoError(t, applyErr)
			require.NoError(t, rollbackErr)
			require.NoError(t, db.QueryRowContext(ctx, "SELECT last_value::text || ':' || is_called::text FROM users_id_seq").Scan(&sequenceAfter))
			require.Equal(t, sequenceBefore, sequenceAfter)
			fs := fstest.MapFS{migrationName: &fstest.MapFile{Data: content}}
			require.NoError(t, postgres.ApplyMigrations(ctx, db, fs))
			require.NoError(t, postgres.ApplyMigrations(ctx, db, fs))
			for offset := range 2 {
				var generated int64
				if reserved != nil {
					err = reserved.QueryRowContext(ctx, "INSERT INTO users(marker) VALUES('new') RETURNING id").Scan(&generated)
				} else {
					err = db.QueryRowContext(ctx, "INSERT INTO users(marker) VALUES('new') RETURNING id").Scan(&generated)
				}
				require.NoError(t, err)
				require.Equal(t, tc.want+int64(offset), generated)
				_, err = db.ExecContext(ctx, string(content))
				require.NoError(t, err)
			}
			var after, references []int64
			require.NoError(t, db.QueryRowContext(ctx, "SELECT COALESCE(array_agg(id ORDER BY id),ARRAY[]::bigint[]) FROM users WHERE marker='legacy'").Scan(pq.Array(&after)))
			require.NoError(t, db.QueryRowContext(ctx, "SELECT COALESCE(array_agg(user_id ORDER BY user_id),ARRAY[]::bigint[]) FROM user_refs").Scan(pq.Array(&references)))
			require.Equal(t, before, after)
			require.Equal(t, before, references)
			var applied int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&applied))
			require.Equal(t, 1, applied)
		})
	}
}
