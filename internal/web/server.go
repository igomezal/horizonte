// Package web contains the HTTP server, routes and handlers.
package web

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"

	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
)

// Config configures the HTTP server.
type Config struct {
	Store   *store.Store
	Catalog *i18n.Catalog
	Logger  *slog.Logger
	// AccessLog receives one line per request (defaults to Logger).
	AccessLog *slog.Logger
	// Assets must contain templates/ and static/ (ui.FS, or os.DirFS("ui") in dev).
	Assets fs.FS
	// Dev re-parses templates on every request.
	Dev bool
	// Now returns the current time (defaults to time.Now; tests pin it).
	Now func() time.Time
	// Sentry configures browser error reporting; empty DSN disables it.
	Sentry BrowserSentry
	// WarmTimeout limits how long Warm blocks (defaults to 60 seconds).
	WarmTimeout time.Duration
}

// BrowserSentry is passed to the page so the browser SDK reports to the same
// Sentry project, environment and release as the server.
type BrowserSentry struct {
	DSN, Environment, Release string
}

// Server serves the web UI.
type Server struct {
	store     *store.Store
	catalog   *i18n.Catalog
	log       *slog.Logger
	render    *renderer
	resources map[string]*resource
	now       func() time.Time
	handler   http.Handler
	sims      *simRunner
	warmBG    warmBackground

	warmTimeout time.Duration
}

// Simulation runner limits (see simrunner.go): each simulation already uses
// every CPU core, so two at a time keeps the CPU busy without making everyone
// wait for everyone; the cache holds the latest few hundred results.
const (
	simConcurrency = 2
	simCacheSize   = 256
)

