package inflate

import (
	"encoding/binary"
	"hash/crc32"
	"io"
)

// A GzipReader decompresses a gzip stream held in memory (RFC 1952) a
// piece at a time, as a Reader does a raw one: for a file too large to
// hold decompressed, such as a .tar.gz. It gives the bytes Gzip gives and
// then the error Gzip would return, or io.EOF where Gzip returns none.
// Reset readies it for another stream. A GzipReader is for one goroutine
// at a time.
type GzipReader struct {
	z     *Reader
	src   []byte
	pos   int    // the next member's header, when not in a member
	in    bool   // in a member
	crc   uint32 // of the member's output so far
	size  uint32 // the member's output so far, modulo 2^32
	left  int64  // the output the stream may still give
	err   error
	first bool // no member has been read yet
}

// NewGzipReader returns a GzipReader of the gzip stream src. limit bounds
// the output, as for Gzip.
func NewGzipReader(src []byte, limit int64) *GzipReader {
	g := &GzipReader{z: NewRawReader(nil, 0)}
	g.Reset(src, limit)
	return g
}

// Reset makes g read the gzip stream src from its start.
func (g *GzipReader) Reset(src []byte, limit int64) {
	g.src, g.pos, g.in, g.err, g.first = src, 0, false, nil, true
	g.left = max(limit, 0)
	g.z.Reset(nil, 0)
}

// Read gives the next bytes of the output.
func (g *GzipReader) Read(p []byte) (int, error) {
	for {
		if g.err != nil {
			return 0, g.err
		}
		if !g.in {
			if !g.first && g.pos == len(g.src) {
				g.err = io.EOF
				continue
			}
			start, err := gzipHeader(g.src, g.pos)
			if err != nil {
				g.err = err
				continue
			}
			g.first, g.in, g.crc, g.size = false, true, 0, 0
			g.z.Reset(g.src[start:], g.left)
			// The member's offsets are counted from its stream.
			g.pos = start
		}
		n, err := g.z.Read(p)
		g.crc = crc32.Update(g.crc, crc32.IEEETable, p[:n])
		g.size += uint32(n)
		g.left -= int64(n)
		if n > 0 {
			return n, nil
		}
		if err != io.EOF {
			g.err = err
			continue
		}
		// The member's stream is done: its trailer follows the last byte
		// of its last block.
		after := g.pos + int(g.z.InputOffset())
		g.in = false
		switch {
		case after+8 > len(g.src):
			g.err = ErrShort
		case binary.LittleEndian.Uint32(g.src[after:]) != g.crc || binary.LittleEndian.Uint32(g.src[after+4:]) != g.size:
			g.err = ErrChecksum
		default:
			g.pos = after + 8
		}
	}
}
