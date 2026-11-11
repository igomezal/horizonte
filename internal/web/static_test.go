package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"

	"github.com/igomezal/horizonte/ui"
)

func prodStatic(t *testing.T) *staticAssets {
	t.Helper()
	sub, err := fs.Sub(ui.FS, "static")
	if err != nil {
		t.Fatal(err)
	}
	a, err := sharedStaticAssets(ui.FS, sub, false) // the same instance the test servers use
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func get(h http.Handler, target string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStaticMinifiedAndCompressed(t *testing.T) {
	a := prodStatic(t)
	raw, _ := fs.ReadFile(ui.FS, "static/css/app.css")
	css := a.files["css/app.css"]
	if len(css.body) >= len(raw)*9/10 || bytes.Contains(css.body, []byte("/* ")) {
		t.Errorf("app.css not minified: %d bytes from %d", len(css.body), len(raw))
	}
	// The font in @font-face points at its versioned URL.
	font := "/static/fonts/jetbrains-mono-latin-wght.woff2?v=" + a.files["fonts/jetbrains-mono-latin-wght.woff2"].hash
	if !bytes.Contains(css.body, []byte(font)) {
		t.Errorf("app.css should reference %s", font)
	}
	rawJS, _ := fs.ReadFile(ui.FS, "static/js/charts.js")
	if js := a.files["js/charts.js"]; len(js.body) >= len(rawJS)*8/10 {
		t.Errorf("charts.js not minified: %d bytes from %d", len(js.body), len(rawJS))
	}
	// Vendor builds are served as shipped.
	rawHTMX, _ := fs.ReadFile(ui.FS, "static/vendor/htmx.min.js")
	if !bytes.Equal(a.files["vendor/htmx.min.js"].body, rawHTMX) {
		t.Error("vendor files must not be minified again")
	}

	// gzip when accepted, with its own ETag; plain otherwise.
	url := "/static/vendor/echarts.min.js?v=" + a.files["vendor/echarts.min.js"].hash
	rec := get(a, url, "Accept-Encoding", "gzip, deflate")
	if rec.Header().Get("Content-Encoding") != "gzip" || !strings.HasSuffix(rec.Header().Get("ETag"), `-gz"`) ||
		!strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("gzip response headers: %v", rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if !bytes.Equal(plain, a.files["vendor/echarts.min.js"].body) {
		t.Error("gzipped body differs from the file")
	}
	if len(a.files["vendor/echarts.min.js"].gz) > len(plain)/2 {
		t.Errorf("echarts gzip = %d bytes of %d", len(a.files["vendor/echarts.min.js"].gz), len(plain))
	}
	if rec := get(a, url); rec.Header().Get("Content-Encoding") != "" || rec.Body.Len() != len(plain) {
		t.Error("without Accept-Encoding the plain file should be sent")
	}
	if rec := get(a, url, "Accept-Encoding", "gzip;q=0"); rec.Header().Get("Content-Encoding") != "" {
		t.Error("gzip;q=0 refuses gzip")
	}
	// Already compressed formats are not gzipped.
	if a.files["fonts/jetbrains-mono-latin-wght.woff2"].gz != nil {
		t.Error("woff2 should not be gzipped")
	}
}

func TestStaticCaching(t *testing.T) {
	a := prodStatic(t)
	f := a.files["js/app.js"]

	rec := get(a, "/static/js/app.js?v="+f.hash)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Errorf("versioned: %d %v", rec.Code, rec.Header())
	}
	// Unversioned or outdated URLs revalidate, and an unchanged file is a 304.
	for _, u := range []string{"/static/js/app.js", "/static/js/app.js?v=old"} {
		rec := get(a, u)
		if rec.Header().Get("Cache-Control") != "no-cache" || rec.Header().Get("ETag") == "" {
			t.Errorf("%s: %v", u, rec.Header())
		}
		if rec := get(a, u, "If-None-Match", rec.Header().Get("ETag")); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
			t.Errorf("%s revalidation = %d", u, rec.Code)
		}
	}
	if rec := get(a, "/static/nope.js"); rec.Code != http.StatusNotFound {
		t.Errorf("missing file = %d", rec.Code)
	}
}

func TestStaticDevMode(t *testing.T) {
	sub, _ := fs.Sub(ui.FS, "static")
	a, err := newStaticAssets(sub, true)
	if err != nil {
		t.Fatal(err)
	}
	if a.URL("/static/css/app.css") != "/static/css/app.css" || a.version == "" {
		t.Errorf("dev URL = %q, version %q", a.URL("/static/css/app.css"), a.version)
	}
	rec := get(a, "/static/css/app.css")
	raw, _ := fs.ReadFile(ui.FS, "static/css/app.css")
	if rec.Header().Get("Cache-Control") != "no-cache" || !bytes.Equal(rec.Body.Bytes(), raw) {
		t.Error("dev mode serves files as they are, never cached")
	}
}

func TestAccepts(t *testing.T) {
	for in, want := range map[string]bool{
		"": false, "gzip": true, "br, gzip": true, "gzip;q=0.5": true, "gzip; q=0": false, "gzip;q=0.0": false,
		"*": true, "deflate": false, "GZIP": true,
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Encoding", in)
		if got := accepts(r, "gzip"); got != want {
			t.Errorf("accepts(%q, gzip) = %v, want %v", in, got, want)
		}
	}
}

func TestStaticBrotli(t *testing.T) {
	a := prodStatic(t)
	<-a.brReady
	f := a.files["vendor/echarts.min.js"]
	br := f.br.Load()
	if br == nil || len(*br) >= len(f.gz) {
		t.Fatalf("echarts brotli should be smaller than gzip (%d bytes)", len(f.gz))
	}
	url := "/static/vendor/echarts.min.js?v=" + f.hash
	rec := get(a, url, "Accept-Encoding", "gzip, deflate, br, zstd")
	if rec.Header().Get("Content-Encoding") != "br" || rec.Header().Get("ETag") != `"`+f.hash+`-br"` {
		t.Fatalf("brotli response headers: %v", rec.Header())
	}
	plain, err := io.ReadAll(brotli.NewReader(rec.Body))
	if err != nil || !bytes.Equal(plain, f.body) {
		t.Errorf("brotli body differs from the file (%v)", err)
	}
	if rec := get(a, url, "Accept-Encoding", "gzip, br;q=0"); rec.Header().Get("Content-Encoding") != "gzip" {
		t.Error("br;q=0 should fall back to gzip")
	}
	if rec := get(a, url, "Accept-Encoding", "br", "If-None-Match", `"`+f.hash+`-br"`); rec.Code != http.StatusNotModified {
		t.Errorf("brotli revalidation = %d", rec.Code)
	}
	if a.files["fonts/jetbrains-mono-latin-wght.woff2"].br.Load() != nil {
		t.Error("woff2 should not be brotli-compressed")
	}
}

// The jump on a results toggle (the viewport landing on the section below
// after a swap) was browser scroll anchoring; app.js switches it off for the
// length of a partial swap and hands focus back to the replaced link. The
// toggles must also stay in-place swaps of an element with an id, which is
// how the focus is found again.
func TestPartialSwapKeepsScrollAndFocus(t *testing.T) {
	js, err := fs.ReadFile(ui.FS, "static/js/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`overflowAnchor = "none"`, `overflowAnchor = ""`, "preventScroll: true", "htmx:swapError", "scrollBy"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("app.js should contain %q", want)
		}
	}
	plan, err := fs.ReadFile(ui.FS, "templates/pages/plan.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan), `hx-target="#plan-summary" hx-swap="outerHTML"`) || !strings.Contains(string(plan), `<section id="plan-summary"`) {
		t.Error("plan toggles must swap #plan-summary in place")
	}
}
