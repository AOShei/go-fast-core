package numfmt

import (
	"bufio"
	"bytes"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"testing"
)

// tableFormats returns the formats and values of the LibreOffice table.
func tableFormats(t testing.TB) ([]*Format, []float64) {
	f, err := os.Open("testdata/libreoffice.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	codes, values := map[string]bool{}, map[float64]bool{}
	var formats []*Format
	var vs []float64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if !codes[fields[0]] {
			codes[fields[0]] = true
			formats = append(formats, Parse(fields[0]))
		}
		if v, err := strconv.ParseFloat(fields[1], 64); err == nil && !values[v] {
			values[v] = true
			vs = append(vs, v)
		}
	}
	return formats, vs
}

// TestCacheIsNumber: through a Cache of any size, every value gives the
// bytes and the Result Format.Number gives, appended after what dst
// holds, across hits, misses, growth, overflowing text and Reset.
func TestCacheIsNumber(t *testing.T) {
	formats, values := tableFormats(t)
	formats = append(formats, General())
	for _, n := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), math.Copysign(0, -1), 5e-324, math.MaxFloat64} {
		values = append(values, n)
	}
	r := rand.New(rand.NewPCG(1, 2))
	for _, size := range []int{1, 2, 3, 64, 100, 4096, 1 << 16} {
		c := NewCache(size)
		var prefix []byte
		var got []byte
		calls, hits := 0, 0
		for i := 0; i < 200_000; i++ {
			f := formats[r.IntN(len(formats))]
			var v float64
			switch r.IntN(4) {
			case 0:
				v = values[r.IntN(len(values))]
			case 1:
				v = float64(r.IntN(50)) // repeats
			case 2:
				v = 40000 + float64(r.IntN(400))/8 // dates and times, repeating
			default:
				v = r.NormFloat64() * math.Pow(10, float64(r.IntN(30)-15))
			}
			if r.IntN(8) == 0 {
				f = formats[i%3] // runs of one format
			}
			d1904 := r.IntN(16) == 0
			prefix = append(prefix[:0], strconv.Itoa(i)...)
			want, wr := f.Number(append([]byte(nil), prefix...), v, d1904)
			before := len(c.text)
			got, gr := c.Number(f, append(got[:0], prefix...), v, d1904)
			if !bytes.Equal(got, want) || gr != wr {
				t.Fatalf("size %d, call %d: %v (1904 %v) = %q %v, want %q %v", size, i, v, d1904, got, gr, want, wr)
			}
			calls++
			if len(c.text) == before && len(want) > len(prefix) {
				hits++
			}
			if len(c.slots) > c.max || len(c.text) > slotText*len(c.slots)+maxSlotText {
				t.Fatalf("size %d: %d slots of %d, %d bytes of text", size, len(c.slots), c.max, len(c.text))
			}
			if r.IntN(5000) == 0 {
				c.Reset()
			}
		}
		if hits == 0 {
			t.Errorf("size %d: no hits in %d calls", size, calls)
		}
	}
}

// TestCacheGrows: the table starts small, grows to its limit as values
// miss, and Reset returns it to its first size without new memory.
func TestCacheGrows(t *testing.T) {
	f := Parse("0.00")
	c := NewCache(1000) // 512 entries
	if c.max != 512 {
		t.Fatalf("max %d", c.max)
	}
	var out []byte
	for i := 0; i < 10_000; i++ {
		out, _ = c.Number(f, out[:0], float64(i), false)
	}
	if len(c.slots) != 512 {
		t.Errorf("after many values: %d slots", len(c.slots))
	}
	c.Reset()
	if len(c.slots) != firstSlots || cap(c.slots) != 512 || len(c.text) != 0 {
		t.Errorf("after Reset: %d slots (cap %d), %d bytes", len(c.slots), cap(c.slots), len(c.text))
	}
	if n := testing.AllocsPerRun(10, func() {
		c.Reset()
		for i := 0; i < 2000; i++ {
			out, _ = c.Number(f, out[:0], float64(i%700), false)
		}
	}); n != 0 {
		t.Errorf("%v allocations after the first sheet", n)
	}
	if NewCache(1<<40).max != 1<<20 {
		t.Errorf("not bounded")
	}
}

