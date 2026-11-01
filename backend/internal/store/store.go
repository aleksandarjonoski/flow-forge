// Package store is FlowForge's persistence: the flow library and the run
// history, in one SQLite file in the data directory. See docs/server-v1.md §3.
//
// The driver is modernc.org/sqlite, which is pure Go, so the binary builds
// with CGO_ENABLED=0 and cross-compiles.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

const (
	// DBFile is the database file name inside the data directory.
	DBFile = "flowforge.db"
	// LockFile guards the data directory: one server per directory.
	LockFile = "flowforge.lock"
)

var (
	// ErrNotFound is returned when a flow or execution does not exist.
	ErrNotFound = errors.New("not found")
	// ErrExists is returned when creating a flow whose ID is taken.
	ErrExists = errors.New("already exists")
	// ErrConflict is returned when saving a flow whose stored revision is not
	// the one the caller loaded (the lost-update guard, server-v1.md §6.2).
	ErrConflict = errors.New("revision conflict")
	// ErrLocked is returned by Open when another process holds the data
	// directory.
	ErrLocked = errors.New("data directory is in use by another FlowForge process")
	// ErrTooNew is returned by Open when the database was migrated by a newer
	// FlowForge than this one.
	ErrTooNew = errors.New("database was created by a newer FlowForge")
)

// Store is an open data directory. Safe for concurrent use.
type Store struct {
	db   *sql.DB
	lock *os.File
	// now is the clock; tests replace it.
	now func() time.Time
}

// Open opens the data directory dir, creating it (mode 0700) and the
// database if missing, takes the directory lock and applies pending
// migrations.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	lock, err := lockDir(filepath.Join(dir, LockFile))
	if err != nil {
		return nil, err
	}

	// A URI path must be absolute: a relative one would be read as the
	// authority ("file://data/…"). On Windows it becomes /C:/….
	abs, err := filepath.Abs(filepath.Join(dir, DBFile))
	if err != nil {
		lock.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	path := filepath.ToSlash(abs)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	dsn := (&url.URL{
		Scheme: "file",
		Path:   path,
		RawQuery: url.Values{"_pragma": {
			"foreign_keys(1)",
			"busy_timeout(5000)",
			"journal_mode(WAL)",
		}}.Encode(),
	}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		lock.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One connection: writes never contend, and the pragmas above apply to
	// every statement (server-v1.md §3.1).
	db.SetMaxOpenConns(1)

	s := &Store{db: db, lock: lock, now: time.Now}
	if err := migrate(context.Background(), db, migrations); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database and releases the directory lock.
func (s *Store) Close() error {
	return errors.Join(s.db.Close(), s.lock.Close())
}

// timeLayout is how timestamps are stored: RFC 3339 in UTC with
// milliseconds, so text order is time order.
const timeLayout = "2006-01-02T15:04:05.000Z"

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(timeLayout, s)
}

// stamp returns the current time, rounded to what is stored.
func (s *Store) stamp() time.Time {
	return s.now().UTC().Truncate(time.Millisecond)
}
