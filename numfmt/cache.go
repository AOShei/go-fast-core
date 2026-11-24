package numfmt

import (
	"math"
	"sync/atomic"
)

// lastID numbers the formats Parse makes, for Cache's hash. The General
// format is 0.
var lastID atomic.Uint64

// Cache remembers what Format.Number made of recent values, so that a
// value shown again through the same format is copied rather than worked
// out. Spreadsheets repeat themselves: the same amount, date or code
// down a column. A remembered text is the text Number gives, since
// Number depends on the format, the value and the date system only, and
// all three are the key.
//
// A Cache is for one reader at a time; it is not safe for concurrent
// use. A reader that reads several sheets at once holds one for each.
// Reset it when a sheet begins: a sheet's values rarely recur in the
// next, and a small sheet then costs a small table.
//
// The table starts at 64 entries and doubles, emptied, each time it
// has missed as many values as it has entries, up to the limit NewCache
// is given. Each entry is 24 bytes, plus its text in a buffer that is
// emptied with the table and bounded at 64 bytes an entry. The nil
// *Cache remembers nothing: its Number is Format.Number.
type Cache struct {
	slots  []slot
	shift  uint // 64 minus log2(len(slots))
	max    int  // the most entries the table grows to: a power of two
	misses int  // since the table was last emptied
	text   []byte
}

type slot struct {
	f      *Format // nil: empty
	bits   uint64  // the value's
	off    uint32  // its text in Cache.text
	n      uint16
	result Result
	d1904  bool
}

const (
	firstSlots  = 64
	slotText    = 64  // bytes of text an entry may use on average
	maxSlotText = 255 // a longer text is not remembered
)

// NewCache returns a Cache of at most entries entries, rounded down to a
// power of two and to no more than 2^20 (24 MiB), or nil, which
// remembers nothing, when entries is under one.
func NewCache(entries int) *Cache {
	if entries < 1 {
		return nil
	}
	max := 1
	for max*2 <= entries && max < 1<<20 {
		max *= 2
	}
	return &Cache{max: max}
}

// Number appends v as f displays it, as f.Number does, remembering the
// text for the next time v is shown through f.
func (c *Cache) Number(f *Format, dst []byte, v float64, date1904 bool) ([]byte, Result) {
	if c == nil {
		return f.Number(dst, v, date1904)
	}
	if c.slots == nil {
		c.resize(min(firstSlots, c.max))
	}
	bits := math.Float64bits(v)
	h := bits + f.id*0x9E3779B97F4A7C15
	if date1904 {
		h ^= 0xD6E8FEB86659FD93
	}
	s := &c.slots[(h*0xBF58476D1CE4E5B9)>>c.shift]
	if s.f == f && s.bits == bits && s.d1904 == date1904 {
		return append(dst, c.text[s.off:s.off+uint32(s.n)]...), s.result
	}
	start := len(dst)
	dst, result := f.Number(dst, v, date1904)
	out := dst[start:]
	if len(out) > maxSlotText {
		return dst, result
	}
	c.misses++
	if c.misses > len(c.slots) && len(c.slots) < c.max {
		c.resize(2 * len(c.slots))
		s = &c.slots[(h*0xBF58476D1CE4E5B9)>>c.shift]
	} else if len(c.text)+len(out) > slotText*len(c.slots) {
		c.empty()
	}
	*s = slot{f: f, bits: bits, off: uint32(len(c.text)), n: uint16(len(out)), result: result, d1904: date1904}
	c.text = append(c.text, out...)
	return dst, result
}

// Reset empties c and returns it to its first size, keeping its memory.
func (c *Cache) Reset() {
	if c == nil || c.slots == nil {
		return
	}
	c.resize(min(firstSlots, c.max))
}

// resize makes the table n entries, a power of two, and empties it.
func (c *Cache) resize(n int) {
	if cap(c.slots) >= n {
		c.slots = c.slots[:n]
	} else {
		c.slots = make([]slot, n)
	}
	c.shift = 64
	for m := n; m > 1; m /= 2 {
		c.shift--
	}
	c.empty()
}

// empty forgets every entry.
func (c *Cache) empty() {
	clear(c.slots)
	c.text = c.text[:0]
	c.misses = 0
}
