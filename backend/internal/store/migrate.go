package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrations is the embedded migration set.
var migrations = mustLoadMigrations(migrationFiles)

// migration is one numbered SQL file: migrations/NNNN_name.sql.
type migration struct {
	version int
	name    string
	sql     string
}

func mustLoadMigrations(fsys fs.FS) []migration {
	ms, err := loadMigrations(fsys)
	if err != nil {
		panic(err)
	}
	return ms
}

// loadMigrations reads migrations/*.sql, sorted by version. Versions must
// start at 1 and have no gaps.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	names, err := fs.Glob(fsys, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, name := range names {
		base := path.Base(name)
		num, _, ok := strings.Cut(base, "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v < 1 {
			return nil, fmt.Errorf("migration %s: name must be NNNN_name.sql", base)
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: v, name: base, sql: string(body)})
	}
	slices.SortFunc(ms, func(a, b migration) int { return a.version - b.version })
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration %s: expected version %d", m.name, i+1)
		}
	}
	return ms, nil
}

// migrate brings db up to the newest of ms. The applied version is SQLite's
// user_version; each migration runs in its own transaction together with the
// version bump. A database newer than ms is refused (server-v1.md §3.3).
func migrate(ctx context.Context, db *sql.DB, ms []migration) error {
	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current > len(ms) {
		return fmt.Errorf("%w (schema version %d, this binary knows %d)", ErrTooNew, current, len(ms))
	}
	for _, m := range ms[current:] {
		if err := apply(ctx, db, m); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	return nil
}

func apply(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	// PRAGMA does not take parameters; the version is an int we control.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
		return err
	}
	return tx.Commit()
}
