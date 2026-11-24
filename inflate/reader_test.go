package inflate

import (
	"bytes"
	"compress/flate"
	"errors"
	"io"
	"math/rand"
	"testing"
)

// readAll reads z in reads of the sizes, in turn, and returns what it
// gave and the error that ended it (nil for io.EOF).
func readAll(t testing.TB, z *Reader, sizes []int) ([]byte, error) {
	t.Helper()
	var out []byte
	for i := 0; ; i++ {
		size := sizes[i%len(sizes)]
		p := make([]byte, size)
		n, err := z.Read(p)
		if n < 0 || n > size {
			t.Fatalf("Read of %d gave %d", size, n)
		}
		out = append(out, p[:n]...)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			if n != 0 {
				t.Fatalf("an error with %d bytes", n)
			}
			// The error stays.
			if n2, err2 := z.Read(p); n2 != 0 || err2 != err {
				t.Fatalf("after %v: %d, %v", err, n2, err2)
			}
			return out, err
		}
		if n == 0 && size > 0 {
			t.Fatalf("Read of %d gave nothing and no error", size)
		}
	}
}

// sameAsRaw compares a Reader of src, read in reads of the sizes, with
// Raw: the same bytes and the same error.
func sameAsRaw(t testing.TB, z *Reader, src []byte, limit int64, sizes []int) {
	t.Helper()
	want, wantErr := Raw(nil, src, limit)
	if z == nil {
		z = NewRawReader(src, limit)
	} else {
		z.Reset(src, limit)
	}
	got, err := readAll(t, z, sizes)
	if !bytes.Equal(got, want) || err != wantErr {
		at := 0
		for at < len(got) && at < len(want) && got[at] == want[at] {
			at++
		}
		t.Fatalf("limit %d, reads %v: %d bytes (%v), Raw gives %d (%v); they differ at %d\ninput %x", limit, sizes, len(got), err, len(want), wantErr, at, clip(src))
	}
}

var readSizes = [][]int{{1 << 20}, {1}, {7, 4096, 3}, {window + piece + 1}, {32 << 10}, {0, 100}}

// large is data that takes more than the reader's buffer, with matches
// from as far back as they may reach, and runs.
func large() []byte {
	rng := rand.New(rand.NewSource(2))
	var b []byte
	for len(b) < 1<<20 {
		switch rng.Intn(4) {
		case 0:
			b = append(b, "the reader keeps the last thirty-two kilobytes "...)
		case 1:
			if len(b) > window {
				from := len(b) - window + rng.Intn(50)
				b = append(b, b[from:from+258]...)
			}
		case 2:
			b = append(b, bytes.Repeat([]byte{byte(rng.Intn(256))}, rng.Intn(600))...)
		default:
			r := make([]byte, rng.Intn(300))
			rng.Read(r)
			b = append(b, r...)
		}
	}
	return b
}

func TestReaderGivesWhatRawGives(t *testing.T) {
	data := samples()
	data["large"] = large()
	for name, d := range data {
		for _, level := range levels {
			src := compressed(t, rawFormat, d, level)
			for _, sizes := range readSizes {
				sameAsRaw(t, nil, src, 1<<30, sizes)
			}
			z := NewRawReader(src, 1<<30)
			got, err := io.ReadAll(z)
			if err != nil || !bytes.Equal(got, d) {
				t.Fatalf("%s at level %d: %d bytes of %d, %v", name, level, len(got), len(d), err)
			}
		}
	}
}

func TestReaderLimit(t *testing.T) {
	d := large()
	src := compressed(t, rawFormat, d, flate.DefaultCompression)
	z := NewRawReader(nil, 0)
	for _, limit := range []int64{0, 1, window - 1, window, window + 1, window + piece - 1, window + piece, window + piece + 1, 3 * piece, int64(len(d)) - 1, int64(len(d)), int64(len(d)) + 1} {
		for _, sizes := range readSizes {
			sameAsRaw(t, z, src, limit, sizes)
		}
	}
	for _, level := range levels {
		src := compressed(t, rawFormat, samples()["skewed"], level)
		for limit := int64(0); limit < 300000; limit += 9973 {
			sameAsRaw(t, z, src, limit, []int{5000})
		}
	}
}

func TestReaderCutOffAndDamaged(t *testing.T) {
	d := large()[:300000]
	z := NewRawReader(nil, 0)
	for _, level := range levels {
		src := compressed(t, rawFormat, d, level)
		for cut := 0; cut < len(src); cut += 1 + len(src)/300 {
			sameAsRaw(t, z, src[:cut], 1<<30, []int{3000})
		}
		rng := rand.New(rand.NewSource(int64(level)))
		for range 300 {
			bad := append([]byte(nil), src...)
			bit := rng.Intn(8 * len(bad))
			bad[bit/8] ^= 1 << (bit % 8)
			sameAsRaw(t, z, bad, 1<<30, []int{7000})
		}
	}
}

// Stored blocks larger than the buffer, cut and limited.
func TestReaderStored(t *testing.T) {
	d := large()[:400000]
	src := compressed(t, rawFormat, d, flate.NoCompression)
	z := NewRawReader(nil, 0)
	for _, sizes := range readSizes {
		sameAsRaw(t, z, src, 1<<30, sizes)
		sameAsRaw(t, z, src, 100000, sizes)
		sameAsRaw(t, z, src[:len(src)-1000], 1<<30, sizes)
	}
}