// New builds the server and its routes.
func New(cfg Config) (*Server, error) {
	staticFS, err := fs.Sub(cfg.Assets, "static")
	if err != nil {
		return nil, fmt.Errorf("web: static assets: %w", err)
	}
	static, err := sharedStaticAssets(cfg.Assets, staticFS, cfg.Dev)
	if err != nil {
		return nil, err
	}
	r, err := newRenderer(cfg.Assets, cfg.Catalog, cfg.Dev, cfg.Sentry, static)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	s := &Server{
		store:     cfg.Store,
		catalog:   cfg.Catalog,
		log:       cfg.Logger,
		render:    r,
		resources: newResources(cfg.Store),
		now:       cfg.Now,
		sims:      newSimRunner(simConcurrency, simCacheSize),

		warmTimeout: cfg.WarmTimeout,
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.warmTimeout == 0 {
		s.warmTimeout = warmTimeout
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", static)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("POST /lang", s.handleSetLang)

	mux.HandleFunc("GET /finances", s.handleFinances)
	mux.HandleFunc("GET /finances/summary", s.handleFinancesSummary)
	mux.HandleFunc("GET /finances/pension", s.handlePension)
	mux.HandleFunc("POST /finances/pension", s.handlePensionAdd)
	mux.HandleFunc("POST /finances/pension/estimate", s.handlePensionEstimate)
	mux.HandleFunc("GET /finances/health", s.handleHealthTool)
	mux.HandleFunc("POST /finances/health", s.handleHealthAdd)
	mux.HandleFunc("POST /finances/health/preview", s.handleHealthPreview)
	mux.HandleFunc("GET /finances/{kind}", s.handleSection)
	mux.HandleFunc("POST /finances/{kind}", s.handleSave)
	mux.HandleFunc("GET /finances/{kind}/new", s.handleNewForm)
	mux.HandleFunc("GET /finances/{kind}/{id}", s.handleRow)
	mux.HandleFunc("GET /finances/{kind}/{id}/edit", s.handleEditForm)
	mux.HandleFunc("PUT /finances/{kind}/{id}", s.handleSave)
	mux.HandleFunc("DELETE /finances/{kind}/{id}", s.handleDelete)

	mux.HandleFunc("GET /plan", s.handlePlan)
	mux.HandleFunc("GET /plan/results", s.handlePlanResults)
	mux.HandleFunc("POST /plan/assumptions", s.handleSaveAssumptions)

	mux.HandleFunc("GET /cashflow", s.handleCashflow)
	mux.HandleFunc("GET /cashflow/year", s.handleCashflowYear)

	mux.HandleFunc("GET /success", s.handleSuccess)
	mux.HandleFunc("GET /success/results", s.handleSuccessResults)
	mux.HandleFunc("POST /success/settings", s.handleSaveMCSettings)

	mux.HandleFunc("GET /backtest", s.handleBacktest)
	mux.HandleFunc("GET /backtest/scenario", s.handleBacktestScenario)
	mux.HandleFunc("GET /backtest/summary", s.handleBacktestRolling("bt-summary"))
	mux.HandleFunc("GET /backtest/heat", s.handleBacktestRolling("bt-heat"))

	mux.HandleFunc("GET /tracking", s.handleTracking)
	mux.HandleFunc("GET /tracking/entries/new", s.handleTrackingNew)
	mux.HandleFunc("POST /tracking/entries", s.handleTrackingSave)
	mux.HandleFunc("GET /tracking/entries/{month}/edit", s.handleTrackingEdit)
	mux.HandleFunc("PUT /tracking/entries/{month}", s.handleTrackingSave)
	mux.HandleFunc("DELETE /tracking/entries/{month}", s.handleTrackingDelete)
	mux.HandleFunc("POST /tracking/baseline", s.handleTrackingBaseline)

	mux.HandleFunc("GET /scenarios", s.handleScenarios)
	mux.HandleFunc("POST /scenarios", s.handleCreateScenario)
	mux.HandleFunc("GET /scenarios/{id}/odds", s.handlePlanOdds)
	mux.HandleFunc("POST /scenarios/{id}/activate", s.handleActivatePlan)
	mux.HandleFunc("POST /scenarios/{id}/rename", s.handleRenamePlan)
	mux.HandleFunc("POST /scenarios/{id}/delete", s.handleDeletePlan)
	mux.HandleFunc("GET /scenarios/export", s.handleExportAll)
	mux.HandleFunc("GET /scenarios/{id}/export", s.handleExportPlan)
	mux.HandleFunc("POST /scenarios/import", s.handleImportPlan)
	mux.HandleFunc("POST /plans/switch", s.handleActivatePlan)
	mux.HandleFunc("/", s.handleNotFound)

	var h http.Handler = mux
	h = i18n.Middleware(h)
	h = sameOriginWrites(cfg.Logger, h)
	h = securityHeaders(contentSecurityPolicy(cfg.Sentry.DSN), h)
	// Sentry sits inside recoverPanics: it reports the panic and re-panics so
	// recoverPanics still renders the 500 page. It is a no-op without a DSN.
	h = sentryhttp.New(sentryhttp.Options{Repanic: true}).Handle(h)
	h = s.recoverPanics(h)
	// Compression wraps everything a handler writes, the 500 page included.
	h = compressResponses(h)
	access := cfg.AccessLog
	if access == nil {
		access = cfg.Logger
	}
	h = logRequests(access, h)
	s.handler = h
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// page renders a full page, or only its "content" block for htmx navigation.
func (s *Server) page(w http.ResponseWriter, r *http.Request, status int, page, titleKey string, data any) {
	block := "layout"
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Boosted") != "true" {
		block = "content"
	}
	pd := pageData{TitleKey: titleKey, Data: data}
	if block == "layout" {
		// The top bar lists the plans; a failure here should not break the page.
		lang := i18n.FromContext(r.Context())
		if cur, err := s.store.CurrentPlan(r.Context(), s.catalog.T(lang, "plan.default_name")); err == nil {
			pd.CurrentPlan = cur
			pd.Plans, _ = s.store.ListPlans(r.Context())
		}
	}
	if err := s.render.render(w, r, status, page, block, pd); err != nil {
		s.serverError(w, r, err)
	}
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		s.log.ErrorContext(r.Context(), "server error",
			"http.request.method", r.Method, "http.route", r.Pattern,
			"error.type", fmt.Sprintf("%T", err), "error.message", err.Error())
		hub := sentry.GetHubFromContext(r.Context())
		if hub == nil {
			hub = sentry.CurrentHub()
		}
		hub.CaptureException(err)
	}
	pd := pageData{TitleKey: "error.server.title", Data: errorData{
		TitleKey: "error.server.title", BodyKey: "error.server.body",
	}}
	if rerr := s.render.render(w, r, http.StatusInternalServerError, "error", "layout", pd); rerr != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}
