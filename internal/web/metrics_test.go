package web

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

// nopTransport keeps the test client from sending anything over the network.
type nopTransport struct{}

func (nopTransport) Flush(time.Duration) bool              { return true }
func (nopTransport) FlushWithContext(context.Context) bool { return true }
func (nopTransport) Configure(sentry.ClientOptions)        {}
func (nopTransport) SendEvent(*sentry.Event)               {}
func (nopTransport) Close()                                {}

type capturedMetric struct {
	name, unit string
	attrs      map[string]string
}

// captureMetrics binds a Sentry client that records every metric instead of
// sending it, for the duration of the test.
func captureMetrics(t *testing.T) func() []capturedMetric {
	t.Helper()
	var mu sync.Mutex
	var got []capturedMetric
	err := sentry.Init(sentry.ClientOptions{
		Dsn:       "https://key@o0.ingest.sentry.io/0",
		Transport: nopTransport{},
		BeforeSendMetric: func(m *sentry.Metric) *sentry.Metric {
			c := capturedMetric{name: m.Name, unit: m.Unit, attrs: map[string]string{}}
			for k, v := range m.Attributes {
				c.attrs[k] = v.String()
			}
			mu.Lock()
			got = append(got, c)
			mu.Unlock()
			return nil // recorded; never sent
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })
	return func() []capturedMetric {
		mu.Lock()
		defer mu.Unlock()
		return append([]capturedMetric(nil), got...)
	}
}

func findMetric(ms []capturedMetric, name string) (capturedMetric, bool) {
	for _, m := range ms {
		if m.name == name {
			return m, true
		}
	}
	return capturedMetric{}, false
}

func TestMetricFormRejectedHasRouteOnly(t *testing.T) {
	metrics := captureMetrics(t)
	s, _ := newLoggedServer(t)
	const typed = "  " // an invalid plan name
	if rec := postForm(s, "/scenarios", url.Values{"name": {typed}, "source": {"1"}, "preset": {"pessimistic"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	m, ok := findMetric(metrics(), "horizonte.form.rejected")
	if !ok {
		t.Fatalf("no horizonte.form.rejected metric; got %+v", metrics())
	}
	if m.attrs["http.route"] != "POST /scenarios" {
		t.Errorf("http.route = %q", m.attrs["http.route"])
	}
}

func TestMetricImportRejected(t *testing.T) {
	metrics := captureMetrics(t)
	s, _ := newLoggedServer(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "plan.json")
	fw.Write([]byte(`{"not":"a plan"}`))
	mw.Close()
	req := httptest.NewRequest("POST", "/scenarios/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	do(s, req)

	m, ok := findMetric(metrics(), "horizonte.import")
	if !ok {
		t.Fatalf("no horizonte.import metric; got %+v", metrics())
	}
	if m.attrs["horizonte.import.outcome"] != "rejected" || m.attrs["horizonte.import.reject_reason"] != "format" {
		t.Errorf("attrs = %v", m.attrs)
	}
	if size, ok := findMetric(metrics(), "horizonte.import.file_size"); !ok || size.unit != sentry.UnitByte {
		t.Errorf("file size metric = %+v, %v", size, ok)
	}
}

func TestMetricCrossOriginRejected(t *testing.T) {
	metrics := captureMetrics(t)
	s, _ := newLoggedServer(t)
	req := httptest.NewRequest("POST", "/lang", strings.NewReader("lang=en"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	do(s, req)
	if m, ok := findMetric(metrics(), "horizonte.csrf.rejected"); !ok || m.attrs["http.request.method"] != "POST" {
		t.Errorf("csrf metric = %+v, %v", m, ok)
	}
}

func TestMetricSimulationDuration(t *testing.T) {
	metrics := captureMetrics(t)
	s, _ := newFinanceServer(t)
	seedMC(t, s)
	for _, path := range []string{"/success/results", "/backtest/summary", "/success/results"} {
		if rec, _ := do(s, htmxReq("GET", path, nil)); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rec.Code)
		}
	}
	kinds := map[string]capturedMetric{}
	for _, m := range metrics() {
		if m.name == "horizonte.simulation.duration" {
			kinds[m.attrs["horizonte.simulation.kind"]] = m
		}
	}
	mc, ok := kinds["monte_carlo"]
	if !ok || mc.unit != sentry.UnitMillisecond || mc.attrs["horizonte.simulation.source"] != "success" ||
		mc.attrs["horizonte.simulation.runs"] == "" || mc.attrs["horizonte.simulation.months"] == "0" {
		t.Errorf("monte carlo metric = %+v, %v", mc, ok)
	}
	if bt, ok := kinds["rolling_backtest"]; !ok || bt.attrs["horizonte.simulation.source"] != "backtest" {
		t.Errorf("backtest metric = %+v, %v", bt, ok)
	}

	// The second Monte Carlo request came from the cache.
	cache := map[string]int{}
	for _, m := range metrics() {
		if m.name == "horizonte.simulation.cache" && m.attrs["horizonte.simulation.kind"] == "monte_carlo" {
			cache[m.attrs["horizonte.simulation.cache"]]++
		}
	}
	if cache["miss"] != 1 || cache["hit"] != 1 {
		t.Errorf("monte carlo cache counts = %v, want 1 miss and 1 hit", cache)
	}
}

// A page that finds its result cached renders it straight away; that hit
// must be counted too, or a warm cache looks idle in Sentry.
func TestMetricSimulationCachePageHits(t *testing.T) {
	metrics := captureMetrics(t)
	s, _ := newFinanceServer(t)
	seedMC(t, s)
	// Fill the cache the way the pages' placeholders do.
	for _, path := range []string{"/success/results", "/backtest/summary"} {
		if rec, _ := do(s, htmxReq("GET", path, nil)); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rec.Code)
		}
	}
	plans, err := s.store.ListPlans(context.Background())
	if err != nil || len(plans) == 0 {
		t.Fatalf("plans: %v %v", plans, err)
	}
	if rec, _ := do(s, htmxReq("GET", fmt.Sprintf("/scenarios/%d/odds", plans[0].ID), nil)); rec.Code != http.StatusOK {
		t.Fatalf("odds: %d", rec.Code)
	}
	before := len(metrics())

	for _, path := range []string{"/success", "/backtest", "/scenarios"} {
		rec, body := do(s, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		if strings.Contains(body, "is-pending") {
			t.Errorf("%s rendered a placeholder although the result was cached", path)
		}
	}

	got := map[string]int{}
	for _, m := range metrics()[before:] {
		if m.name != "horizonte.simulation.cache" {
			continue
		}
		if m.attrs["horizonte.simulation.cache"] != "hit" || m.attrs["horizonte.simulation.served"] != "page" {
			t.Errorf("page lookup counted as %v", m.attrs)
		}
		got[m.attrs["horizonte.simulation.source"]+"/"+m.attrs["horizonte.simulation.kind"]]++
	}
	want := map[string]int{
		"success/monte_carlo":        1,
		"backtest/rolling_backtest":  1,
		"scenarios/monte_carlo":      1,
		"scenarios/rolling_backtest": 1,
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("%s hits = %d, want %d (all: %v)", k, got[k], n, got)
		}
	}
	for _, m := range metrics()[:before] {
		if m.name == "horizonte.simulation.cache" && m.attrs["horizonte.simulation.served"] != "results" {
			t.Errorf("results lookup counted as %v", m.attrs)
		}
	}
}
