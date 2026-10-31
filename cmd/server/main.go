// Command server runs the financial planning web app.
package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
	"github.com/igomezal/horizonte/internal/web"
	"github.com/igomezal/horizonte/ui"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address (localhost only by default, for privacy)")
	dbPath := flag.String("db", "projection.db", "path to the SQLite database file")
	dev := flag.Bool("dev", false, "development mode: read templates and static files from ./ui on every request")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(*addr, *dbPath, *dev, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(addr, dbPath string, dev bool, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	catalog, err := i18n.Load()
	if err != nil {
		return err
	}

	var assets fs.FS = ui.FS
	if dev {
		assets = os.DirFS("ui")
		log.Info("dev mode: serving templates and static files from ./ui")
	}

	srv, err := web.New(web.Config{
		Store: st, Catalog: catalog, Logger: log, Assets: assets, Dev: dev,
	})
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "url", "http://"+addr, "db", dbPath)
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

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}
