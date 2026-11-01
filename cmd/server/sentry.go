package main

import (
	"context"
	"os"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"

	"github.com/igomezal/horizonte/internal/store"
)

// release is the app version reported to Sentry. Set it at build time with
// -ldflags "-X main.release=horizonte@<version>"; when empty the SDK falls back
// to SENTRY_RELEASE or the VCS revision embedded by the Go toolchain.
var release string

// sentrySettings is what the browser needs to report to the same project.
type sentrySettings struct {
	DSN, Environment, Release string
}

// initSentry enables error and performance reporting only when SENTRY_DSN is
// set, so a self-hosted install sends nothing anywhere by default.
//
// Horizonte holds personal financial data, so collection is locked down:
// no IPs, cookies, request bodies or query strings, and only a few harmless
// request headers keep their values.
func initSentry(dev bool) (sentrySettings, error) {
	dsn := strings.TrimSpace(os.Getenv("SENTRY_DSN"))
	if dsn == "" {
		return sentrySettings{}, nil
	}
	env := os.Getenv("SENTRY_ENVIRONMENT")
	if env == "" {
		env = "production"
		if dev {
			env = "development"
		}
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      env,
		Release:          release,
		ServerName:       "horizonte", // not the machine's hostname
		AttachStacktrace: true,
		DataCollection: &sentry.DataCollection{
			UserInfo:   sentry.Set(false),
			Cookies:    &sentry.KeyValueCollectionBehavior{Mode: sentry.CollectionOff},
			HTTPBodies: []sentry.BodyType{},
			HTTPHeaders: &sentry.HeaderCollectionConfig{
				Request: &sentry.KeyValueCollectionBehavior{
					Mode:  sentry.CollectionAllowList,
					Terms: []string{"User-Agent", "Accept-Language", "HX-Request", "HX-Boosted"},
				},
				Response: &sentry.KeyValueCollectionBehavior{Mode: sentry.CollectionOff},
			},
			QueryParams: &sentry.KeyValueCollectionBehavior{Mode: sentry.CollectionOff},
		},
		// Sentry Logs need no option in current sentry-go: they are on once the
		// slog integration is set up (see logging.go).
		EnableTracing: true,
		TracesSampler: func(ctx sentry.SamplingContext) float64 {
			// Static files and health checks are noise, not performance data.
			name := ctx.Span.Name
			if strings.Contains(name, "/static/") || strings.HasSuffix(name, "/healthz") {
				return 0
			}
			return 1.0
		},
	})
	if err != nil {
		return sentrySettings{}, err
	}
	return sentrySettings{
		DSN:         dsn,
		Environment: env,
		Release:     sentry.CurrentHub().Client().Options().Release,
	}, nil
}

// recordStartupGauges records the size of this install once per start. Slow
// pages or failed migrations are much easier to explain next to how big the
// database is and how many plans it holds. A no-op when Sentry is off.
func recordStartupGauges(ctx context.Context, st *store.Store, dbPath string) {
	m := sentry.NewMeter(ctx)
	schema := attribute.Int("horizonte.db.schema_version", st.Migration().To)
	if fi, err := os.Stat(dbPath); err == nil {
		m.Gauge("horizonte.db.size", float64(fi.Size()),
			sentry.WithUnit(sentry.UnitByte), sentry.WithAttributes(schema))
	}
	if plans, err := st.ListPlans(ctx); err == nil {
		m.Gauge("horizonte.plans", float64(len(plans)), sentry.WithAttributes(schema))
	}
}