func TestReaderReset(t *testing.T) {
	a := compressed(t, rawFormat, large(), 6)
	b := compressed(t, rawFormat, samples()["text"], 6)
	z := NewRawReader(a, 1<<30)
	z.Read(make([]byte, 100000)) // part of a, then another stream
	sameAsRaw(t, z, b, 1<<30, []int{4096})
	sameAsRaw(t, z, a, 1<<30, []int{4096})
	sameAsRaw(t, z, []byte{0xff, 0xff}, 1<<30, []int{4096})
	sameAsRaw(t, z, b, 1<<30, []int{4096})
}

// Reading allocates nothing.
func TestReaderAllocations(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector allocates as it watches")
	}
	src := compressed(t, rawFormat, large(), 6)
	z := NewRawReader(src, 1<<30)
	p := make([]byte, 8192)
	if n := testing.AllocsPerRun(5, func() {
		z.Reset(src, 1<<30)
		for {
			if _, err := z.Read(p); err != nil {
				break
			}
		}
	}); n != 0 {
		t.Errorf("%v allocations", n)
	}
}

func FuzzRawReader(f *testing.F) {
	for _, d := range samples() {
		if len(d) > 2000 {
			d = d[:2000]
		}
		for _, level := range levels {
			f.Add(compressed(f, rawFormat, d, level), int64(1<<20), uint16(100))
		}
	}
	z := NewRawReader(nil, 0)
	f.Fuzz(func(t *testing.T, src []byte, limit int64, size uint16) {
		if limit < 0 || limit > 1<<24 {
			limit = 1 << 24
		}
		sameAsRaw(t, z, src, limit, []int{int(size)%9000 + 1, 1})
	})
}

func BenchmarkRawReader(b *testing.B) {
	for _, name := range []string{"text", "skewed", "large"} {
		var d []byte
		if name == "large" {
			d = large()
		} else {
			d = samples()[name]
		}
		src := compressed(b, rawFormat, d, flate.DefaultCompression)
		p := make([]byte, 32<<10)
		b.Run(name+"/reader", func(b *testing.B) {
			b.SetBytes(int64(len(d)))
			z := NewRawReader(src, 1<<30)
			for b.Loop() {
				z.Reset(src, 1<<30)
				for {
					if _, err := z.Read(p); err != nil {
						if !errors.Is(err, io.EOF) {
							b.Fatal(err)
						}
						break
					}
				}
			}
		})
		b.Run(name+"/compress-flate", func(b *testing.B) {
			b.SetBytes(int64(len(d)))
			fr := flate.NewReader(bytes.NewReader(src))
			for b.Loop() {
				fr.(flate.Resetter).Reset(bytes.NewReader(src), nil)
				for {
					if _, err := fr.Read(p); err != nil {
						break
					}
				}
			}
		})
	}
}

// countingSource gives compress/flate its input a byte at a time, so that
// it takes not a byte past the end of the stream, and counts the bytes.
type countingSource struct {
	src []byte
	n   int
}

func (c *countingSource) ReadByte() (byte, error) {
	if c.n == len(c.src) {
		return 0, io.EOF
	}
	c.n++
	return c.src[c.n-1], nil
}

func (c *countingSource) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b, err := c.ReadByte()
	if err != nil {
		return 0, err
	}
	p[0] = b
	return 1, nil
}

// endsAt checks that where z says the stream src ended is where
// compress/flate ends it, for a stream both read whole.
func endsAt(t testing.TB, z *Reader, src []byte) {
	t.Helper()
	z.Reset(src, 1<<30)
	_, err := io.Copy(io.Discard, z)
	c := &countingSource{src: src}
	_, ferr := io.Copy(io.Discard, flate.NewReader(c))
	if (err == nil) != (ferr == nil) {
		t.Fatalf("Reader %v, compress/flate %v\ninput %x", err, ferr, clip(src))
	}
	if err == nil && z.InputOffset() != int64(c.n) {
		t.Fatalf("the stream ends at %d, compress/flate says %d\ninput %x", z.InputOffset(), c.n, clip(src))
	}
}

// InputOffset is where the stream ends, whatever follows it.
func TestReaderInputOffset(t *testing.T) {
	z := NewRawReader(nil, 0)
	data := samples()
	data["large"] = large()[:200000]
	for name, d := range data {
		for _, level := range levels {
			src := compressed(t, rawFormat, d, level)
			for _, after := range [][]byte{nil, {0}, []byte("PK\x07\x08 and what follows"), bytes.Repeat([]byte{0xff}, 100)} {
				in := append(append([]byte(nil), src...), after...)
				z.Reset(in, 1<<30)
				if _, err := io.Copy(io.Discard, z); err != nil {
					t.Fatalf("%s at level %d: %v", name, level, err)
				}
				if got := z.InputOffset(); got != int64(len(src)) {
					t.Fatalf("%s at level %d, %d bytes after: ends at %d, want %d", name, level, len(after), got, len(src))
				}
				endsAt(t, z, in)
			}
		}
	}
}

func FuzzReaderInputOffset(f *testing.F) {
	for _, d := range samples() {
		if len(d) > 2000 {
			d = d[:2000]
		}
		for _, level := range levels {
			f.Add(append(compressed(f, rawFormat, d, level), "PK\x03\x04"...))
		}
	}
	z := NewRawReader(nil, 0)
	f.Fuzz(func(t *testing.T, src []byte) {
		endsAt(t, z, src)
	})
}
