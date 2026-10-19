package postgres

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// repository.go stores remote pool configuration and snapshots in settings rows.
// Refresh tokens rotate during network calls, so read-modify-write callbacks hold
// a database lock until their updated credentials and complete snapshot are saved.
// Independent server instances therefore observe the last committed session before
// consuming a token. Callers bound the callback duration to leave time for commit.
//
// Missing keys are inserted inside the transaction before locking. Concurrent first
// updates converge on the same row, and all callback writes commit together.
// Callback and SQL errors roll back the transaction, while ordinary reads return
// the latest committed value without waiting for remote collection to finish.

// Repository persists remote pool records using the application's shared SQL pool.
type Repository struct {
	db *sql.DB // application owned SQL connection pool
}

// NewRepository binds remote pool persistence to the application's shared database.
// The application retains ownership of the connection pool and closes it after
// active HTTP operations have completed during shutdown.
func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

// GetValue reads the latest committed settings value for the supplied key.
// An absent row returns settings.ErrSettingNotFound so the pool can construct its
// disabled default configuration; other database failures remain errors.
func (r *Repository) GetValue(ctx context.Context, key string) (string, error) {
	var value string
	err := r.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = $1", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", settings.ErrSettingNotFound
	}
	return value, err
}

// UpdateMultiple locks settings keys, invokes change, and commits its returned
// values.
// The callback runs once after every key is locked, including initially absent keys.
// Callers keep the callback bounded because it can perform remote session exchanges.
//
// Keys are sorted before insertion and locking so concurrent updates acquire rows
// in the same order. A callback error or SQL failure rolls back all writes. The
// caller records collection failures as data when rotated credentials need saving
// despite an incomplete remote account collection.
func (r *Repository) UpdateMultiple(ctx context.Context, keys []string, change func(map[string]string) (map[string]string, error)) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Roll back every incomplete update, including callback and commit failures,
	// so partial credentials or snapshots remain invisible to readers.
	defer func() { _ = tx.Rollback() }()
	ordered := append([]string(nil), keys...)
	sort.Strings(ordered)
	values := make(map[string]string, len(keys))
	for _, key := range ordered {
		// Insert missing rows before locking so concurrent first-time
		// configurations serialize through the same unique settings key and row
		// lock.
		if _, err = tx.ExecContext(ctx, "INSERT INTO settings (key, value, updated_at) VALUES ($1, '', NOW()) ON CONFLICT (key) DO NOTHING", key); err != nil {
			return err
		}
		var value string
		if err = tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = $1 FOR UPDATE", key).Scan(&value); err != nil {
			return err
		}
		values[key] = value
	}
	next, err := change(values)
	if err != nil {
		return err
	}
	for key, value := range next {
		if _, err = tx.ExecContext(ctx, "UPDATE settings SET value = $2, updated_at = NOW() WHERE key = $1", key, value); err != nil {
			return err
		}
	}
	// Commit the session and snapshot together after the callback finishes,
	// allowing later instances to consume the newly rotated refresh token.
	return tx.Commit()
}
