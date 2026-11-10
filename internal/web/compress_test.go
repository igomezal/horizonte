package web

import (
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
)

func decode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var r io.Reader = rec.Body
	switch rec.Header().Get("Content-Encoding") {
	case "br":
		r = brotli.NewReader(rec.Body)
	case "gzip":
		zr, err := gzip.NewReader(rec.Body)
		if err != nil {
			t.Fatalf("gzip: %v", err)
		}
		r = zr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("decode %s: %v", rec.Header().Get("Content-Encoding"), err)
	}
	return string(b)
}

func TestPagesAreCompressed(t *testing.T) {
	s := newTestServer(t)
	plain := get(s, "/plan")
	if plain.Code != 200 || plain.Header().Get("Content-Encoding") != "" {
		t.Fatalf("no Accept-Encoding: status %d, encoding %q", plain.Code, plain.Header().Get("Content-Encoding"))
	}
	if !strings.Contains(plain.Header().Get("Vary"), "Accept-Encoding") {
		t.Errorf("Vary = %q, want Accept-Encoding", plain.Header().Get("Vary"))
	}
	for _, tc := range []struct{ accept, want string }{
		{"gzip, deflate", "gzip"},
		{"gzip, deflate, br, zstd", "br"},
		{"br;q=0, gzip", "gzip"},
	} {
		rec := get(s, "/plan", "Accept-Encoding", tc.accept)
		if got := rec.Header().Get("Content-Encoding"); got != tc.want {
			t.Fatalf("%q: Content-Encoding = %q, want %q", tc.accept, got, tc.want)
		}
		if rec.Header().Get("Content-Length") != "" {
			t.Errorf("%q: Content-Length set on a compressed body", tc.accept)
		}
		if rec.Header().Get(assetHeader) == "" {
			t.Errorf("%q: %s header lost", tc.accept, assetHeader)
		}
		if body := decode(t, rec); body != plain.Body.String() {
			t.Errorf("%q: decoded body differs from the plain one (%d vs %d bytes)", tc.accept, len(body), plain.Body.Len())
		}
		if rec.Body.Len() > plain.Body.Len()/3 {
			t.Errorf("%q: %d bytes compressed from %d", tc.accept, rec.Body.Len(), plain.Body.Len())
		}
	}
}

func TestPartialsAndErrorsAreCompressed(t *testing.T) {
	s := newTestServer(t)
	for _, target := range []string{"/plan/results", "/finances/summary", "/no-such-page", "/scenarios/export"} {
		rec := get(s, target, "Accept-Encoding", "gzip")
		if rec.Header().Get("Content-Encoding") != "gzip" {
			t.Errorf("%s: status %d, Content-Encoding %q (%d bytes)", target, rec.Code, rec.Header().Get("Content-Encoding"), rec.Body.Len())
			continue
		}
		decode(t, rec)
	}
}

func TestStaticIsNotCompressedTwice(t *testing.T) {
	s := newTestServer(t)
	rec := get(s, s.render.static.URL("/static/css/app.css"), "Accept-Encoding", "gzip")
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q", rec.Header().Get("Content-Encoding"))
	}
	if v := rec.Header().Values("Vary"); len(v) != 1 {
		t.Errorf("Vary = %q, want it once", v)
	}
	if !strings.Contains(decode(t, rec), "--app-accent") {
		t.Error("not plain CSS after one gunzip")
	}
	font := get(s, s.render.static.URL("/static/fonts/jetbrains-mono-latin-wght.woff2"), "Accept-Encoding", "gzip")
	if font.Code == 200 && font.Header().Get("Content-Encoding") != "" {
		t.Errorf("font compressed: %q", font.Header().Get("Content-Encoding"))
	}
}

func TestCompressSkips(t *testing.T) {
	small := strings.Repeat("a", compressMinSize-1)
	big := strings.Repeat("hello world ", 500)
	cases := []struct {
		name   string
		method string
		h      http.HandlerFunc
		want   string // expected Content-Encoding
	}{
		{"small body", "GET", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, small)
		}, ""},
		{"big body in small writes", "GET", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			for i := 0; i < 50; i++ {
				io.WriteString(w, big[:100])
			}
		}, "gzip"},
		{"image", "GET", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			io.WriteString(w, big)
		}, ""},
		{"already encoded", "GET", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Encoding", "identity")
			io.WriteString(w, big)
		}, "identity"},
		{"no-transform", "GET", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Cache-Control", "no-store, no-transform")
			io.WriteString(w, big)
		}, ""},
		{"not modified", "GET", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotModified)
		}, ""},
		{"HEAD", "HEAD", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, big)
		}, ""},
		{"sniffed type", "GET", func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "<!doctype html>"+big)
		}, "gzip"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		compressResponses(tc.h).ServeHTTP(rec, req)
		if got := rec.Header().Get("Content-Encoding"); got != tc.want {
			t.Errorf("%s: Content-Encoding = %q, want %q", tc.name, got, tc.want)
		}
		if tc.want == "gzip" && !strings.Contains(decode(t, rec), "hello world") {
			t.Errorf("%s: body lost", tc.name)
		}
	}
}

func TestCompressStatusAndFlush(t *testing.T) {
	h := compressResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, "<p>first</p>")
		http.NewResponseController(w).Flush()
		io.WriteString(w, strings.Repeat("<p>more</p>", 200))
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := httptest.NewRecorder()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	logRequests(logger, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !rec.Flushed {
		t.Fatalf("status %d, flushed %v", rec.Code, rec.Flushed)
	}
	if rec.Header().Get("Content-Encoding") != "br" {
		t.Errorf("Content-Encoding = %q", rec.Header().Get("Content-Encoding"))
	}
	if !strings.HasPrefix(decode(t, rec), "<p>first</p><p>more</p>") {
		t.Errorf("body mangled")
	}
}
