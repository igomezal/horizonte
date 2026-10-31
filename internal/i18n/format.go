package i18n

import (
	"math"
	"strconv"
	"strings"
)

// separators returns the thousands and decimal separators for lang.
func separators(lang Lang) (thousands, decimal string) {
	if lang == EN {
		return ",", "."
	}
	return ".", ","
}

// FormatNumber formats v with the given number of decimals using lang's separators.
// Four-digit amounts are grouped too (1.000), as is usual for financial figures.
func FormatNumber(lang Lang, v float64, decimals int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "—"
	}
	thousands, decimal := separators(lang)

	neg := v < 0
	s := strconv.FormatFloat(math.Abs(v), 'f', decimals, 64)
	intPart, fracPart, _ := strings.Cut(s, ".")

	var b strings.Builder
	if neg && strings.Trim(s, "0.") != "" {
		b.WriteByte('-')
	}
	for i, d := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteString(thousands)
		}
		b.WriteRune(d)
	}
	if fracPart != "" {
		b.WriteString(decimal)
		b.WriteString(fracPart)
	}
	return b.String()
}

// FormatMoney formats an amount in euros: "1.234,56 €" (es) or "€1,234.56" (en).
func FormatMoney(lang Lang, v float64, decimals int) string {
	n := FormatNumber(lang, v, decimals)
	if lang == EN {
		if strings.HasPrefix(n, "-") {
			return "-€" + n[1:]
		}
		return "€" + n
	}
	return n + " €"
}

// FormatPercent formats a ratio (0.05 = 5 %) as a percentage.
func FormatPercent(lang Lang, ratio float64, decimals int) string {
	n := FormatNumber(lang, ratio*100, decimals)
	if lang == EN {
		return n + "%"
	}
	return n + " %"
}
