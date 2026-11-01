// Command dataset regenerates the embedded historical market data from
// Robert Shiller's "ie_data.xls" (US stocks, bonds and CPI since 1871).
//
//	go run ./cmd/dataset update                 # download from Shiller's site
//	go run ./cmd/dataset update -url <url>      # a different copy of ie_data.xls
//	go run ./cmd/dataset update -in ie_data.xls # a file you downloaded yourself
//	    (add -source <url> to record where that file came from)
//
// It writes internal/history/shiller_monthly.csv; rebuild the app to embed it.
// The app itself never downloads anything.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/igomezal/horizonte/internal/history"
	"github.com/igomezal/horizonte/internal/xls"
)

const defaultURL = "http://www.econ.yale.edu/~shiller/data/ie_data.xls"

func main() {
	if len(os.Args) < 2 || os.Args[1] != "update" {
		fmt.Fprintln(os.Stderr, "usage: dataset update [-url URL | -in FILE] [-out FILE]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	src := fs.String("url", defaultURL, "where to download ie_data.xls from")
	in := fs.String("in", "", "use a local ie_data.xls instead of downloading")
	out := fs.String("out", filepath.Join("internal", "history", "shiller_monthly.csv"), "CSV to write")
	label := fs.String("source", "", "source to record in the CSV (default: the URL or file used)")
	_ = fs.Parse(os.Args[2:])

	if err := run(*src, *in, *out, *label); err != nil {
		fmt.Fprintln(os.Stderr, "dataset:", err)
		os.Exit(1)
	}
}

func run(src, in, out, label string) error {
	var data []byte
	var err error
	source := src
	if in != "" {
		source = in
		data, err = os.ReadFile(in)
	} else {
		fmt.Println("downloading", src)
		data, err = download(src)
	}
	if err != nil {
		return err
	}
	rows, err := convert(data)
	if err != nil {
		return err
	}
	if label != "" {
		source = label
	}
	csv := render(rows, source, time.Now())
	// Validate with the same parser the app uses before replacing anything.
	s, err := history.Parse([]byte(csv))
	if err != nil {
		return fmt.Errorf("generated data does not parse: %w", err)
	}
	if err := os.WriteFile(out, []byte(csv), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s: %d months, %s to %s\n", out, s.Len(), s.Start, s.End())
	return nil
}

func download(url string) ([]byte, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 50<<20))
}

type row struct {
	year, month            int
	stock, bond, inflation float64
}

// Column layout of Shiller's "Data" sheet, located by their header text.
type columns struct {
	date, cpi, rate, realTR, bondTR int
}

func findColumns(sh *xls.Sheet) (columns, int, error) {
	c := columns{-1, -1, -1, -1, -1}
	for r := 0; r <= 12; r++ {
		// "Date", "CPI" and "Rate GS10" must sit in the same header row (the row
		// above also contains "Date", as part of "Date Fraction").
		c.date, c.cpi, c.rate = -1, -1, -1
		for col := 0; col < 30; col++ {
			switch strings.TrimSpace(sh.Get(r, col).Str) {
			case "Date":
				if c.date < 0 {
					c.date = col
				}
			case "CPI":
				c.cpi = col
			case "Rate GS10":
				c.rate = col
			}
		}
		// The multi-line headers: "Real Total Return Price" and "Monthly Total Bond Returns".
		for col := 0; col < 30; col++ {
			if strings.TrimSpace(sh.Get(r, col).Str) == "Price" && strings.TrimSpace(sh.Get(r-1, col).Str) == "Return" &&
				strings.TrimSpace(sh.Get(r-3, col).Str) == "Real" {
				c.realTR = col
			}
			if strings.TrimSpace(sh.Get(r, col).Str) == "Returns" && strings.TrimSpace(sh.Get(r-1, col).Str) == "Bond" &&
				strings.TrimSpace(sh.Get(r-3, col).Str) == "Monthly" {
				c.bondTR = col
			}
		}
		if c.date >= 0 && c.cpi >= 0 && c.rate >= 0 && c.realTR >= 0 {
			return c, r + 1, nil
		}
	}
	return c, 0, errors.New("could not find the Date / CPI / Rate GS10 / Real Total Return Price columns")
}

// convert turns the Data sheet into monthly nominal returns.
func convert(data []byte) ([]row, error) {
	sheets, err := xls.Read(data)
	if err != nil {
		return nil, err
	}
	var sh *xls.Sheet
	for _, s := range sheets {
		if s.Name == "Data" {
			sh = s
		}
	}
	if sh == nil {
		return nil, errors.New(`no "Data" sheet`)
	}
	cols, first, err := findColumns(sh)
	if err != nil {
		return nil, err
	}

	type obs struct {
		year, month             int
		cpi, rate, realTR, bond float64
		hasBond                 bool
	}
	var series []obs
	for r := first; r <= sh.MaxRow(); r++ {
		d, cpi, rate, tr := sh.Get(r, cols.date), sh.Get(r, cols.cpi), sh.Get(r, cols.rate), sh.Get(r, cols.realTR)
		if !d.IsNum || !cpi.IsNum || !rate.IsNum || !tr.IsNum || cpi.Num <= 0 || tr.Num <= 0 {
			continue
		}
		year := int(math.Floor(d.Num))
		month := int(math.Round((d.Num - float64(year)) * 100))
		if month < 1 || month > 12 {
			return nil, fmt.Errorf("row %d: bad date %v", r+1, d.Num)
		}
		o := obs{year: year, month: month, cpi: cpi.Num, rate: rate.Num / 100, realTR: tr.Num}
		if cols.bondTR >= 0 {
			if b := sh.Get(r, cols.bondTR); b.IsNum && b.Num > 0 {
				o.bond, o.hasBond = b.Num-1, true // stored as a growth factor (1.004)
			}
		}
		series = append(series, o)
	}

	var rows []row
	for i := 0; i+1 < len(series); i++ {
		a, b := series[i], series[i+1]
		if (b.year*12 + b.month) != (a.year*12 + a.month + 1) {
			return nil, fmt.Errorf("gap between %d-%02d and %d-%02d", a.year, a.month, b.year, b.month)
		}
		infl := b.cpi/a.cpi - 1
		// Nominal total return = change of the real total-return index, re-inflated.
		stock := (b.realTR*b.cpi)/(a.realTR*a.cpi) - 1
		bond := a.bond
		if !a.hasBond {
			// Missing in the latest months: coupon plus price change of a
			// 10-year bond (modified duration ≈ 8).
			bond = a.rate/12 - 8*(b.rate-a.rate)
		}
		if math.Abs(stock) > 0.6 || math.Abs(bond) > 0.3 || math.Abs(infl) > 0.2 {
			return nil, fmt.Errorf("%d-%02d: implausible values (stock %.3f, bond %.3f, inflation %.3f)",
				a.year, a.month, stock, bond, infl)
		}
		rows = append(rows, row{a.year, a.month, stock, bond, infl})
	}
	if len(rows) < 600 {
		return nil, fmt.Errorf("only %d months found", len(rows))
	}
	return rows, nil
}

func render(rows []row, source string, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Robert J. Shiller, U.S. stock market data (ie_data.xls): monthly nominal total returns.\n")
	fmt.Fprintf(&b, "# stock = S&P Composite with dividends; bond = 10-year Treasury; inflation = CPI change.\n")
	fmt.Fprintf(&b, "# source: %s\n", source)
	fmt.Fprintf(&b, "# generated: %s\n", now.Format("2006-01-02"))
	b.WriteString("month,stock,bond,inflation\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%04d-%02d,%.6f,%.6f,%.6f\n", r.year, r.month, r.stock, r.bond, r.inflation)
	}
	return b.String()
}
