package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withExtra returns the real migrations plus one more, so a test database has
// something pending whatever the latest real migration is.
func withExtra(sql string) func() ([]migration, error) {
	return func() ([]migration, error) {
		ms, err := loadMigrations()
		if err != nil {
			return nil, err
		}
		return append(ms, migration{version: ms[len(ms)-1].version + 1, name: "9999_extra.sql", sql: sql}), nil
	}
}

func newSnapshotDB(t *testing.T) (dbPath string, sn *snapshotter) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "plan.db")
	return dbPath, newSnapshotter(dbPath, SnapshotOptions{Keep: 3})
}

// seedExisting creates a fully migrated database with a marker row.
func seedExisting(t *testing.T, dbPath string) {
	t.Helper()
	s, err := Open(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetSetting(t.Context(), "marker", "original"); err != nil {
		t.Fatal(err)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestMigrationBackupOfExistingDatabase(t *testing.T) {
	ctx := t.Context()
	dbPath, sn := newSnapshotDB(t)
	seedExisting(t, dbPath)

	s, err := open(ctx, dbPath, sn, withExtra(`CREATE TABLE extra (x INTEGER)`))
	if err != nil {
		t.Fatal(err)
	}
	mig := s.Migration()
	if mig.Applied != 1 || mig.Backup == nil {
		t.Fatalf("migration = %+v, want one applied with a backup", mig)
	}
	s.Close()

	names := dirNames(t, sn.dir)
	if len(names) != 1 || names[0] != mig.Backup.File || !strings.Contains(names[0], fmt.Sprintf("-pre-v%04d.db", mig.From)) {
		t.Fatalf("backups = %v, result %+v", names, mig.Backup)
	}
	// The copy holds the data, and the schema from before the migration.
	c, err := Open(ctx, filepath.Join(sn.dir, names[0]))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if v, _ := c.GetSetting(ctx, "marker", ""); v != "original" {
		t.Errorf("marker in copy = %q", v)
	}
	var n int
	if err := c.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'extra'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("copy was taken after the migration: n = %d, err = %v", n, err)
	}
}

func TestMigrationBackupOnlyWhenNeeded(t *testing.T) {
	ctx := t.Context()
	t.Run("brand new database", func(t *testing.T) {
		dbPath, sn := newSnapshotDB(t)
		s, err := open(ctx, dbPath, sn, loadMigrations)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if s.Migration().Backup != nil || dirNames(t, sn.dir) != nil {
			t.Errorf("backup taken: %+v, %v", s.Migration(), dirNames(t, sn.dir))
		}
	})
	t.Run("nothing pending", func(t *testing.T) {
		dbPath, sn := newSnapshotDB(t)
		seedExisting(t, dbPath)
		s, err := open(ctx, dbPath, sn, loadMigrations)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if s.Migration().Backup != nil || dirNames(t, sn.dir) != nil {
			t.Errorf("backup taken: %+v, %v", s.Migration(), dirNames(t, sn.dir))
		}
	})
	t.Run("in memory", func(t *testing.T) {
		s, err := OpenWithSnapshots(ctx, ":memory:", SnapshotOptions{Keep: 3})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err := s.Snapshot(ctx); err == nil {
			t.Error("Snapshot of an in-memory database should fail")
		}
		s.StartSnapshotSchedule(ctx, time.Millisecond, func(SnapshotResult, error) {
			t.Error("an in-memory database was scheduled")
		})()
	})
}

func TestFailedBackupAbortsMigration(t *testing.T) {
	ctx := t.Context()
	dbPath, sn := newSnapshotDB(t)
	seedExisting(t, dbPath)

	// A file where the directory should be makes the backup fail.
	sn.dir = filepath.Join(filepath.Dir(dbPath), "blocker")
	if err := os.WriteFile(sn.dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := open(ctx, dbPath, sn, withExtra(`CREATE TABLE extra (x INTEGER)`))
	if err == nil || !strings.Contains(err.Error(), "not migrated") {
		t.Fatalf("open = %v, want a backup error", err)
	}
	if strings.Contains(err.Error(), filepath.Dir(dbPath)) {
		t.Errorf("error exposes a full path: %v", err)
	}

	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'extra'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("migration ran after the backup failed: n = %d, err = %v", n, err)
	}
}

func TestRetentionKeepsTheNewestAndOnlyTouchesItsOwnFiles(t *testing.T) {
	ctx := t.Context()
	dbPath, sn := newSnapshotDB(t)
	s, err := open(ctx, dbPath, sn, loadMigrations)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := os.MkdirAll(sn.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := []string{"notes.txt", "plan-keep-me.db", "other-20260101T000000.000Z.db"}
	for _, f := range foreign {
		if err := os.WriteFile(filepath.Join(sn.dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var made []string
	for i := range 5 {
		sn.now = func() time.Time { return base.Add(time.Duration(i) * time.Hour) }
		res, err := s.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, res.File)
		if i >= 3 && res.Removed != 1 {
			t.Errorf("copy %d removed %d, want 1", i, res.Removed)
		}
	}
	files, err := sn.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files[0].name != made[4] || files[2].name != made[2] {
		t.Errorf("kept %+v, want the last three of %v", files, made)
	}
	for _, f := range foreign {
		if _, err := os.Stat(filepath.Join(sn.dir, f)); err != nil {
			t.Errorf("retention touched %s: %v", f, err)
		}
	}
}

// When a migration keeps failing, each restart takes another copy. Those
// replace each other instead of pushing out the older copies.
func TestRepeatedPreMigrationCopiesDontEvictOthers(t *testing.T) {
	ctx := t.Context()
	dbPath, sn := newSnapshotDB(t)
	s, err := open(ctx, dbPath, sn, loadMigrations)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var daily []string
	for i := range 2 {
		sn.now = func() time.Time { return base.Add(time.Duration(i) * time.Hour) }
		res, err := s.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		daily = append(daily, res.File)
	}
	var last string
	for i := range 6 {
		sn.now = func() time.Time { return base.Add(time.Duration(10+i) * time.Hour) }
		res, err := s.snapshot(ctx, "pre-v0012")
		if err != nil {
			t.Fatal(err)
		}
		last = res.File
	}
	names := strings.Join(dirNames(t, sn.dir), " ")
	for _, want := range append(daily, last) {
		if !strings.Contains(names, want) {
			t.Errorf("%s missing from %s", want, names)
		}
	}
	if n := len(dirNames(t, sn.dir)); n != 3 {
		t.Errorf("%d copies, want 3: %s", n, names)
	}
}

func TestSnapshotIsAtomic(t *testing.T) {
	ctx := t.Context()
	dbPath, sn := newSnapshotDB(t)
	s, err := open(ctx, dbPath, sn, loadMigrations)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sn.now = func() time.Time { return fixed }
	final := filepath.Join(sn.dir, sn.name(fixed, ""))

	t.Run("no final name until the copy is complete", func(t *testing.T) {
		called := false
		sn.beforeRename = func(tmp string) {
			called = true
			if _, err := os.Stat(final); !os.IsNotExist(err) {
				t.Errorf("final name exists before the rename: %v", err)
			}
			if !strings.HasSuffix(tmp, snapshotTempExt) {
				t.Errorf("temporary name = %s", tmp)
			}
		}
		defer func() { sn.beforeRename = nil }()
		if _, err := s.Snapshot(ctx); err != nil {
			t.Fatal(err)
		}
		if !called {
			t.Fatal("hook not called")
		}
		if names := dirNames(t, sn.dir); len(names) != 1 || filepath.Join(sn.dir, names[0]) != final {
			t.Errorf("directory = %v", names)
		}
		if err := os.Remove(final); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a failed copy leaves nothing behind", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := s.Snapshot(cancelled); err == nil {
			t.Fatal("want an error from a cancelled copy")
		}
		if names := dirNames(t, sn.dir); len(names) != 0 {
			t.Errorf("directory = %v, want empty", names)
		}
	})

	t.Run("a failed rename leaves nothing behind", func(t *testing.T) {
		if err := os.Mkdir(final, 0o700); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(final)
		_, err := s.Snapshot(ctx)
		if err == nil {
			t.Fatal("want an error when the rename fails")
		}
		if strings.Contains(err.Error(), filepath.Dir(dbPath)) {
			t.Errorf("error exposes a full path: %v", err)
		}
		if names := dirNames(t, sn.dir); len(names) != 1 || filepath.Join(sn.dir, names[0]) != final {
			t.Errorf("directory = %v, want only the blocking directory", names)
		}
	})

	t.Run("leftovers from a crash are cleared", func(t *testing.T) {
		stale := filepath.Join(sn.dir, "plan-123"+snapshotTempExt)
		if err := os.WriteFile(stale, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		sn.now = func() time.Time { return fixed.Add(time.Hour) }
		if _, err := s.Snapshot(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(stale); !os.IsNotExist(err) {
			t.Errorf("stale temporary file still there: %v", err)
		}
	})
}

func TestFirstDelay(t *testing.T) {
	ctx := t.Context()
	dbPath, sn := newSnapshotDB(t)
	s, err := open(ctx, dbPath, sn, loadMigrations)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const interval = 24 * time.Hour

	if d := sn.firstDelay(interval); d != 0 {
		t.Errorf("with no copies, delay = %v", d)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sn.now = func() time.Time { return base }
	if _, err := s.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		since time.Duration
		want  time.Duration
	}{
		{6 * time.Hour, 18 * time.Hour}, // restarted part-way through the interval
		{48 * time.Hour, 0},             // overdue
		{-time.Hour, interval},          // clock moved back
	}
	for _, c := range cases {
		sn.now = func() time.Time { return base.Add(c.since) }
		if d := sn.firstDelay(interval); d != c.want {
			t.Errorf("%v after the last copy, delay = %v, want %v", c.since, d, c.want)
		}
	}
}

func TestScheduleStopsCleanly(t *testing.T) {
	ctx := t.Context()
	dbPath, sn := newSnapshotDB(t)
	s, err := open(ctx, dbPath, sn, loadMigrations)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	results := make(chan error, 1000)
	stop := s.StartSnapshotSchedule(ctx, 5*time.Millisecond, func(_ SnapshotResult, err error) { results <- err })
	for range 3 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("no backup from the schedule")
		}
	}
	stop()
	stop() // stopping twice is harmless

	// stop waited for the goroutine, so nothing more can arrive or be written.
	before := len(results)
	names := dirNames(t, sn.dir)
	time.Sleep(30 * time.Millisecond)
	if len(results) != before {
		t.Error("the schedule ran after stop")
	}
	if got := dirNames(t, sn.dir); strings.Join(got, ",") != strings.Join(names, ",") {
		t.Errorf("directory changed after stop: %v then %v", names, got)
	}
	if len(names) > sn.keep {
		t.Errorf("%d copies, want at most %d", len(names), sn.keep)
	}
	for _, n := range names {
		if strings.Contains(n, snapshotTempExt) {
			t.Errorf("temporary file left behind: %s", n)
		}
	}
}

func TestScheduleSurvivesFailures(t *testing.T) {
	ctx := t.Context()
	dbPath, sn := newSnapshotDB(t)
	s, err := open(ctx, dbPath, sn, loadMigrations)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sn.dir = filepath.Join(filepath.Dir(dbPath), "blocker")
	if err := os.WriteFile(sn.dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	failures := make(chan error, 1000)
	stop := s.StartSnapshotSchedule(ctx, time.Millisecond, func(_ SnapshotResult, err error) { failures <- err })
	defer stop()
	for range 3 {
		select {
		case err := <-failures:
			if err == nil {
				t.Fatal("want a failure")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the schedule stopped after a failure")
		}
	}
}

func TestScheduleDisabled(t *testing.T) {
	dbPath, sn := newSnapshotDB(t)
	s, err := open(t.Context(), dbPath, sn, loadMigrations)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.StartSnapshotSchedule(t.Context(), 0, func(SnapshotResult, error) { t.Error("ran with no interval") })()
	if names := dirNames(t, sn.dir); names != nil {
		t.Errorf("directory = %v", names)
	}
}
