package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func testDatabase(t *testing.T) *sql.DB {
	t.Helper()

	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestMigrateAppliesInOrderAndIsIdempotent(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	files := fstest.MapFS{
		"0001_first.sql":  {Data: []byte("CREATE TABLE first (id INTEGER PRIMARY KEY)")},
		"0002_second.sql": {Data: []byte("INSERT INTO first (id) VALUES (1)")},
	}

	if err := Migrate(ctx, database, files); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := Migrate(ctx, database, files); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var versions, rows int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if versions != 2 {
		t.Fatalf("migration versions = %d, want 2", versions)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM first`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows after replay = %d, want 1", rows)
	}
}

func TestMigrateRefusesAChangedMigration(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()

	original := fstest.MapFS{"0001_first.sql": {Data: []byte("CREATE TABLE first (id INTEGER PRIMARY KEY)")}}
	if err := Migrate(ctx, database, original); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	changed := fstest.MapFS{"0001_first.sql": {Data: []byte("CREATE TABLE first (id INTEGER PRIMARY KEY, extra TEXT)")}}
	err := Migrate(ctx, database, changed)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("changed migration: err = %v, want checksum mismatch", err)
	}
}
