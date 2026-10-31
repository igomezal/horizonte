package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/igomezal/horizonte/internal/i18n"
)

// renderer parses one template set per page: the layout, all partials and the page.
type renderer struct {
	fsys    fs.FS
	catalog *i18n.Catalog
	dev     bool // re-parse on every render so template edits apply immediately

	mu    sync.Mutex
	pages map[string]*template.Template
}

func newRenderer(fsys fs.FS, catalog *i18n.Catalog, dev bool) (*renderer, error) {
	r := &renderer{fsys: fsys, catalog: catalog, dev: dev}
	if err := r.parseAll(); err != nil {
		return nil, err
	}
	return r, nil
}

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
}

// render executes block ("layout" for full pages, or a named partial for htmx
// requests) of page into a buffer first, so a template error never sends a
// half-written page.
func (r *renderer) render(w http.ResponseWriter, req *http.Request, status int, page, block string, pd pageData) error {
	t, err := r.lookup(page)
	if err != nil {
		return err
	}
	lang := i18n.FromContext(req.Context())
	t, err = r.bind(t, lang)
	if err != nil {
		return err
	}
	pd.Lang = lang
	pd.Langs = i18n.Supported
	pd.Path = req.URL.Path

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, block, pd); err != nil {
		return fmt.Errorf("execute %s/%s: %w", page, block, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err = buf.WriteTo(w)
	return err
}
