package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// openTest opens a store in a fresh directory with a fixed clock that
// advances one second per reading.
func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}
	return s
}

func TestOpen_CreatesDirectoryAndSchema(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("data directory mode = %o, want 700", perm)
	}
	if _, err := os.Stat(filepath.Join(dir, DBFile)); err != nil {
		t.Errorf("database file: %v", err)
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != len(migrations) {
		t.Errorf("user_version = %d, want %d", version, len(migrations))
	}
	var fk int
	if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Error("foreign keys are off")
	}
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

func TestOpen_RelativeDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	s, err := Open("data")
	if err != nil {
		t.Fatalf("Open(relative) = %v", err)
	}
	s.Close()
	if _, err := os.Stat(filepath.Join("data", DBFile)); err != nil {
		t.Errorf("database not in the relative directory: %v", err)
	}
}

func TestOpen_ReopenKeepsData(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateFlow(context.Background(), "f", "F", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	if _, err := s.GetFlow(context.Background(), "f"); err != nil {
		t.Errorf("flow lost after reopen: %v", err)
	}
}

func TestOpen_SecondOpenIsLocked(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Open err = %v, want ErrLocked", err)
	}
	s.Close()

	// Closing releases the lock.
	s, err = Open(dir)
	if err != nil {
		t.Fatalf("Open after Close: %v", err)
	}
	s.Close()
}

func TestOpen_RefusesNewerDatabase(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	if _, err := Open(dir); !errors.Is(err, ErrTooNew) {
		t.Fatalf("Open err = %v, want ErrTooNew", err)
	}
	// The failed Open released the lock again.
	lock, err := lockDir(filepath.Join(dir, LockFile))
	if err != nil {
		t.Fatalf("lock still held after failed Open: %v", err)
	}
	lock.Close()
}

func TestLoadMigrations(t *testing.T) {
	ms, err := loadMigrations(fstest.MapFS{
		"migrations/0002_b.sql": {Data: []byte("B")},
		"migrations/0001_a.sql": {Data: []byte("A")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].sql != "A" || ms[1].sql != "B" {
		t.Errorf("migrations = %+v, want 0001 then 0002", ms)
	}

	for name, fsys := range map[string]fstest.MapFS{
		"gap":      {"migrations/0001_a.sql": {}, "migrations/0003_c.sql": {}},
		"bad name": {"migrations/init.sql": {}},
		"zero":     {"migrations/0000_a.sql": {}},
	} {
		if _, err := loadMigrations(fsys); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestMigrate_AppliesOnlyNewMigrations(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	ms := append(append([]migration{}, migrations...), migration{
		version: len(migrations) + 1,
		name:    "next.sql",
		sql:     "CREATE TABLE extra (x INTEGER)",
	})
	if err := migrate(ctx, s.db, ms); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Running again is a no-op: the table is not created twice.
	if err := migrate(ctx, s.db, ms); err != nil {
		t.Fatalf("migrate again: %v", err)
	}
	var version int
	s.db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != len(ms) {
		t.Errorf("user_version = %d, want %d", version, len(ms))
	}
}

func TestMigrate_FailedMigrationLeavesVersion(t *testing.T) {
	s := openTest(t)
	ms := append(append([]migration{}, migrations...), migration{
		version: len(migrations) + 1,
		name:    "broken.sql",
		sql:     "CREATE TABLE half (x INTEGER); NOT SQL",
	})
	err := migrate(context.Background(), s.db, ms)
	if err == nil || !strings.Contains(err.Error(), "broken.sql") {
		t.Fatalf("err = %v, want a failure naming broken.sql", err)
	}
	var version int
	s.db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != len(migrations) {
		t.Errorf("user_version = %d, want %d (unchanged)", version, len(migrations))
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = 'half'").Scan(&n)
	if n != 0 {
		t.Error("table from the failed migration was kept")
	}
}
