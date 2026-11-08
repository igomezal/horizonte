package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Automatic backups are complete copies of the database made with VACUUM INTO,
// which is safe while the app is running. A copy is first written under a
// temporary name and only renamed once complete, so a file with the final name
// is never partial. Errors from here reach logs and Sentry, so they carry base
// filenames at most, never the full paths.

const (
	snapshotExt     = ".db"
	snapshotTempExt = ".db.tmp"
	// Sorts chronologically as text. UTC, so daylight saving can't reorder files.
	snapshotStamp  = "20060102T150405.000Z"
	preMigrationAt = "pre-v"
)

// SnapshotOptions configures automatic backups.
type SnapshotOptions struct {
	Dir  string // where copies are kept; empty means "backups" next to the database
	Keep int    // how many copies to keep
}

// SnapshotResult describes a finished backup.
type SnapshotResult struct {
	File         string // base filename of the new copy
	Removed      int    // old copies deleted by retention
	RemoveFailed int    // old copies retention could not delete
	Duration     time.Duration
}

// snapshotter knows where a database's copies live and how they are named.
type snapshotter struct {
	dir  string
	base string // database filename without its extension; prefix of every copy
	keep int
	now  func() time.Time

	beforeRename func(tmp string) // only set by tests
}

// newSnapshotter returns nil for a database that isn't a file, which has
// nothing to copy and nowhere to put the copies.
func newSnapshotter(dbPath string, opts SnapshotOptions) *snapshotter {
	if dbPath == "" || strings.Contains(dbPath, ":memory:") || strings.Contains(dbPath, "mode=memory") {
		return nil
	}
	dir := opts.Dir
	if dir == "" {
		dir = filepath.Join(filepath.Dir(dbPath), "backups")
	}
	file := filepath.Base(dbPath)
	return &snapshotter{
		dir:  filepath.Clean(dir),
		base: strings.TrimSuffix(file, filepath.Ext(file)),
		keep: max(opts.Keep, 1),
		now:  time.Now,
	}
}

// OpenWithSnapshots is Open for the running app: a database about to be
// migrated is copied first, and Snapshot and StartSnapshotSchedule are
// available afterwards.
func OpenWithSnapshots(ctx context.Context, dbPath string, opts SnapshotOptions) (*Store, error) {
	return open(ctx, dbPath, newSnapshotter(dbPath, opts), loadMigrations)
}

// backupBeforeMigrating copies a database that has data in it. A brand-new one
// has nothing to protect yet.
func (s *Store) backupBeforeMigrating(ctx context.Context, version int) error {
	var tables int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations'`).Scan(&tables); err != nil {
		return fmt.Errorf("store: inspect database before migrating: %w", err)
	}
	if tables == 0 {
		return nil
	}
	res, err := s.snapshot(ctx, fmt.Sprintf("%s%04d", preMigrationAt, version))
	if err != nil {
		return fmt.Errorf("store: backup before migrating failed, database not migrated: %w", err)
	}
	s.migration.Backup = &res
	return nil
}

// Snapshot writes a new copy of the database and applies retention.
func (s *Store) Snapshot(ctx context.Context) (SnapshotResult, error) {
	if s.snapshots == nil {
		return SnapshotResult{}, errors.New("store: backup: not available for this database")
	}
	return s.snapshot(ctx, "")
}

// snapshot writes a copy; tag, if any, becomes part of its filename.
func (s *Store) snapshot(ctx context.Context, tag string) (SnapshotResult, error) {
	sn := s.snapshots
	start := time.Now()
	if err := os.MkdirAll(sn.dir, 0o700); err != nil {
		return SnapshotResult{}, sn.safeErr("create directory", err)
	}
	sn.removeStaleTemps()

	// VACUUM INTO accepts an empty file that already exists, and this way the
	// copy gets a unique name and is private to the user from the start.
	f, err := os.CreateTemp(sn.dir, sn.base+"-*"+snapshotTempExt)
	if err != nil {
		return SnapshotResult{}, sn.safeErr("create temporary file", err)
	}
	tmp := f.Name()
	f.Close()
	done := false
	defer func() {
		if !done {
			removeTemp(tmp)
		}
	}()

	if _, err := s.DB.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		return SnapshotResult{}, sn.safeErr("copy database", err)
	}
	if err := syncFile(tmp); err != nil {
		return SnapshotResult{}, sn.safeErr("flush copy", err)
	}
	if sn.beforeRename != nil {
		sn.beforeRename(tmp)
	}
	name := sn.name(sn.now(), tag)
	if err := os.Rename(tmp, filepath.Join(sn.dir, name)); err != nil {
		return SnapshotResult{}, sn.safeErr("rename copy", err)
	}
	done = true
	syncDir(sn.dir)

	res := SnapshotResult{File: name}
	res.Removed, res.RemoveFailed = sn.prune(name, tag)
	res.Duration = time.Since(start)
	return res, nil
}

