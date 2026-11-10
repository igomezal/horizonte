package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/igomezal/horizonte/internal/i18n"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.SchemaVersion(r.Context())
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "ok schema=%d\n", v)
}

// demoData is a compound-interest example shown on the home page until the
// real projection engine exists. It also exercises the chart pipeline.
type demoData struct {
	Initial, Monthly, Rate float64
	Years                  int
	Final, Contributed     float64
	Chart                  stackedChart
}

// stackedChart is the JSON contract read by ui/static/js/charts.js.
type stackedChart struct {
	Type       string        `json:"type"`
	Categories []string      `json:"categories"`
	Series     []chartSeries `json:"series"`
	// HideLegend is set when the page explains the colors itself (e.g. swatches in a sentence).
	HideLegend bool   `json:"hideLegend,omitempty"`
	TotalLabel string `json:"totalLabel,omitempty"` // first row of the tooltip
}

type chartSeries struct {
	Name string    `json:"name"`
	Data []float64 `json:"data"`
}

func compoundDemo(lang i18n.Lang, catalog *i18n.Catalog) demoData {
	d := demoData{Initial: 10000, Monthly: 300, Rate: 0.07, Years: 30}
	monthlyRate := d.Rate / 12

	balance, contributed := d.Initial, d.Initial
	contribSeries := []float64{contributed}
	growthSeries := []float64{0}
	cats := []string{"0"}
	for m := 1; m <= d.Years*12; m++ {
		balance = balance*(1+monthlyRate) + d.Monthly
		contributed += d.Monthly
		if m%12 == 0 {
			cats = append(cats, fmt.Sprint(m/12))
			contribSeries = append(contribSeries, round2(contributed))
			growthSeries = append(growthSeries, round2(balance-contributed))
		}
	}
	d.Final, d.Contributed = balance, contributed
	d.Chart = stackedChart{
		Type:       "stacked-area",
		HideLegend: true,
		TotalLabel: catalog.T(lang, "chart.total"),
		Categories: cats,
		Series: []chartSeries{
			{Name: catalog.T(lang, "demo.contributed"), Data: contribSeries},
			{Name: catalog.T(lang, "demo.growth"), Data: growthSeries},
		},
	}
	return d
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// handleSetLang stores the chosen language in a cookie and returns the user
// to the page they were on.
func (s *Server) handleSetLang(w http.ResponseWriter, r *http.Request) {
	lang, ok := i18n.Parse(r.FormValue("lang"))
	if !ok {
		http.Error(w, "unsupported language", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     i18n.CookieName,
		Value:    string(lang),
		Path:     "/",
		Expires:  time.Now().AddDate(1, 0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	target := safeRedirect(r.FormValue("next"))
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// safeRedirect only allows local absolute paths, preventing open redirects.
func safeRedirect(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.IsAbs() || u.Host != "" {
		return "/"
	}
	return u.RequestURI()
}

type errorData struct {
	TitleKey, BodyKey string
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, http.StatusNotFound, "error", "error.notfound.title", errorData{
		TitleKey: "error.notfound.title", BodyKey: "error.notfound.body",
	})
}