// TestCacheNil: no cache, or a size under one, is Format.Number.
func TestCacheNil(t *testing.T) {
	for _, c := range []*Cache{nil, NewCache(0), NewCache(-5)} {
		if c != nil {
			t.Fatalf("NewCache gave %v", c)
		}
		out, r := c.Number(Parse("0.0%"), nil, 0.125, false)
		if string(out) != "12.5%" || r != Shown {
			t.Errorf("%q %v", out, r)
		}
		c.Reset()
	}
}

// TestCacheKey: the format, the value's bits and the date system are the
// key: none of them is taken for another.
func TestCacheKey(t *testing.T) {
	c := NewCache(64)
	a, b := Parse("0.0"), Parse("0.0") // the same code, two formats
	date := Parse("yyyy-mm-dd")
	for _, k := range []struct {
		f     *Format
		v     float64
		d1904 bool
	}{{a, 1, false}, {b, 1, false}, {a, 1.0000000000000002, false}, {date, 100, false}, {date, 100, true}, {a, 0, false}, {a, math.Copysign(0, -1), false}} {
		for i := 0; i < 2; i++ {
			want, wr := k.f.Number(nil, k.v, k.d1904)
			got, gr := c.Number(k.f, nil, k.v, k.d1904)
			if string(got) != string(want) || gr != wr {
				t.Errorf("%v %v: %q, want %q", k.v, k.d1904, got, want)
			}
		}
	}
}

func FuzzCache(f *testing.F) {
	f.Add([]byte("\x00\x01\x02\x03\x00\x01"), uint16(4))
	f.Add(bytes.Repeat([]byte{7, 0, 200}, 40), uint16(1))
	codes := []string{"General", "0", "0.00", "#,##0.00;[Red](#,##0.00)", "0%", "0.00E+00", "# ?/?", "yyyy-mm-dd", "[h]:mm:ss", "@", ";;;", "\"x\"", `0" ` + strings.Repeat("y", 300) + `"`}
	var formats []*Format
	for _, code := range codes {
		formats = append(formats, Parse(code))
	}
	f.Fuzz(func(t *testing.T, ops []byte, size uint16) {
		c := NewCache(int(size))
		var got []byte
		for i := 0; i+2 < len(ops); i += 3 {
			if ops[i] == 0xFF {
				c.Reset()
				continue
			}
			fm := formats[int(ops[i])%len(formats)]
			v := float64(int8(ops[i+1])) / float64(1+ops[i+2]%7)
			d1904 := ops[i+2]&0x80 != 0
			want, wr := fm.Number([]byte("p"), v, d1904)
			got, gr := c.Number(fm, append(got[:0], 'p'), v, d1904)
			if !bytes.Equal(got, want) || gr != wr {
				t.Fatalf("op %d: %q, want %q", i/3, got, want)
			}
		}
	})
}

// BenchmarkCache compares Format.Number with Cache.Number on a stream
// like a sheet's: a few formats, values that recur down columns.
func BenchmarkCache(b *testing.B) {
	formats := []*Format{Parse("#,##0.00"), Parse("0"), Parse("yyyy-mm-dd"), Parse("0.0%")}
	r := rand.New(rand.NewPCG(3, 4))
	type call struct {
		f *Format
		v float64
	}
	stream := make([]call, 4096)
	for i := range stream {
		f := formats[r.IntN(len(formats))]
		v := float64(r.IntN(300)) // about 70% recur
		if r.IntN(3) == 0 {
			v = r.Float64() * 1e6
		}
		stream[i] = call{f, v}
	}
	var out []byte
	b.Run("engine", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			s := stream[i%len(stream)]
			out, _ = s.f.Number(out[:0], s.v, false)
		}
	})
	for _, size := range []int{256, 4096} {
		b.Run("cache"+strconv.Itoa(size), func(b *testing.B) {
			c := NewCache(size)
			for i := 0; i < b.N; i++ {
				s := stream[i%len(stream)]
				out, _ = c.Number(s.f, out[:0], s.v, false)
			}
		})
	}
}
