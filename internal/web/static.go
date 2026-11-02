package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/js"
	"github.com/tdewolff/minify/v2/svg"
)

// staticAssets serves ui/static.
//
// In production every file is prepared once at startup and kept in memory:
// our own CSS, JavaScript and SVG are minified (vendor files ship minified),
// url() references inside CSS get their version, text files are gzipped in
// advance, and each file gets a hash of its served content. Brotli (about
// 15 % smaller than gzip) is compressed at its best level in the
// background after startup, because that takes seconds; until it is ready
// browsers get gzip. Pages link to
// /static/x?v=<hash> (see URL), which browsers may keep for a year: a new
// build changes the hash and so the URL. Unversioned or outdated URLs still
// work, revalidated with an ETag so an unchanged file costs a 304.
//
// In development files are read from disk on every request, unminified and
// never cached, so edits show up on a reload.
type staticAssets struct {
	dev     bool
	fsys    fs.FS // the static/ directory
	files   map[string]*staticFile
	version string // hash of every file, sent as X-Horizonte-Assets
	handler http.Handler
	brReady chan struct{} // closed once the brotli versions are done
}

type staticFile struct {
	body  []byte                 // as served (minified in production)
	gz    []byte                 // gzipped body, nil when it doesn't pay off
	br    atomic.Pointer[[]byte] // brotli body, set in the background; nil until then or when it doesn't pay off
	ctype string
	hash  string
}

// brotliLevel is brotli's best (and slowest) level: the files are compressed
// once, in the background, so the extra few percent are free.
const brotliLevel = 11

// Files compressed with gzip when it saves something (fonts and images
// other than SVG are compressed already).
var compressible = map[string]bool{".css": true, ".js": true, ".svg": true, ".txt": true, ".json": true, ".html": true}

func newStaticAssets(fsys fs.FS, dev bool) (*staticAssets, error) {
	a := &staticAssets{dev: dev, fsys: fsys, files: map[string]*staticFile{}}
	var paths []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, p)
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("web: static assets: %w", err)
	}
	sort.Strings(paths)

	if dev {
		a.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Always revalidate in dev so CSS/JS edits show up on a normal reload.
			w.Header().Set("Cache-Control", "no-cache")
			http.StripPrefix("/static/", http.FileServerFS(fsys)).ServeHTTP(w, r)
		})
		h := sha256.New()
		for _, p := range paths {
			b, err := fs.ReadFile(fsys, p)
			if err != nil {
				return nil, fmt.Errorf("web: static assets: %w", err)
			}
			h.Write([]byte(p))
			h.Write(b)
		}
		a.version = hex.EncodeToString(h.Sum(nil))[:12]
		return a, nil
	}

	m := minify.New()
	m.AddFunc("text/css", css.Minify)
	m.AddFunc("text/javascript", js.Minify)
	m.AddFunc("image/svg+xml", svg.Minify)

	// CSS last: its url()s need the hashes of the files they point to.
	isCSS := func(p string) bool { return path.Ext(p) == ".css" }
	sort.SliceStable(paths, func(i, j int) bool { return !isCSS(paths[i]) && isCSS(paths[j]) })
	for _, p := range paths {
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("web: static assets: %w", err)
		}
		ext := path.Ext(p)
		f := &staticFile{ctype: mime.TypeByExtension(ext)}
		if f.ctype == "" {
			f.ctype = http.DetectContentType(b)
		}
		if ext == ".css" {
			b = a.versionCSSURLs(b)
		}
		if mt := minifyType(p); mt != "" {
			out, err := m.Bytes(mt, b)
			if err != nil {
				return nil, fmt.Errorf("web: minify %s: %w", p, err)
			}
			b = out
		}
		f.body = b
		sum := sha256.Sum256(b)
		f.hash = hex.EncodeToString(sum[:])[:12]
		if compressible[ext] {
			if gz := gzipBytes(b); len(gz) < len(b)*9/10 {
				f.gz = gz
			}
		}
		a.files[p] = f
	}

	h := sha256.New()
	for _, p := range paths {
		h.Write([]byte(p))
		h.Write([]byte(a.files[p].hash))
	}
	a.version = hex.EncodeToString(h.Sum(nil))[:12]
	a.handler = http.HandlerFunc(a.serve)
	a.brReady = make(chan struct{})
	go a.compressBrotli()
	return a, nil
}

