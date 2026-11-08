package web

import (
	"fmt"
	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets ResponseController reach the underlying writer through logging,
// so middleware like compression can still flush streaming responses.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// logRequests logs method, path, status and duration of every request. It
// writes to the access logger (stdout only): Sentry already records every
// request as a trace, so sending these lines too would only add volume.
func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Info("request", "method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration", time.Since(start).Round(time.Microsecond))
	})
}

// recoverPanics turns a handler panic into a 500 response instead of a dropped connection.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				// sentryhttp has already captured the panic as an error with a
				// stack trace; this line keeps it visible in stdout and Logs.
				s.log.ErrorContext(r.Context(), "handler panicked",
					"http.request.method", r.Method, "http.route", r.Pattern,
					"error.type", fmt.Sprintf("%T", v))
				s.serverError(w, r, nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy returns the Content-Security-Policy for every response.
//
// What the app needs beyond 'self':
//   - script-src 'unsafe-eval': the standard Alpine build compiles its
//     expressions with new Function.
//   - style-src 'unsafe-inline': chart tooltips are HTML with style attributes,
//     and htmx adds a <style> element for its indicators.
//   - img-src data:: Pico.css draws its form icons with data: URIs.
//
// Scripts are never inline: templates must use files and Alpine directives
// instead of <script> blocks and on* attributes (TestTemplatesHaveNoInlineScripts).
//
// With a Sentry DSN the browser SDK also posts to the project's ingest origin,
// and Session Replay compresses its recording in a worker started from a blob: URL.
func contentSecurityPolicy(sentryDSN string) string {
	connect := "'self'"
	directives := []string{
		"default-src 'self'",
		"script-src 'self' 'unsafe-eval'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"font-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}
	if sentryDSN != "" {
		directives = append(directives, "worker-src 'self' blob:")
		if u, err := url.Parse(sentryDSN); err == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") {
			connect += " " + u.Scheme + "://" + u.Host
		}
	}
	return strings.Join(append(directives, "connect-src "+connect), "; ")
}

// securityHeaders sets conservative browser security headers.
func securityHeaders(csp string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// sameOriginWrites rejects state-changing requests that come from another site.
// The app has no login, so this is what stops a malicious web page from
// posting to the local server (CSRF). net/http.CrossOriginProtection checks
// Sec-Fetch-Site, falling back to comparing Origin with Host; requests with
// neither header (curl, tests) and safe methods (GET, HEAD, OPTIONS) pass.
func sameOriginWrites(log *slog.Logger, next http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A warning, not an error: the request was blocked as intended. A burst
		// of these means a page or extension is trying to post to the app, or a
		// reverse proxy is rewriting Host/Origin and breaking real form posts.
		log.WarnContext(r.Context(), "cross-origin write rejected",
			"http.request.method", r.Method,
			"url.path", r.URL.Path,
			"horizonte.csrf.sec_fetch_site", r.Header.Get("Sec-Fetch-Site"),
			"horizonte.csrf.has_origin", r.Header.Get("Origin") != "")
		// Counted too, so a burst can drive an alert without searching logs.
		meter(r.Context()).Count("horizonte.csrf.rejected", 1, sentry.WithAttributes(
			attribute.String("http.request.method", r.Method)))
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
	}))
	return cop.Handler(next)
}