func (sn *snapshotter) name(t time.Time, tag string) string {
	name := sn.base + "-" + t.UTC().Format(snapshotStamp)
	if tag != "" {
		name += "-" + tag
	}
	return name + snapshotExt
}

// snapshotFile is a copy found in the backups directory.
type snapshotFile struct {
	name string
	at   time.Time
	tag  string
}

// list returns this database's copies, newest first. Only names this package
// generates count, so retention can never delete anything else in the directory.
func (sn *snapshotter) list() ([]snapshotFile, error) {
	entries, err := os.ReadDir(sn.dir)
	if err != nil {
		return nil, err
	}
	var files []snapshotFile
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), sn.base+"-")
		if !ok || e.IsDir() {
			continue
		}
		if rest, ok = strings.CutSuffix(rest, snapshotExt); !ok {
			continue
		}
		stamp, tag, _ := strings.Cut(rest, "-")
		at, err := time.Parse(snapshotStamp, stamp)
		if err != nil || (tag != "" && !strings.HasPrefix(tag, preMigrationAt)) {
			continue
		}
		files = append(files, snapshotFile{name: e.Name(), at: at, tag: tag})
	}
	slices.SortFunc(files, func(a, b snapshotFile) int {
		return cmp.Or(b.at.Compare(a.at), cmp.Compare(b.name, a.name))
	})
	return files, nil
}

// prune deletes the copies retention no longer keeps. A pre-migration copy
// first replaces earlier ones of the same schema version: when a migration
// fails, every restart takes another one of the same data, and those must not
// push out the copies from other days.
func (sn *snapshotter) prune(newest, tag string) (removed, failed int) {
	files, err := sn.list()
	if err != nil {
		return 0, 1
	}
	kept := 0
	for _, f := range files {
		superseded := tag != "" && f.tag == tag && f.name != newest
		if !superseded && kept < sn.keep {
			kept++
			continue
		}
		if err := os.Remove(filepath.Join(sn.dir, f.name)); err != nil {
			failed++
		} else {
			removed++
		}
	}
	return removed, failed
}

// removeStaleTemps clears what a crash in the middle of a copy left behind.
func (sn *snapshotter) removeStaleTemps() {
	entries, err := os.ReadDir(sn.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), sn.base+"-") && strings.Contains(e.Name(), snapshotTempExt) {
			os.Remove(filepath.Join(sn.dir, e.Name()))
		}
	}
}

// removeTemp deletes a temporary copy and any rollback journal SQLite left with it.
func removeTemp(tmp string) {
	os.Remove(tmp)
	os.Remove(tmp + "-journal")
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// syncDir makes a rename durable. Best effort: Windows can't sync a directory,
// and the copy itself is complete either way.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

// safeErr drops the paths from err.
func (sn *snapshotter) safeErr(step string, err error) error {
	var pathErr *fs.PathError
	var linkErr *os.LinkError
	msg := err.Error()
	switch {
	case errors.As(err, &pathErr):
		msg = pathErr.Err.Error()
	case errors.As(err, &linkErr):
		msg = linkErr.Err.Error()
	default: // SQLite quotes the file it couldn't open
		msg = strings.ReplaceAll(msg, sn.dir+string(filepath.Separator), "")
	}
	return fmt.Errorf("store: backup: %s: %s", step, msg)
}

// firstDelay is how long the schedule waits for its first copy. It counts from
// the newest existing copy, so an app restarted more often than the interval
// still gets its backups; with no copy yet, one is due now.
func (sn *snapshotter) firstDelay(interval time.Duration) time.Duration {
	files, err := sn.list()
	if err != nil || len(files) == 0 {
		return 0
	}
	return min(max(interval-sn.now().Sub(files[0].at), 0), interval)
}

// StartSnapshotSchedule writes a copy every interval until ctx ends or the
// returned stop function is called. report is called from the schedule's
// goroutine after each attempt; a failed attempt never ends the schedule. stop
// waits for the goroutine to exit, so call it before Close. It can be called
// more than once. With no interval, or no file to copy, nothing is scheduled.
func (s *Store) StartSnapshotSchedule(ctx context.Context, interval time.Duration, report func(SnapshotResult, error)) (stop func()) {
	if s.snapshots == nil || interval <= 0 {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(s.snapshots.firstDelay(interval))
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			res, err := s.Snapshot(ctx)
			if err != nil && ctx.Err() != nil {
				return // interrupted by the shutdown, not a failure
			}
			report(res, err)
			timer.Reset(interval)
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
