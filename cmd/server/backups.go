package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/igomezal/horizonte/internal/store"
)

// backupSettings holds the automatic backup flags.
type backupSettings struct {
	Dir      string        // empty means "backups" next to the database
	Interval time.Duration // 0 turns the schedule off
	Keep     int
}

func (b backupSettings) validate() error {
	if b.Interval < 0 {
		return errors.New("-backup-interval must not be negative")
	}
	if b.Keep < 1 {
		return errors.New("-backup-keep must be at least 1")
	}
	return nil
}

// Values of horizonte.backup.trigger.
const (
	backupTriggerMigration = "migration"
	backupTriggerSchedule  = "schedule"
)

// reportBackup logs the outcome of a backup and reports a failure to Sentry.
// Only base filenames, counts and durations: see the conventions in logging.go.
func reportBackup(ctx context.Context, log *slog.Logger, trigger string, res store.SnapshotResult, err error) {
	if err != nil {
		log.ErrorContext(ctx, "database backup failed",
			"horizonte.backup.trigger", trigger, "error.message", err.Error())
		sentry.CurrentHub().CaptureException(err)
		return
	}
	log.InfoContext(ctx, "database backup written",
		"horizonte.backup.trigger", trigger,
		"horizonte.backup.file", res.File,
		"horizonte.backup.removed", res.Removed,
		"horizonte.backup.duration_ms", res.Duration.Milliseconds())
	if res.RemoveFailed > 0 {
		log.WarnContext(ctx, "database backup retention incomplete",
			"horizonte.backup.remove_failed", res.RemoveFailed)
	}
}
