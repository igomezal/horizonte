// Package xls reads cell values from legacy Excel .xls files (BIFF8 inside an
// OLE2 compound document). It is deliberately small: it supports what the
// historical-data updater needs (numbers, shared strings and cached formula
// numbers) and ignores formatting, charts and everything else.
package xls

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf16"
)

// Cell is a cell value: a number, a string, or empty.
type Cell struct {
	Num   float64
	Str   string
	IsNum bool
}

// Sheet is a worksheet as rows of cells (rows and columns are 0-based).
type Sheet struct {
	Name string
	Rows map[int]map[int]Cell
}

// Get returns the cell at row, col (empty if missing).
func (s *Sheet) Get(row, col int) Cell { return s.Rows[row][col] }

// MaxRow returns the highest row index with any cell.
func (s *Sheet) MaxRow() int {
	m := -1
	for r := range s.Rows {
		if r > m {
			m = r
		}
	}
	return m
}

var errFormat = errors.New("xls: unsupported or corrupt file")

// ---------- OLE2 compound document ----------

const (
	freeSect   = 0xFFFFFFFF
	endOfChain = 0xFFFFFFFE
)

type cfb struct {
	data       []byte
	sectorSize int
	fat        []uint32
	dirStart   uint32
}

func (c *cfb) sector(n uint32) ([]byte, error) {
	off := (int(n) + 1) * c.sectorSize
	if n >= endOfChain-1 || off < 0 || off+c.sectorSize > len(c.data) {
		return nil, errFormat
	}
	return c.data[off : off+c.sectorSize], nil
}

func (c *cfb) chain(start uint32, limit int) ([]byte, error) {
	var out []byte
	seen := map[uint32]bool{}
	for s := start; s != endOfChain; {
		if seen[s] || int(s) >= len(c.fat) {
			return nil, errFormat
		}
		seen[s] = true
		b, err := c.sector(s)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
		if limit > 0 && len(out) >= limit {
			return out[:limit], nil
		}
		s = c.fat[s]
	}
	return out, nil
}

func openCFB(data []byte) (*cfb, error) {
	if len(data) < 512 || !bytes.Equal(data[:8], []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}) {
		return nil, errFormat
	}
	le := binary.LittleEndian
	shift := le.Uint16(data[0x1E:])
	if shift != 9 && shift != 12 {
		return nil, errFormat
	}
	c := &cfb{data: data, sectorSize: 1 << shift, dirStart: le.Uint32(data[0x30:])}
	numFAT := int(le.Uint32(data[0x2C:]))
	difat := make([]uint32, 0, numFAT)
	for i := 0; i < 109 && len(difat) < numFAT; i++ {
		difat = append(difat, le.Uint32(data[0x4C+4*i:]))
	}
	// Extra DIFAT sectors for very large files.
	next := le.Uint32(data[0x44:])
	for len(difat) < numFAT && next != endOfChain && next != freeSect {
		b, err := c.sector(next)
		if err != nil {
			return nil, err
		}
		per := c.sectorSize/4 - 1
		for i := 0; i < per && len(difat) < numFAT; i++ {
			difat = append(difat, le.Uint32(b[4*i:]))
		}
		next = le.Uint32(b[4*per:])
	}
	for _, s := range difat {
		b, err := c.sector(s)
		if err != nil {
			return nil, err
		}
		for i := 0; i+4 <= len(b); i += 4 {
			c.fat = append(c.fat, le.Uint32(b[i:]))
		}
	}
	return c, nil
}

