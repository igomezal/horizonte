// Command server runs the financial planning web app.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
	"github.com/igomezal/horizonte/internal/web"
	"github.com/igomezal/horizonte/ui"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8347", "listen address (localhost only by default, for privacy)")
	dbPath := flag.String("db", "horizonte.db", "path to the SQLite database file")
	dev := flag.Bool("dev", false, "development mode: read templates and static files from ./ui on every request")
	health := flag.Bool("healthcheck", false, "check that a server listening on -addr is healthy, then exit (for container health checks)")
	flag.Parse()

	if *health {
		if err := healthcheck(*addr); err != nil {
			fmt.Fprintln(os.Stderr, "unhealthy:", err)
			os.Exit(1)
		}
		return
	}

	snt, sentryErr := initSentry(*dev)
	log, access := newLoggers(snt.DSN != "")
	if sentryErr != nil {
		log.Error("sentry init failed; continuing without error reporting", "error.message", sentryErr.Error())
	} else if snt.DSN != "" {
		log.Info("sentry enabled", "horizonte.sentry.environment", snt.Environment, "horizonte.sentry.release", snt.Release)
	}
	err := run(*addr, *dbPath, *dev, snt, log, access)
	if err != nil {
		log.Error("server stopped with an error", "error.message", err.Error())
		sentry.CaptureException(err)
	}
	sentry.Flush(2 * time.Second)
	if err != nil {
		os.Exit(1)
	}
}

// testWebConfig, when set, adjusts the server's configuration. Only tests set it.
var testWebConfig func(*web.Config)

func run(addr, dbPath string, dev bool, snt sentrySettings, log, access *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	// Answers "which schema is this install on?" when a bug only shows up for
	// some users, and records upgrades as they happen.
	mig := st.Migration()
	log.InfoContext(ctx, "database ready",
		"horizonte.db.file", filepath.Base(dbPath),
		"horizonte.db.schema_version", mig.To,
		"horizonte.db.migrations_applied", mig.Applied,
		"horizonte.db.schema_version_before", mig.From)
	recordStartupGauges(ctx, st, dbPath)

	catalog, err := i18n.Load()
	if err != nil {
		return err
	}

	var assets fs.FS = ui.FS
	if dev {
		assets = os.DirFS("ui")
		log.Info("dev mode: serving templates and static files from ./ui", "horizonte.server.dev", true)
	}

	cfg := web.Config{
		Store: st, Catalog: catalog, Logger: log, AccessLog: access, Assets: assets, Dev: dev,
		Sentry: web.BrowserSentry{DSN: snt.DSN, Environment: snt.Environment, Release: snt.Release},
	}
	if testWebConfig != nil {
		testWebConfig(&cfg)
	}
	srv, err := web.New(cfg)
	if err != nil {
		return err
	}
	// Runs before st.Close on every way out of this function: the background
	// warm-up reads the database.
	defer srv.StopWarm()

	// Before listening, so the port normally opens with the simulations ready
	// and nobody reaches a page that is still calculating them. Past its time
	// limit it returns and the remaining plans carry on in the background.
	srv.Warm(ctx)
	if ctx.Err() != nil {
		log.Info("shutting down", "horizonte.server.reason", "signal")
		return nil
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "url.full", "http://"+addr, "horizonte.server.dev", dev)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down", "horizonte.server.reason", "signal")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}
