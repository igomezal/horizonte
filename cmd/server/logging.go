package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	sentryslog "github.com/getsentry/sentry-go/slog"
)

// Logging conventions (shared with ui/static/js/sentry-init.js so server and
// browser logs can be searched together in Sentry):
//
//   - Messages are short, stable event names ("plan created", "import
//     rejected"); variable data goes in attributes, never in the message.
//   - Standard keys where one exists: http.request.method, http.route,
//     url.path, http.response.status_code, error.type.
//   - App-specific keys are namespaced: horizonte.<domain>.<field>.
//   - Never log amounts, names, notes or anything else a person typed in.
//     IDs, counts, enum values and durations are fine.
//   - Levels: info = normal noteworthy event, warn = handled but notable,
//     error = handled failure. Unexpected failures are captured as Sentry
//     errors (sentry.CaptureException), not just logged.

// newLoggers returns the app logger, which also sends info-and-above records
// to Sentry Logs when Sentry is enabled, and an access logger that only
// writes to stdout (per-request lines are already covered by tracing).
func newLoggers(sentryEnabled bool) (app, access *slog.Logger) {
	text := slog.NewTextHandler(os.Stdout, nil)
	access = slog.New(text)
	if !sentryEnabled {
		return access, access
	}
	toSentry := sentryslog.Option{
		LogLevel: []slog.Level{slog.LevelInfo, slog.LevelWarn, slog.LevelError, sentryslog.LevelFatal},
	}.NewSentryHandler(context.Background())
	return slog.New(fanout{text, toSentry}), access
}

// fanout sends each record to several handlers.
// TODO: replace with slog.NewMultiHandler once the module requires Go 1.26.
type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (f fanout) WithGroup(name string) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}