// stream returns the contents of a top-level stream by name (big streams only;
// Excel workbook streams are always larger than the 4 KiB mini-stream cutoff).
func (c *cfb) stream(names ...string) ([]byte, error) {
	dir, err := c.chain(c.dirStart, 0)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	for off := 0; off+128 <= len(dir); off += 128 {
		e := dir[off : off+128]
		nameLen := int(le.Uint16(e[0x40:]))
		if nameLen < 2 || nameLen > 64 || e[0x42] != 2 {
			continue
		}
		u := make([]uint16, nameLen/2-1)
		for i := range u {
			u[i] = le.Uint16(e[2*i:])
		}
		name := string(utf16.Decode(u))
		for _, want := range names {
			if name == want {
				size := int(le.Uint32(e[0x78:]))
				if size < 4096 {
					return nil, fmt.Errorf("xls: stream %q too small for this reader", name)
				}
				return c.chain(le.Uint32(e[0x74:]), size)
			}
		}
	}
	return nil, errors.New("xls: no Workbook stream")
}

// ---------- BIFF8 records ----------

const (
	recBOF        = 0x0809
	recEOF        = 0x000A
	recBoundSheet = 0x0085
	recSST        = 0x00FC
	recContinue   = 0x003C
	recNumber     = 0x0203
	recRK         = 0x027E
	recMulRK      = 0x00BD
	recFormula    = 0x0006
	recLabelSST   = 0x00FD
)

type record struct {
	typ  uint16
	data []byte
}

func records(b []byte, from int) ([]record, error) {
	var out []record
	le := binary.LittleEndian
	for off := from; off+4 <= len(b); {
		typ, n := le.Uint16(b[off:]), int(le.Uint16(b[off+2:]))
		if off+4+n > len(b) {
			return nil, errFormat
		}
		out = append(out, record{typ, b[off+4 : off+4+n]})
		off += 4 + n
		if typ == recEOF && from > 0 {
			break // end of this worksheet substream
		}
	}
	return out, nil
}

func rk(v uint32) float64 {
	var f float64
	if v&2 != 0 {
		f = float64(int32(v) >> 2)
	} else {
		f = math.Float64frombits(uint64(v&0xFFFFFFFC) << 32)
	}
	if v&1 != 0 {
		f /= 100
	}
	return f
}

// sstReader walks the shared string table across CONTINUE records.
type sstReader struct {
	parts [][]byte
	p, i  int
}

func (r *sstReader) need() bool {
	for r.p < len(r.parts) && r.i >= len(r.parts[r.p]) {
		r.p++
		r.i = 0
	}
	return r.p < len(r.parts)
}

func (r *sstReader) byte() (byte, bool) {
	if !r.need() {
		return 0, false
	}
	b := r.parts[r.p][r.i]
	r.i++
	return b, true
}

func (r *sstReader) u16() (uint16, bool) {
	a, ok1 := r.byte()
	b, ok2 := r.byte()
	return uint16(a) | uint16(b)<<8, ok1 && ok2
}

func (r *sstReader) u32() (uint32, bool) {
	a, ok1 := r.u16()
	b, ok2 := r.u16()
	return uint32(a) | uint32(b)<<16, ok1 && ok2
}

func (r *sstReader) skip(n int) {
	for ; n > 0; n-- {
		r.byte()
	}
}

func (r *sstReader) str() (string, bool) {
	n, ok := r.u16()
	if !ok {
		return "", false
	}
	flags, ok := r.byte()
	if !ok {
		return "", false
	}
	var runs int
	var ext uint32
	if flags&0x08 != 0 {
		v, _ := r.u16()
		runs = int(v)
	}
	if flags&0x04 != 0 {
		ext, _ = r.u32()
	}
	wide := flags&0x01 != 0
	u := make([]uint16, 0, n)
	for len(u) < int(n) {
		if r.p < len(r.parts) && r.i >= len(r.parts[r.p]) {
			// Characters continue in a CONTINUE record that starts with a
			// fresh option byte saying whether they are 8- or 16-bit.
			r.p++
			r.i = 0
			f, ok := r.byte()
			if !ok {
				return "", false
			}
			wide = f&0x01 != 0
		}
		if wide {
			c, ok := r.u16()
			if !ok {
				return "", false
			}
			u = append(u, c)
		} else {
			c, ok := r.byte()
			if !ok {
				return "", false
			}
			u = append(u, uint16(c))
		}
	}
	r.skip(4*runs + int(ext))
	return string(utf16.Decode(u)), true
}

