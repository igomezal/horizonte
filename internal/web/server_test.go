package web

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
	"github.com/igomezal/horizonte/ui"
)

func newTestServer(t *testing.T) *Server {
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
	s, err := New(Config{
		Store: st, Catalog: cat, Assets: ui.FS,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.StopWarm) // runs before st.Close above
	return s
}

func do(s *Server, req *http.Request) (*httptest.ResponseRecorder, string) {
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec, rec.Body.String()
}

func TestHomeDefaultsToSpanish(t *testing.T) {
	s := newTestServer(t)
	rec, body := do(s, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{`<html lang="es">`, "Bienvenido", "Finanzas actuales", `id="demo-chart"`} {
		if !strings.Contains(body, want) {
			t.Errorf("home body missing %q", want)
		}
	}
}

func TestHomeInEnglishViaCookie(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: "en"})
	_, body := do(s, req)
	for _, want := range []string{`<html lang="en">`, "Welcome", "Current finances", "€"} {
		if !strings.Contains(body, want) {
			t.Errorf("home body missing %q", want)
		}
	}
}

func TestHTMXPartialRendersContentOnly(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("HX-Request", "true")
	_, body := do(s, req)
	if strings.Contains(body, "<html") {
		t.Error("htmx partial should not include the layout")
	}
	if !strings.Contains(body, "Lo que hacen") {
		t.Error("htmx partial missing page content")
	}
}

func TestSetLang(t *testing.T) {
	s := newTestServer(t)
	form := url.Values{"lang": {"en"}, "next": {"/some/page"}}
	req := httptest.NewRequest("POST", "/lang", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec, _ := do(s, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/some/page" {
		t.Errorf("Location = %q", loc)
	}
	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == i18n.CookieName && c.Value == "en" {
			found = true
		}
	}
	if !found {
		t.Error("lang cookie not set")
	}

	// Unsupported language.
	req = httptest.NewRequest("POST", "/lang", strings.NewReader("lang=fr"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec, _ := do(s, req); rec.Code != http.StatusBadRequest {
		t.Errorf("unsupported lang status = %d", rec.Code)
	}
}

func TestCrossOriginWriteRejected(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/lang", strings.NewReader("lang=en"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if rec, _ := do(s, req); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site status = %d, want 403", rec.Code)
	}

	req = httptest.NewRequest("POST", "/lang", strings.NewReader("lang=en"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example")
	if rec, _ := do(s, req); rec.Code != http.StatusForbidden {
		t.Errorf("foreign origin status = %d, want 403", rec.Code)
	}
}

func TestSafeRedirect(t *testing.T) {
	cases := map[string]string{
		"":                     "/",
		"/plan?x=1":            "/plan?x=1",
		"//evil.example":       "/",
		"/\\evil.example":      "/",
		"https://evil.example": "/",
		"plan":                 "/",
	}
	for in, want := range cases {
		if got := safeRedirect(in); got != want {
			t.Errorf("safeRedirect(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNotFoundAndStaticAndHealth(t *testing.T) {
	s := newTestServer(t)
	if rec, body := do(s, httptest.NewRequest("GET", "/nope", nil)); rec.Code != http.StatusNotFound ||
		!strings.Contains(body, "Página no encontrada") {
		t.Errorf("404 page: status %d", rec.Code)
	}
	for _, p := range []string{"/static/vendor/htmx.min.js", "/static/vendor/echarts.min.js", "/static/css/app.css"} {
		if rec, _ := do(s, httptest.NewRequest("GET", p, nil)); rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d", p, rec.Code)
		}
	}
	if rec, body := do(s, httptest.NewRequest("GET", "/healthz", nil)); rec.Code != http.StatusOK ||
		!strings.HasPrefix(body, "ok") {
		t.Errorf("healthz: %d %q", rec.Code, body)
	}
}

func TestCompoundDemo(t *testing.T) {
	cat, _ := i18n.Load()
	d := compoundDemo(i18n.ES, cat)
	if len(d.Chart.Categories) != d.Years+1 {
		t.Errorf("categories = %d, want %d", len(d.Chart.Categories), d.Years+1)
	}
	wantContrib := d.Initial + d.Monthly*12*float64(d.Years)
	if d.Contributed != wantContrib {
		t.Errorf("contributed = %v, want %v", d.Contributed, wantContrib)
	}
	// 10k + 300/month at 7% for 30y, monthly compounding ≈ 446k.
	if d.Final < 440000 || d.Final > 450000 {
		t.Errorf("final = %v, out of expected range", d.Final)
	}
}

func TestStaticAssetsAreVersioned(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedMC(t, s)
	st := s.render.static
	if len(st.version) != 12 {
		t.Fatalf("assets version = %q", st.version)
	}

	rec, body := do(s, httptest.NewRequest("GET", "/success", nil))
	if rec.Header().Get(assetHeader) != st.version {
		t.Errorf("page header %s = %q, want %q", assetHeader, rec.Header().Get(assetHeader), st.version)
	}
	if !strings.Contains(body, `<meta name="horizonte-assets" content="`+st.version+`">`) {
		t.Error("page should carry the assets version")
	}
	// Every static link carries its own file's hash.
	for _, p := range []string{"css/app.css", "js/app.js", "vendor/htmx.min.js", "fonts/jetbrains-mono-latin-wght.woff2", "img/favicon.svg"} {
		want := "/static/" + p + "?v=" + st.files[p].hash
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// Partials carry the set's version too, so a stale tab notices on its next htmx request.
	if rec, _ := do(s, htmxReq("GET", "/success/results", nil)); rec.Header().Get(assetHeader) != st.version {
		t.Error("partials should send the assets version")
	}
}
func TestBusyOverlayHiddenWithoutCSS(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedMC(t, s)
	_, body := do(s, htmxReq("GET", "/success/results", nil))
	if !strings.Contains(body, `<div class="sim-busy" hidden>`) || !strings.Contains(body, `<span class="when-busy" hidden>`) {
		t.Error("loading labels must be hidden by markup, not only by CSS")
	}
}

func TestContentSecurityPolicy(t *testing.T) {
	s := newTestServer(t)
	rec, body := do(s, httptest.NewRequest("GET", "/", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self' 'unsafe-eval'", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q missing %q", csp, want)
		}
	}
	if strings.Contains(csp, "worker-src") {
		t.Errorf("CSP without Sentry allows workers: %q", csp)
	}
	if strings.Contains(body, `src="/static/vendor/echarts.min.js`) {
		t.Error("ECharts should be loaded on demand, not by the layout")
	}

	got := contentSecurityPolicy("https://key@o1.ingest.de.sentry.io/42")
	for _, want := range []string{"connect-src 'self' https://o1.ingest.de.sentry.io", "worker-src 'self' blob:"} {
		if !strings.Contains(got, want) {
			t.Errorf("CSP with Sentry %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "key@") {
		t.Errorf("CSP leaks the DSN key: %q", got)
	}
}

// The CSP blocks inline scripts, so these would silently stop working.
func TestTemplatesHaveNoInlineScripts(t *testing.T) {
	scriptTag := regexp.MustCompile(`<script\b[^>]*>`)
	handler := regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	err := fs.WalkDir(ui.FS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(ui.FS, p)
		if err != nil {
			return err
		}
		for _, tag := range scriptTag.FindAllString(string(b), -1) {
			if !strings.Contains(tag, " src=") && !strings.Contains(tag, `type="application/json"`) {
				t.Errorf("%s: inline script %s", p, tag)
			}
		}
		if m := handler.FindString(string(b)); m != "" {
			t.Errorf("%s: inline event handler %q", p, m)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
