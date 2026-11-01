package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
	"github.com/igomezal/horizonte/ui"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
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
