package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenMigratesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	want := ms[len(ms)-1].version
	if v, _ := s.SchemaVersion(ctx); v != want {
		t.Fatalf("schema version = %d, want %d", v, want)
	}
	s.Close()

	// Reopening must not re-apply migrations.
	s, err = Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(ms) {
		t.Errorf("schema_migrations has %d rows, want %d", n, len(ms))
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if v, err := s.GetSetting(ctx, "missing", "def"); err != nil || v != "def" {
		t.Fatalf("GetSetting default = %q, %v", v, err)
	}
	if err := s.SetSetting(ctx, "k", "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "k", "b"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetSetting(ctx, "k", ""); v != "b" {
		t.Errorf("GetSetting = %q, want b", v)
	}
}
