// Package history provides historical monthly market data (Robert Shiller's
// US dataset) for backtests. The data is embedded from shiller_monthly.csv,
// which `go run ./cmd/dataset update` regenerates.
package history

import (
	"bufio"
	"bytes"
	_ "embed"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/igomezal/horizonte/internal/domain"
)

//go:embed shiller_monthly.csv
var csvData []byte

// Series holds monthly returns from Start onwards. Index i covers the month
// Start+i: the return earned from the start of that month to the next.
type Series struct {
	Start     domain.Month
	Stock     []float64 // US stocks, total return (price + dividends), nominal
	Bond      []float64 // 10-year US Treasury, total return, nominal
	Inflation []float64 // CPI change
	Source    string    // where the data came from
	Generated string    // when the CSV was generated
}

// Len is the number of months.
func (s *Series) Len() int { return len(s.Stock) }

// End is the last month with data.
func (s *Series) End() domain.Month { return s.Start.AddMonths(s.Len() - 1) }

// Index returns the position of month m, or -1 if outside the data.
func (s *Series) Index(m domain.Month) int {
	i := s.Start.MonthsUntil(m)
	if i < 0 || i >= s.Len() {
		return -1
	}
	return i
}

var (
	once   sync.Once
	loaded *Series
	errLd  error
)

// Load parses the embedded dataset (once).
func Load() (*Series, error) {
	once.Do(func() { loaded, errLd = Parse(csvData) })
	return loaded, errLd
}

// Parse reads the CSV format written by cmd/dataset:
//
//	# source: <url>
//	# generated: <date>
//	month,stock,bond,inflation
//	1871-01,0.0123,0.0040,0.0305
func Parse(b []byte) (*Series, error) {
	s := &Series{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		switch {
		case text == "":
			continue
		case strings.HasPrefix(text, "# source:"):
			s.Source = strings.TrimSpace(strings.TrimPrefix(text, "# source:"))
			continue
		case strings.HasPrefix(text, "# generated:"):
			s.Generated = strings.TrimSpace(strings.TrimPrefix(text, "# generated:"))
			continue
		case strings.HasPrefix(text, "#"), strings.HasPrefix(text, "month,"):
			continue
		}
		f := strings.Split(text, ",")
		if len(f) != 4 {
			return nil, fmt.Errorf("history: line %d: want 4 fields", line)
		}
		m, err := domain.ParseMonth(f[0])
		if err != nil {
			return nil, fmt.Errorf("history: line %d: %w", line, err)
		}
		if s.Start.IsZero() {
			s.Start = m
		} else if want := s.Start.AddMonths(s.Len()); m != want {
			return nil, fmt.Errorf("history: line %d: month %s, want %s (gap)", line, m, want)
		}
		vals := make([]float64, 3)
		for i := range vals {
			if vals[i], err = strconv.ParseFloat(f[i+1], 64); err != nil {
				return nil, fmt.Errorf("history: line %d: %w", line, err)
			}
		}
		s.Stock = append(s.Stock, vals[0])
		s.Bond = append(s.Bond, vals[1])
		s.Inflation = append(s.Inflation, vals[2])
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if s.Len() == 0 {
		return nil, fmt.Errorf("history: no data")
	}
	return s, nil
}

// RealStockReturn is the long-run annualised stock return above inflation.
func (s *Series) RealStockReturn() float64 {
	if s.Len() == 0 {
		return 0
	}
	var sum float64
	for i := range s.Stock {
		sum += math.Log((1 + s.Stock[i]) / (1 + s.Inflation[i]))
	}
	return math.Exp(sum*12/float64(s.Len())) - 1
}
