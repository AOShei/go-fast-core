package inflate

import (
	"bytes"
	"io"
	"math/rand"
	"testing"
)

// sameAsGzip compares a GzipReader of src, read in reads of the sizes,
// with Gzip: the same bytes and the same error.
func sameAsGzip(t testing.TB, g *GzipReader, src []byte, limit int64, sizes []int) {
	t.Helper()
	want, wantErr := Gzip(nil, src, limit)
	g.Reset(src, limit)
	var got []byte
	var err error
	for i := 0; ; i++ {
		p := make([]byte, sizes[i%len(sizes)])
		n, e := g.Read(p)
		got = append(got, p[:n]...)
		if e != nil {
			if e != io.EOF {
				err = e
			}
			break
		}
		if n == 0 && len(p) > 0 {
			t.Fatalf("a Read of %d gave nothing and no error", len(p))
		}
	}
	if !bytes.Equal(got, want) || err != wantErr {
		t.Fatalf("limit %d, reads %v: %d bytes (%v), Gzip gives %d (%v)\ninput %x", limit, sizes, len(got), err, len(want), wantErr, clip(src))
	}
}

func TestGzipReaderGivesWhatGzipGives(t *testing.T) {
	g := NewGzipReader(nil, 0)
	data := samples()
	data["large"] = large()
	for _, d := range data {
		for _, level := range levels {
			src := compressed(t, gzipFormat, d, level)
			for _, sizes := range readSizes {
				if sizes[0] == 0 {
					continue
				}
				sameAsGzip(t, g, src, 1<<30, sizes)
			}
		}
	}
	// Members one after another, with flags, cut off, followed by junk,
	// limited.
	one := gzipWithHeader(gzipName|gzipExtra|gzipHCRC, large()[:200000])
	two := gzipWithHeader(gzipComment, []byte("second member"))
	both := append(append([]byte(nil), one...), two...)
	for _, src := range [][]byte{nil, {0x1f}, both, append(append([]byte(nil), one...), "junk"...), one[:len(one)-3], both[:len(one)+5]} {
		for _, limit := range []int64{0, 1, 1000, 200000, 200005, 1 << 30} {
			sameAsGzip(t, g, src, limit, []int{4096})
		}
	}
	rng := rand.New(rand.NewSource(3))
	for range 500 {
		bad := append([]byte(nil), both...)
		bit := rng.Intn(8 * len(bad))
		bad[bit/8] ^= 1 << (bit % 8)
		sameAsGzip(t, g, bad, 1<<30, []int{7000})
	}
}

func FuzzGzipReader(f *testing.F) {
	for _, d := range samples() {
		if len(d) > 2000 {
			d = d[:2000]
		}
		f.Add(compressed(f, gzipFormat, d, 6), int64(1<<20), uint16(100))
	}
	f.Add(append(gzipWithHeader(gzipName, []byte("one")), gzipWithHeader(0, []byte("two"))...), int64(100), uint16(1))
	g := NewGzipReader(nil, 0)
	f.Fuzz(func(t *testing.T, src []byte, limit int64, size uint16) {
		if limit < 0 || limit > 1<<24 {
			limit = 1 << 24
		}
		sameAsGzip(t, g, src, limit, []int{int(size)%9000 + 1, 1})
	})
}
