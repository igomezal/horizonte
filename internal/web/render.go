package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
)

// renderer parses one template set per page: the layout, all partials and the page.
type renderer struct {
	fsys    fs.FS
	catalog *i18n.Catalog
	dev     bool // re-parse on every render so template edits apply immediately
	sentry  BrowserSentry
	// assets identifies this build's static files (a hash of their contents).
	// Static URLs carry it (?v=…) so browsers can cache them for good, and
	// every response sends it (X-Horizonte-Assets) so a tab opened before a
	// deploy notices and reloads instead of mixing new HTML with old CSS/JS.
	assets string

	mu    sync.Mutex
	pages map[string]*template.Template
}

func newRenderer(fsys fs.FS, catalog *i18n.Catalog, dev bool, snt BrowserSentry) (*renderer, error) {
	r := &renderer{fsys: fsys, catalog: catalog, dev: dev, sentry: snt}
	var err error
	if r.assets, err = assetsVersion(fsys); err != nil {
		return nil, err
	}
	if err := r.parseAll(); err != nil {
		return nil, err
	}
	return r, nil
}

// assetsVersion hashes every file under static/.
func assetsVersion(fsys fs.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(fsys, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		h.Write([]byte(p))
		h.Write(b)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("web: hash static assets: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

// assetHeader is the response header carrying renderer.assets.
const assetHeader = "X-Horizonte-Assets"

// baseFuncs are the functions available to every template. Language-dependent
// functions are placeholders here and are rebound per request in bind.
func (r *renderer) baseFuncs() template.FuncMap {
	return template.FuncMap{
		"t":       func(key string, args ...any) string { return key },
		"money":   func(v float64) string { return "" },
		"money2":  func(v float64) string { return "" },
		"percent": func(v float64, decimals int) string { return "" },
		"number":  func(v float64, decimals int) string { return "" },
		"sub":     func(a, b float64) float64 { return a - b },
		"eur":     func(c domain.Cents) string { return "" },
		"eur2":    func(c domain.Cents) string { return "" },
		"pct":     func(r float64) string { return "" },
		"month":   func(m domain.Month) string { return "" },
		"neg":     func(c domain.Cents) bool { return c < 0 },
		"float":   func(n int) float64 { return float64(n) },
		"list":    func(v ...string) []string { return v },
		"str":     func(v any) string { return fmt.Sprint(v) },
		// asset adds this build's version to a static URL.
		"asset":  func(p string) string { return p + "?v=" + r.assets },
		"assets": func() string { return r.assets },
		// dict builds a map from key/value pairs so a template can pass several
		// named arguments to another template: {{template "field" (dict "F" . "name" "x")}}.
		"dict": func(kv ...any) (map[string]any, error) {
			if len(kv)%2 != 0 {
				return nil, fmt.Errorf("dict: odd number of arguments")
			}
			m := make(map[string]any, len(kv)/2)
			for i := 0; i < len(kv); i += 2 {
				k, ok := kv[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: key %v is not a string", kv[i])
				}
				m[k] = kv[i+1]
			}
			return m, nil
		},
		"json": func(v any) (template.JS, error) {
			b, err := json.Marshal(v)
			return template.JS(b), err
		},
	}
}

func (r *renderer) bind(t *template.Template, lang i18n.Lang) (*template.Template, error) {
	c, err := t.Clone()
	if err != nil {
		return nil, err
	}
	return c.Funcs(template.FuncMap{
		"t":       func(key string, args ...any) string { return r.catalog.T(lang, key, args...) },
		"money":   func(v float64) string { return i18n.FormatMoney(lang, v, 0) },
		"money2":  func(v float64) string { return i18n.FormatMoney(lang, v, 2) },
		"percent": func(v float64, decimals int) string { return i18n.FormatPercent(lang, v, decimals) },
		"number":  func(v float64, decimals int) string { return i18n.FormatNumber(lang, v, decimals) },
		"eur":     func(c domain.Cents) string { return i18n.FormatMoney(lang, c.Euros(), 0) },
		"eur2": func(c domain.Cents) string { // cents only when not a whole amount
			if c%100 == 0 {
				return i18n.FormatMoney(lang, c.Euros(), 0)
			}
			return i18n.FormatMoney(lang, c.Euros(), 2)
		},
		"pct":   func(r float64) string { return i18n.FormatPercentShort(lang, r) },
		"month": func(m domain.Month) string { return i18n.FormatMonth(lang, m.Year, m.Month) },
	}), nil
}

func (r *renderer) parseAll() error {
	pageFiles, err := fs.Glob(r.fsys, "templates/pages/*.html")
	if err != nil {
		return err
	}
	pages := make(map[string]*template.Template, len(pageFiles))
	for _, pf := range pageFiles {
		name := strings.TrimSuffix(path.Base(pf), ".html")
		t, err := template.New(name).Funcs(r.baseFuncs()).ParseFS(r.fsys,
			"templates/layout.html", "templates/partials/*.html", pf)
		if err != nil {
			return fmt.Errorf("parse page %s: %w", name, err)
		}
		pages[name] = t
	}
	r.mu.Lock()
	r.pages = pages
	r.mu.Unlock()
	return nil
}

func (r *renderer) lookup(page string) (*template.Template, error) {
	if r.dev {
		if err := r.parseAll(); err != nil {
			return nil, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.pages[page]
	if !ok {
		return nil, fmt.Errorf("unknown page %q", page)
	}
	return t, nil
}

// pageData is passed to every template as the root value.
type pageData struct {
	Lang     i18n.Lang
	Langs    []i18n.Lang
	Path     string // current request path, for nav highlighting
	TitleKey string // i18n key of the page title
	Data     any    // page-specific data
	// Plans and CurrentPlan feed the plan switcher in the top bar.
	Plans       []store.Plan
	CurrentPlan int64
	// Sentry configures the browser SDK; the script is omitted when DSN is empty.
	Sentry BrowserSentry
}

// render executes block of page into a buffer first, so a template error never
// sends a half-written page. Full pages pass a pageData (its language and path
// are filled in here); partials pass their own view as the template root.
func (r *renderer) render(w http.ResponseWriter, req *http.Request, status int, page, block string, data any) error {
	t, err := r.lookup(page)
	if err != nil {
		return err
	}
	lang := i18n.FromContext(req.Context())
	t, err = r.bind(t, lang)
	if err != nil {
		return err
	}
	if pd, ok := data.(pageData); ok {
		pd.Lang = lang
		pd.Langs = i18n.Supported
		pd.Path = req.URL.Path
		pd.Sentry = r.sentry
		data = pd
	}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, block, data); err != nil {
		return fmt.Errorf("execute %s/%s: %w", page, block, err)
	}
	if status == http.StatusUnprocessableEntity {
		// Every form that comes back with validation errors passes here.
		// Counted per route: a form that keeps getting rejected points to
		// confusing fields or a parsing bug (e.g. a number format). Field
		// values are never recorded.
		meter(req.Context()).Count("horizonte.form.rejected", 1, sentry.WithAttributes(
			attribute.String("http.route", req.Pattern)))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set(assetHeader, r.assets)
	w.WriteHeader(status)
	_, err = buf.WriteTo(w)
	return err
}
