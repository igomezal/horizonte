package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/igomezal/horizonte/internal/backup"
	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
	"github.com/igomezal/horizonte/ui"
)

// logRecorder is a slog.Handler that keeps every record for assertions.
type logRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (l *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (l *logRecorder) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *logRecorder) WithGroup(string) slog.Handler            { return l }
func (l *logRecorder) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, r.Clone())
	return nil
}

// find returns the attributes of the first record with this message.
func (l *logRecorder) find(msg string) (slog.Level, map[string]string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.records {
		if r.Message != msg {
			continue
		}
		attrs := map[string]string{}
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.String()
			return true
		})
		return r.Level, attrs, true
	}
	return 0, nil, false
}

// dump renders every record, to check sensitive text never reaches a log.
func (l *logRecorder) dump() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var b strings.Builder
	for _, r := range l.records {
		fmt.Fprint(&b, r.Message)
		r.Attrs(func(a slog.Attr) bool {
			fmt.Fprintf(&b, " %s=%s", a.Key, a.Value)
			return true
		})
		b.WriteString("\n")
	}
	return b.String()
}

func newLoggedServer(t *testing.T) (*Server, *logRecorder) {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cat, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec := &logRecorder{}
	s, err := New(Config{Store: st, Catalog: cat, Assets: ui.FS, Logger: slog.New(rec)})
	if err != nil {
		t.Fatal(err)
	}
	return s, rec
}

func TestLogCrossOriginRejected(t *testing.T) {
	s, logs := newLoggedServer(t)
	req := httptest.NewRequest("POST", "/lang", strings.NewReader("lang=en"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	do(s, req)

	level, attrs, ok := logs.find("cross-origin write rejected")
	if !ok {
		t.Fatalf("no rejection log; got:\n%s", logs.dump())
	}
	if level != slog.LevelWarn {
		t.Errorf("level = %v, want WARN", level)
	}
	for k, want := range map[string]string{
		"http.request.method": "POST", "url.path": "/lang", "horizonte.csrf.sec_fetch_site": "cross-site",
	} {
		if attrs[k] != want {
			t.Errorf("%s = %q, want %q", k, attrs[k], want)
		}
	}
}

func TestLogPlanLifecycleHasNoPlanNames(t *testing.T) {
	s, logs := newLoggedServer(t)
	const secret = "Plan jubilación Ana" // user text: must never be logged
	rec := postForm(s, "/scenarios", url.Values{"name": {secret}, "source": {"1"}, "preset": {"pessimistic"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", rec.Code)
	}
	_, attrs, ok := logs.find("plan created")
	if !ok {
		t.Fatalf("no plan created log; got:\n%s", logs.dump())
	}
	if attrs["horizonte.plan.id"] != "2" || attrs["horizonte.plan.preset"] != "pessimistic" {
		t.Errorf("plan created attrs = %v", attrs)
	}

	postForm(s, "/scenarios/2/delete", nil)
	if _, _, ok := logs.find("plan deleted"); !ok {
		t.Error("no plan deleted log")
	}
	postForm(s, "/scenarios/1/delete", nil) // last plan: refused
	if level, attrs, ok := logs.find("plan delete refused"); !ok || level != slog.LevelWarn ||
		attrs["horizonte.plan.refused_reason"] != "last_plan" {
		t.Errorf("plan delete refused = %v %v %v", ok, level, attrs)
	}

	if strings.Contains(logs.dump(), secret) {
		t.Errorf("a log contains the plan name:\n%s", logs.dump())
	}
}

func TestClassifyImportErrorDropsUserText(t *testing.T) {
	err := fmt.Errorf("wrap: %w", &backup.InvalidError{Plan: "Mi plan", Section: "accounts", Name: "Cuenta Ana", Field: "balance"})
	got := classifyImportError(err)
	want := importReject{Reason: "invalid", Section: "accounts", Field: "balance"}
	if got != want {
		t.Errorf("classify = %+v, want %+v", got, want)
	}
	if s := fmt.Sprint(got.attrs()...); strings.Contains(s, "Ana") || strings.Contains(s, "Mi plan") {
		t.Errorf("attrs leak user text: %s", s)
	}
	if got := classifyImportError(backup.ErrVersion); got.Reason != "version" {
		t.Errorf("version error classified as %q", got.Reason)
	}
}
