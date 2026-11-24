package inflate

import (
	"compress/flate"
	"io"
	"sync"
)

// Decompressor decompresses the deflated members of a zip archive with a
// Reader. Register it on each archive read:
//
//	zr.RegisterDecompressor(zip.Deflate, inflate.Decompressor)
//
// It reads a member's compressed bytes into a buffer and gives its
// output through a Reader, which holds 96 KiB of it at a time.
// archive/zip checks the CRC-32 and size on top, as it does with
// compress/flate. Buffers and Readers are reused from one member to the
// next, and given back when the member is closed. A member whose
// compressed size is over 32 MiB, or one archive/zip hands over in a way
// it did not before, is read with compress/flate, which does not hold its
// compressed bytes: the archive may be read from a file, not memory.
//
// It gives what compress/flate gives, but for one difference on a damaged
// member, which comes from archive/zip: it drops the read that ends the
// data short of the member's declared size, and the read that passes it.
// What is dropped depends on how the output is cut into reads: up to
// 32 KiB at a time by compress/flate, which gives its last piece in the
// read that ends the data, and at most 4 KiB here (maxRead). So a damaged
// member gives at most 4 KiB less of its output before the error, and
// often up to 32 KiB more. A damaged member is reported damaged either
// way.
func Decompressor(r io.Reader) io.ReadCloser {
	sr, ok := r.(*io.SectionReader)
	if !ok || sr.Size() > maxBuffered {
		return flate.NewReader(r)
	}
	size := int(sr.Size())
	var buf []byte
	if p, _ := inputs.Get().(*[]byte); p != nil && cap(*p) >= size {
		buf = (*p)[:size]
	} else {
		if p != nil {
			inputs.Put(p)
		}
		buf = make([]byte, size)
	}
	n, _ := io.ReadFull(sr, buf)
	z := readers.Get().(*Reader)
	// What could not be read of the member is missing from its end, and
	// reads as a stream that ends early. The archive's own limits bound
	// the output.
	z.Reset(buf[:n], 1<<62)
	return &member{z: z, buf: buf}
}

const (
	// maxBuffered bounds the compressed bytes of a member that are
	// read into memory.
	maxBuffered = 32 << 20
	// maxPooled bounds a buffer kept for the next member.
	maxPooled = 4 << 20
	// maxRead bounds the output one Read gives, and so what archive/zip
	// drops of a damaged member (see Decompressor).
	maxRead = 4 << 10
)

var (
	readers = sync.Pool{New: func() any { return NewRawReader(nil, 0) }}
	inputs  sync.Pool
)

// member is a deflated member being read: its Reader and the buffer of
// its compressed bytes, given back on Close.
type member struct {
	z   *Reader
	buf []byte
}

func (m *member) Read(p []byte) (int, error) {
	if m.z == nil {
		return 0, io.ErrClosedPipe
	}
	return m.z.Read(p[:min(len(p), maxRead)])
}

func (m *member) Close() error {
	if m.z == nil {
		return nil
	}
	m.z.Reset(nil, 0)
	readers.Put(m.z)
	if cap(m.buf) <= maxPooled {
		b := m.buf[:0]
		inputs.Put(&b)
	}
	m.z, m.buf = nil, nil
	return nil
}