// compressBrotli adds the brotli version of every gzipped file, using half
// the CPUs so the server stays responsive while it runs.
func (a *staticAssets) compressBrotli() {
	defer close(a.brReady)
	jobs := make(chan *staticFile)
	var wg sync.WaitGroup
	for range max(1, runtime.GOMAXPROCS(0)/2) {
		wg.Go(func() {
			for f := range jobs {
				if br := brotliBytes(f.body); len(br) < len(f.gz) {
					f.br.Store(&br)
				}
			}
		})
	}
	for _, f := range a.files {
		if f.gz != nil {
			jobs <- f
		}
	}
	close(jobs)
	wg.Wait()
}

func brotliBytes(b []byte) []byte {
	var buf bytes.Buffer
	w := brotli.NewWriterLevel(&buf, brotliLevel)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

// Prepared assets by source, so servers built from the same embedded files
// (tests build many) prepare them once.
var (
	staticMu    sync.Mutex
	staticCache = map[any]*staticAssets{}
)

// sharedStaticAssets is newStaticAssets, memoized by the source file system
// in production (development reads from disk on every request anyway).
func sharedStaticAssets(key any, fsys fs.FS, dev bool) (*staticAssets, error) {
	if dev || key == nil || !reflect.TypeOf(key).Comparable() {
		return newStaticAssets(fsys, dev)
	}
	staticMu.Lock()
	defer staticMu.Unlock()
	if a, ok := staticCache[key]; ok {
		return a, nil
	}
	a, err := newStaticAssets(fsys, dev)
	if err == nil {
		staticCache[key] = a
	}
	return a, err
}

// minifyType is the media type to minify p as, or "" to serve it as is.
func minifyType(p string) string {
	if strings.Contains(path.Base(p), ".min.") {
		return "" // vendor builds are minified already
	}
	switch path.Ext(p) {
	case ".css":
		return "text/css"
	case ".js":
		return "text/javascript"
	case ".svg":
		return "image/svg+xml"
	}
	return ""
}

var cssURL = regexp.MustCompile(`url\(\s*(["']?)(/static/[^"')?#]+)(["']?)\s*\)`)

// versionCSSURLs adds the version to url(/static/…) references (fonts).
func (a *staticAssets) versionCSSURLs(b []byte) []byte {
	return cssURL.ReplaceAllFunc(b, func(m []byte) []byte {
		g := cssURL.FindSubmatch(m)
		return []byte("url(" + string(g[1]) + a.URL(string(g[2])) + string(g[3]) + ")")
	})
}

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

// URL returns the URL to link a static file with: /static/x?v=<hash> in
// production, the plain path in development (never cached there).
func (a *staticAssets) URL(p string) string {
	if a.dev {
		return p
	}
	if f, ok := a.files[strings.TrimPrefix(p, "/static/")]; ok {
		return p + "?v=" + f.hash
	}
	return p
}

func (a *staticAssets) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.handler.ServeHTTP(w, r) }

func (a *staticAssets) serve(w http.ResponseWriter, r *http.Request) {
	f, ok := a.files[strings.TrimPrefix(r.URL.Path, "/static/")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	if v := r.URL.Query().Get("v"); v != "" && v == f.hash {
		// This URL always has this content: keep it for good.
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	body, etag := f.body, `"`+f.hash+`"`
	if f.gz != nil {
		h.Add("Vary", "Accept-Encoding")
		if br := f.br.Load(); br != nil && accepts(r, "br") {
			body, etag = *br, `"`+f.hash+`-br"`
			h.Set("Content-Encoding", "br")
		} else if accepts(r, "gzip") {
			body, etag = f.gz, `"`+f.hash+`-gz"`
			h.Set("Content-Encoding", "gzip")
		}
		if h.Get("Content-Encoding") != "" {
			r.Header.Del("Range") // byte ranges of a compressed body make no sense
		}
	}
	h.Set("ETag", etag)
	// ServeContent answers If-None-Match with 304 and handles HEAD.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}

// accepts reports whether the request's Accept-Encoding allows enc
// ("gzip", "gzip;q=0.8", "*"; not "gzip;q=0").
func accepts(r *http.Request, enc string) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.TrimSpace(name)
		if !strings.EqualFold(name, enc) && name != "*" {
			continue
		}
		q := 1.0
		if v, ok := strings.CutPrefix(strings.ReplaceAll(params, " ", ""), "q="); ok {
			if _, err := fmt.Sscanf(v, "%g", &q); err != nil {
				q = 1
			}
		}
		return q > 0
	}
	return false
}
