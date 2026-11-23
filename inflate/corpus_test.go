package inflate

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"io"
	"os"
	"sync"
	"testing"
)

// zipMembers are the deflated members of the zip files (ODF, OOXML, EPUB,
// any) listed one a line in the file GOFASTCORE_INFLATE_ZIPS names: each
// member's compressed bytes and its size. Nil when the variable is not
// set.
var zipMembers = sync.OnceValues(func() (streams [][]byte, sizes []int) {
	list := os.Getenv("GOFASTCORE_INFLATE_ZIPS")
	if list == "" {
		return nil, nil
	}
	f, err := os.Open(list)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		data, err := os.ReadFile(sc.Text())
		if err != nil {
			continue
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			continue
		}
		for _, m := range zr.File {
			if m.Method != zip.Deflate || m.UncompressedSize64 > 64<<20 {
				continue
			}
			off, err := m.DataOffset()
			if err != nil || off+int64(m.CompressedSize64) > int64(len(data)) {
				continue
			}
			streams = append(streams, data[off:off+int64(m.CompressedSize64)])
			sizes = append(sizes, int(m.UncompressedSize64))
		}
	}
	return streams, sizes
})

// TestCorpusLikeCompressFlate: every member gives what compress/flate
// gives, damaged ones included.
func TestCorpusLikeCompressFlate(t *testing.T) {
	streams, _ := zipMembers()
	if streams == nil {
		t.Skip("GOFASTCORE_INFLATE_ZIPS names no list of zip files")
	}
	for _, s := range streams {
		same(t, rawFormat, s, 1<<30, nil)
	}
	t.Logf("%d members", len(streams))
}

// BenchmarkRawCorpus decompresses every member, with this package and
// with compress/flate.
func BenchmarkRawCorpus(b *testing.B) {
	streams, sizes := zipMembers()
	if streams == nil {
		b.Skip("GOFASTCORE_INFLATE_ZIPS names no list of zip files")
	}
	total := 0
	for _, n := range sizes {
		total += n
	}
	b.Run("inflate", func(b *testing.B) {
		b.SetBytes(int64(total))
		var dst []byte
		for b.Loop() {
			for i, s := range streams {
				if cap(dst) < sizes[i]+fastRoom {
					dst = make([]byte, 0, sizes[i]+fastRoom)
				}
				Raw(dst[:0], s, 1<<30)
			}
		}
	})
	b.Run("compress-flate", func(b *testing.B) {
		b.SetBytes(int64(total))
		fr := flate.NewReader(nil)
		var dst []byte
		for b.Loop() {
			for i, s := range streams {
				if cap(dst) < sizes[i] {
					dst = make([]byte, sizes[i])
				}
				fr.(flate.Resetter).Reset(bytes.NewReader(s), nil)
				io.ReadFull(fr, dst[:sizes[i]])
			}
		}
	})
}