// Read parses an .xls file and returns its worksheets.
func Read(data []byte) ([]*Sheet, error) {
	c, err := openCFB(data)
	if err != nil {
		return nil, err
	}
	wb, err := c.stream("Workbook", "Book")
	if err != nil {
		return nil, err
	}
	globals, err := records(wb, 0)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	var sst []string
	type sheetRef struct {
		name string
		off  int
	}
	var refs []sheetRef
	for k, rec := range globals {
		switch rec.typ {
		case recBoundSheet:
			if len(rec.data) < 8 || rec.data[5] != 0 { // worksheets only
				continue
			}
			n := int(rec.data[6])
			name := ""
			if rec.data[7]&1 != 0 && len(rec.data) >= 8+2*n {
				u := make([]uint16, n)
				for i := range u {
					u[i] = le.Uint16(rec.data[8+2*i:])
				}
				name = string(utf16.Decode(u))
			} else if len(rec.data) >= 8+n {
				name = string(rec.data[8 : 8+n])
			}
			refs = append(refs, sheetRef{name, int(le.Uint32(rec.data))})
		case recSST:
			parts := [][]byte{rec.data[8:]}
			for j := k + 1; j < len(globals) && globals[j].typ == recContinue; j++ {
				parts = append(parts, globals[j].data)
			}
			count := int(le.Uint32(rec.data[4:]))
			r := &sstReader{parts: parts}
			for i := 0; i < count; i++ {
				s, ok := r.str()
				if !ok {
					break
				}
				sst = append(sst, s)
			}
		}
		if rec.typ == recEOF {
			break
		}
	}

	var sheets []*Sheet
	for _, ref := range refs {
		recs, err := records(wb, ref.off)
		if err != nil {
			return nil, err
		}
		sh := &Sheet{Name: ref.name, Rows: map[int]map[int]Cell{}}
		set := func(row, col int, cell Cell) {
			if sh.Rows[row] == nil {
				sh.Rows[row] = map[int]Cell{}
			}
			sh.Rows[row][col] = cell
		}
		for _, rec := range recs {
			d := rec.data
			switch rec.typ {
			case recNumber:
				if len(d) >= 14 {
					set(int(le.Uint16(d)), int(le.Uint16(d[2:])), Cell{Num: math.Float64frombits(le.Uint64(d[6:])), IsNum: true})
				}
			case recRK:
				if len(d) >= 10 {
					set(int(le.Uint16(d)), int(le.Uint16(d[2:])), Cell{Num: rk(le.Uint32(d[6:])), IsNum: true})
				}
			case recMulRK:
				if len(d) >= 6 {
					row, col := int(le.Uint16(d)), int(le.Uint16(d[2:]))
					for off := 4; off+6 <= len(d)-2; off += 6 {
						set(row, col, Cell{Num: rk(le.Uint32(d[off+2:])), IsNum: true})
						col++
					}
				}
			case recFormula:
				// Only cached numeric results (the last two bytes of the
				// result are 0xFFFF for strings, booleans and errors).
				if len(d) >= 14 && !(d[12] == 0xFF && d[13] == 0xFF) {
					set(int(le.Uint16(d)), int(le.Uint16(d[2:])), Cell{Num: math.Float64frombits(le.Uint64(d[6:])), IsNum: true})
				}
			case recLabelSST:
				if len(d) >= 10 {
					if i := int(le.Uint32(d[6:])); i < len(sst) {
						set(int(le.Uint16(d)), int(le.Uint16(d[2:])), Cell{Str: sst[i]})
					}
				}
			}
		}
		sheets = append(sheets, sh)
	}
	return sheets, nil
}
