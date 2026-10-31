// Package i18n provides message catalogs and locale-aware formatting
// for the supported UI languages (Spanish by default, English).
package i18n

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
)

// Lang is a supported UI language code.
type Lang string

const (
	ES Lang = "es"
	EN Lang = "en"

	// Default is the language used when none is selected.
	Default = ES

	// CookieName stores the user's language choice.
	CookieName = "lang"
)

// Supported lists the available languages in display order.
var Supported = []Lang{ES, EN}

// Parse returns the Lang for code, or false if it is not supported.
func Parse(code string) (Lang, bool) {
	code = strings.ToLower(strings.TrimSpace(code))
	for _, l := range Supported {
		if string(l) == code {
			return l, true
		}
	}
	return "", false
}

//go:embed locales/*.json
var localeFS embed.FS

// Catalog holds the translated messages for every supported language.
type Catalog struct {
	messages map[Lang]map[string]string
}

// Load reads the embedded locale files. Every supported language must have a file.
func Load() (*Catalog, error) {
	c := &Catalog{messages: make(map[Lang]map[string]string, len(Supported))}
	for _, l := range Supported {
		data, err := localeFS.ReadFile(path.Join("locales", string(l)+".json"))
		if err != nil {
			return nil, fmt.Errorf("i18n: read %s catalog: %w", l, err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("i18n: parse %s catalog: %w", l, err)
		}
		c.messages[l] = m
	}
	return c, nil
}

// T translates key into lang. Extra args are applied with fmt.Sprintf.
// Missing keys fall back to the default language, then to the key itself,
// so a missing translation is visible but never breaks a page.
func (c *Catalog) T(lang Lang, key string, args ...any) string {
	msg, ok := c.messages[lang][key]
	if !ok {
		msg, ok = c.messages[Default][key]
	}
	if !ok {
		return key
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

// Keys returns the message keys of lang (used by tests to check catalog parity).
func (c *Catalog) Keys(lang Lang) []string {
	keys := make([]string, 0, len(c.messages[lang]))
	for k := range c.messages[lang] {
		keys = append(keys, k)
	}
	return keys
}

type ctxKey struct{}

// WithLang returns a copy of ctx carrying lang.
func WithLang(ctx context.Context, lang Lang) context.Context {
	return context.WithValue(ctx, ctxKey{}, lang)
}

// FromContext returns the request language, or Default.
func FromContext(ctx context.Context) Lang {
	if l, ok := ctx.Value(ctxKey{}).(Lang); ok {
		return l
	}
	return Default
}

// Middleware resolves the request language from the lang cookie.
// Without a valid cookie the default language (Spanish) is used.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := Default
		if ck, err := r.Cookie(CookieName); err == nil {
			if l, ok := Parse(ck.Value); ok {
				lang = l
			}
		}
		next.ServeHTTP(w, r.WithContext(WithLang(r.Context(), lang)))
	})
}
