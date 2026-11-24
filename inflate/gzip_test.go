package inflate

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"
)

// gzipWithHeader is a gzip member of data whose header has the flags,
// with a name, a comment, extra bytes and a header CRC where they say.
func gzipWithHeader(flags byte, data []byte) []byte {
	h := []byte{0x1f, 0x8b, 8, flags, 1, 2, 3, 4, 0, 3}
	if flags&gzipExtra != 0 {
		h = append(h, 5, 0, 'e', 'x', 't', 'r', 'a')
	}
	if flags&gzipName != 0 {
		h = append(h, "name.txt\x00"...)
	}
	if flags&gzipComment != 0 {
		h = append(h, "a comment\x00"...)
	}
	if flags&gzipHCRC != 0 {
		h = binary.LittleEndian.AppendUint16(h, uint16(crc32.ChecksumIEEE(h)))
	}
	return gzipMember(h, rawDeflate(data), data)
}

// gzipMember is a member of the header, the deflate stream and the
// trailer of data.
func gzipMember(header, stream, data []byte) []byte {
	m := append(append([]byte(nil), header...), stream...)
	m = binary.LittleEndian.AppendUint32(m, crc32.ChecksumIEEE(data))
	return binary.LittleEndian.AppendUint32(m, uint32(len(data)))
}

func rawDeflate(data []byte) []byte {
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.DefaultCompression)
	w.Write(data)
	w.Close()
	return buf.Bytes()
}

// Every header the flags can make reads, and so does each one cut off at
// every byte, as compress/gzip reads them.
func TestGzipHeaders(t *testing.T) {
	data := []byte("the text of the member, the text of the member")
	for flags := byte(0); flags < 32; flags++ {
		src := gzipWithHeader(flags, data)
		got, err := Gzip(nil, src, 1<<20)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("flags %05b: %q, %v", flags, got, err)
		}
		for cut := 0; cut <= len(src); cut++ {
			same(t, gzipFormat, src[:cut], 1<<20, nil)
		}
	}
	bad := gzipWithHeader(gzipHCRC, data)
	bad[10]++ // the header CRC
	if _, err := Gzip(nil, bad, 1<<20); !errors.Is(err, ErrHeader) {
		t.Errorf("a wrong header CRC: %v", err)
	}
	same(t, gzipFormat, bad, 1<<20, nil)
	for _, h := range [][]byte{{0x1f, 0x8c, 8}, {0x1e}, {0x1f, 0x8b, 7, 0, 0, 0, 0, 0, 0, 0}} {
		src := append(h, rawDeflate(data)...)
		if _, err := Gzip(nil, src, 1<<20); !errors.Is(err, ErrHeader) {
			t.Errorf("%x: %v", h, err)
		}
		same(t, gzipFormat, src, 1<<20, nil)
	}
}

// Members follow one another; what follows the last must be another.
func TestGzipMembers(t *testing.T) {
	one := gzipWithHeader(0, []byte("first member "))
	two := gzipWithHeader(gzipName, []byte("second member"))
	got, err := Gzip([]byte("before "), append(append([]byte(nil), one...), two...), 1<<20)
	if err != nil || string(got) != "before first member second member" {
		t.Fatalf("%q, %v", got, err)
	}
	for _, src := range [][]byte{
		append(append([]byte(nil), one...), two...),
		append(append([]byte(nil), one...), 0, 0, 0, 0),
		append(append([]byte(nil), one...), "trailing text"...),
		append(append([]byte(nil), one...), 0x1f),
		append(append([]byte(nil), one...), two[:12]...),
	} {
		same(t, gzipFormat, src, 1<<20, nil)
		for limit := int64(0); limit < 30; limit++ {
			same(t, gzipFormat, src, limit, nil)
		}
	}
	// A match in the second member cannot reach into the first: "a", then
	// three bytes from two back.
	src := append(gzipWithHeader(0, []byte("abcabc")), gzipMember([]byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 3}, []byte{0x4b, 0x04, 0x12, 0x00}, []byte("aaaa"))...)
	got, err = Gzip(nil, src, 1<<20)
	if !errors.Is(err, ErrCorrupt) || string(got) != "abcabca" {
		t.Errorf("a match into the member before: %q, %v", got, err)
	}
	same(t, gzipFormat, src, 1<<20, nil)
}

// The CRC-32 and the size of each member are checked.
func TestGzipChecksum(t *testing.T) {
	src := gzipWithHeader(0, []byte("checked text"))
	for _, at := range []int{len(src) - 8, len(src) - 1} {
		bad := append([]byte(nil), src...)
		bad[at] ^= 1
		got, err := Gzip(nil, bad, 1<<20)
		if !errors.Is(err, ErrChecksum) || string(got) != "checked text" {
			t.Errorf("byte %d of %d changed: %q, %v", at, len(src), got, err)
		}
		same(t, gzipFormat, bad, 1<<20, nil)
	}
}
