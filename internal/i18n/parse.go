package i18n

import (
	"errors"
	"strconv"
	"strings"
)

// ErrInvalidNumber is returned when user input is not a number.
var ErrInvalidNumber = errors.New("invalid number")

// ParseDecimal parses a number typed by the user in lang's conventions.
// It accepts thousands separators, a currency or percent sign and spaces:
//
//	es: "1.234,56", "1234,56", "1234.56", "1.234", "12,5 %", "1.500 €"
//	en: "1,234.56", "1234.56", "1,234", "12.5%", "€1,500"
//
// A lone separator followed by exactly three digits ("1.234" in es, "1,234" in
// en) is read as a thousands separator; otherwise it is the decimal mark.
func ParseDecimal(lang Lang, s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("€", "", "%", "", " ", "", " ", "", " ", "").Replace(s)
	if s == "" {
		return 0, ErrInvalidNumber
	}
	thousands, decimal := separators(lang)

	switch {
	case strings.Contains(s, decimal) && strings.Contains(s, thousands):
		// Both present: the locale's convention is unambiguous.
		s = strings.ReplaceAll(s, thousands, "")
		s = strings.Replace(s, decimal, ".", 1)
	case strings.Contains(s, decimal):
		s = strings.Replace(s, decimal, ".", 1)
	case strings.Contains(s, thousands):
		// Only the locale's thousands mark: thousands if every group has 3 digits,
		// otherwise the user typed the other convention's decimal mark.
		parts := strings.Split(s, thousands)
		grouped := len(parts) > 1
		for _, p := range parts[1:] {
			if len(p) != 3 {
				grouped = false
			}
		}
		if grouped {
			s = strings.Join(parts, "")
		} else if len(parts) == 2 {
			s = parts[0] + "." + parts[1]
		} else {
			return 0, ErrInvalidNumber
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, ErrInvalidNumber
	}
	return v, nil
}

// FormatInput formats v for an editable text field: no thousands separator,
// lang's decimal mark, and trailing zeros trimmed ("1234,5", "1234").
func FormatInput(lang Lang, v float64, maxDecimals int) string {
	s := strconv.FormatFloat(v, 'f', maxDecimals, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		s = "0"
	}
	_, decimal := separators(lang)
	return strings.Replace(s, ".", decimal, 1)
}

var monthNames = map[Lang][12]string{
	ES: {"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sept", "oct", "nov", "dic"},
	EN: {"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
}

// FormatMonth formats a year and month (1-12) as "mar 2041" (es) or "Mar 2041" (en).
func FormatMonth(lang Lang, year, month int) string {
	if month < 1 || month > 12 {
		return ""
	}
	names, ok := monthNames[lang]
	if !ok {
		names = monthNames[Default]
	}
	return names[month-1] + " " + strconv.Itoa(year)
}
