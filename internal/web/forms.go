package web

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/i18n"
)

const (
	errNumber = "form.error.number"
	errMonth  = "form.error.month"
)

// formParser reads typed values from a submitted form, collecting field errors.
type formParser struct {
	lang i18n.Lang
	v    url.Values
	errs domain.Errors
}

func newFormParser(lang i18n.Lang, v url.Values) *formParser {
	return &formParser{lang: lang, v: v, errs: domain.Errors{}}
}

// str returns the trimmed value, dropping any invalid UTF-8 bytes so broken
// text never reaches the database.
func (p *formParser) str(name string) string {
	return strings.TrimSpace(strings.ToValidUTF8(p.v.Get(name), ""))
}

// money parses a euro amount. Empty is 0 unless required.
func (p *formParser) money(name string, required bool) domain.Cents {
	s := p.str(name)
	if s == "" {
		if required {
			p.errs.Add(name, domain.ErrRequired)
		}
		return 0
	}
	f, err := i18n.ParseDecimal(p.lang, s)
	if err != nil {
		p.errs.Add(name, errNumber)
		return 0
	}
	return domain.FromEuros(f)
}

// optPercent parses a percentage ("3,5" → 0.035). Empty returns nil.
func (p *formParser) optPercent(name string) *float64 {
	s := p.str(name)
	if s == "" {
		return nil
	}
	f, err := i18n.ParseDecimal(p.lang, s)
	if err != nil {
		p.errs.Add(name, errNumber)
		return nil
	}
	r := f / 100
	return &r
}

// percent parses a required percentage.
func (p *formParser) percent(name string) float64 {
	if p.str(name) == "" {
		p.errs.Add(name, domain.ErrRequired)
		return 0
	}
	if r := p.optPercent(name); r != nil {
		return *r
	}
	return 0
}

func (p *formParser) integer(name string) int {
	s := p.str(name)
	if s == "" {
		p.errs.Add(name, domain.ErrRequired)
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		p.errs.Add(name, errNumber)
	}
	return n
}

func (p *formParser) month(name string) domain.Month {
	m, err := domain.ParseMonth(p.str(name))
	if err != nil {
		p.errs.Add(name, errMonth)
	}
	return m
}

// owner parses a person ID; empty means joint (nil).
func (p *formParser) owner(name string) *int64 {
	s := p.str(name)
	if s == "" {
		return nil
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		p.errs.Add(name, domain.ErrChoice)
		return nil
	}
	return &id
}

// boundary parses a stream start or end: a milestone select (msField) that,
// when set, replaces the fixed month (monthField).
func (p *formParser) boundary(msField, monthField string) (*int64, domain.Month) {
	if ms := p.owner(msField); ms != nil {
		return ms, domain.Month{}
	}
	return nil, p.month(monthField)
}

// decimal parses a required plain number typed in the interface language.
func (p *formParser) decimal(name string) float64 {
	s := p.str(name)
	if s == "" {
		p.errs.Add(name, domain.ErrRequired)
		return 0
	}
	f, err := i18n.ParseDecimal(p.lang, s)
	if err != nil {
		p.errs.Add(name, errNumber)
	}
	return f
}

// merge adds domain validation errors without overwriting parse errors.
func (p *formParser) merge(errs domain.Errors) {
	for k, v := range errs {
		p.errs.Add(k, v)
	}
}

// ---------- formatting values back into inputs ----------

func inMoney(lang i18n.Lang, c domain.Cents) string {
	return i18n.FormatInput(lang, c.Euros(), 2)
}

func inPercent(lang i18n.Lang, r float64) string {
	return i18n.FormatInput(lang, r*100, 2)
}

func inOptPercent(lang i18n.Lang, r *float64) string {
	if r == nil {
		return ""
	}
	return inPercent(lang, *r)
}

func inOwner(id *int64) string {
	if id == nil {
		return ""
	}
	return strconv.FormatInt(*id, 10)
}
