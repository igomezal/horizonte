package main

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/igomezal/horizonte/internal/store"
)

func TestBackupSettingsValidate(t *testing.T) {
	cases := []struct {
		s  backupSettings
		ok bool
	}{
		{backupSettings{Interval: 24 * time.Hour, Keep: 7}, true},
		{backupSettings{Interval: 0, Keep: 1}, true},
		{backupSettings{Interval: -time.Second, Keep: 7}, false},
		{backupSettings{Interval: time.Hour, Keep: 0}, false},
	}
	for _, c := range cases {
		if err := c.s.validate(); (err == nil) != c.ok {
			t.Errorf("%+v: validate = %v", c.s, err)
		}
	}
}

func TestReportBackup(t *testing.T) {
	var b strings.Builder
	log := slog.New(slog.NewTextHandler(&b, nil))

	reportBackup(t.Context(), log, backupTriggerSchedule, store.SnapshotResult{File: "plan-x.db", Removed: 2, RemoveFailed: 1}, nil)
	for _, want := range []string{"database backup written", "plan-x.db", "horizonte.backup.removed=2", "retention incomplete"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, b.String())
		}
	}

	b.Reset()
	reportBackup(t.Context(), log, backupTriggerSchedule, store.SnapshotResult{}, errors.New("store: backup: copy database: disk full"))
	if !strings.Contains(b.String(), "level=ERROR") || !strings.Contains(b.String(), "database backup failed") {
		t.Errorf("failure not logged as an error:\n%s", b.String())
	}
}
