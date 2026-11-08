package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/store"
	"github.com/igomezal/horizonte/internal/web"
)

// simGate, when set, holds every Monte Carlo simulation until it is closed.
var simGate atomic.Pointer[chan struct{}]

// The hook is installed once, here: simulations that outlive a run still read
// engine.TestHook, so a test can't safely clear it.
func init() {
	engine.TestHook = func(point string, _ engine.Input) {
		if point != "monte_carlo.start" {
			return
		}
		if gate := simGate.Load(); gate != nil {
			<-*gate
		}
	}
}

// warmupLog records run's logs. When the background phase says it has ended,
// it also checks that the database was still open at that moment.
type warmupLog struct {
	mu       sync.Mutex
	store    *store.Store
	lines    []string
	finished map[string]string // attributes of the background phase's last log
	pingErr  error             // the database's state when that log was written

	startupPlans string // how many plans the startup phase listed, once it has ended
}

func (l *warmupLog) Enabled(context.Context, slog.Level) bool { return true }
func (l *warmupLog) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *warmupLog) WithGroup(string) slog.Handler            { return l }

func (l *warmupLog) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]string{}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprint(r.Message, " ", attrs))
	if r.Message == "warmup finished" && attrs["horizonte.warmup.phase"] == "startup" {
		l.startupPlans = attrs["horizonte.warmup.plans"]
	}
	if r.Message == "warmup finished" && attrs["horizonte.warmup.phase"] == "background" {
		l.finished = attrs
		l.pingErr = l.store.DB.PingContext(context.Background())
	}
	return nil
}

func (l *warmupLog) setStore(st *store.Store) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.store = st
}

func (l *warmupLog) dump() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// seedDatabase creates a database with one plan that has something to simulate.
func seedDatabase(t *testing.T, path string) {
	t.Helper()
	st, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	plan, err := st.CurrentPlan(t.Context(), "plan")
	if err != nil {
		t.Fatal(err)
	}
	birth := domain.MonthOf(time.Date(1990, 4, 1, 0, 0, 0, 0, time.UTC))
	if _, err := st.SavePerson(t.Context(), domain.Person{PlanID: plan, Name: "Ana", Birth: birth, LifeExpectancy: 90}); err != nil {
		t.Fatal(err)
	}
}

// The background warm-up reads the database, so run must end it before closing
// the database, on every way out. Here the way out is a port already in use,
// with the background phase still waiting for the simulation held by simGate.
func TestRunEndsTheBackgroundWarmUpBeforeClosingTheDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	seedDatabase(t, dbPath)

	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	gate := make(chan struct{})
	defer close(gate)
	simGate.Store(&gate)

	logs := &warmupLog{}
	testWebConfig = func(cfg *web.Config) {
		cfg.WarmTimeout = 5 * time.Second // long enough to list the plans
		logs.setStore(cfg.Store)
	}
	t.Cleanup(func() { testWebConfig = nil })

	logger := slog.New(logs)
	err = run(held.Addr().String(), dbPath, false, backupSettings{Keep: 7}, sentrySettings{}, logger, logger)

	var opErr *net.OpError
	if !errors.As(err, &opErr) || opErr.Op != "listen" {
		t.Fatalf("run = %v, want a listen error", err)
	}
	logs.mu.Lock()
	finished, pingErr, startupPlans := logs.finished, logs.pingErr, logs.startupPlans
	logs.mu.Unlock()
	// A listing the limit cut short logs nothing; one that failed or came back
	// empty says so, and must fail below rather than be skipped.
	if dump := logs.dump(); startupPlans == "0" && !strings.Contains(dump, "warmup started") && !strings.Contains(dump, "warmup failed") {
		t.Skip("precondition not met: the startup phase listed no plans within its limit, so no background phase started")
	}
	if finished == nil {
		t.Fatalf("the background phase had not ended when run returned:\n%s", logs.dump())
	}
	if finished["horizonte.warmup.outcome"] != "cancelled" || finished["horizonte.warmup.failed"] != "0" {
		t.Errorf("background finished = %v, want cancelled with no failures", finished)
	}
	if pingErr != nil {
		t.Errorf("the database was closed before the background phase ended: %v", pingErr)
	}
	if dump := logs.dump(); strings.Contains(dump, "warmup plan failed") || strings.Contains(dump, "database is closed") {
		t.Errorf("the background phase used a closed database:\n%s", dump)
	}
}
