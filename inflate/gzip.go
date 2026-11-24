package inflate

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
)

// Gzip appends the decompressed form of the gzip stream src (RFC 1952)
// to dst and returns the result. As compress/gzip does, it reads one
// member after another until src ends, each a deflate stream of its own,
// and checks each one's CRC-32 and size; bytes after a member that do not
// begin another give ErrHeader, and so does a name or comment in a
// header longer than compress/gzip reads (511 bytes). limit and the other
// errors are as for Zlib, the limit counting the output of every member.
func Gzip(dst, src []byte, limit int64) ([]byte, error) {
	base := len(dst)
	end := outputEnd(base, limit)
	pos := 0
	for {
		start, err := gzipHeader(src, pos)
		if err != nil {
			return dst, err
		}
		from := len(dst)
		var after int
		dst, after, err = deflate(dst, src, start, end)
		if err != nil {
			return dst, err
		}
		if after+8 > len(src) {
			return dst, ErrShort
		}
		if binary.LittleEndian.Uint32(src[after:]) != crc32.ChecksumIEEE(dst[from:]) ||
			binary.LittleEndian.Uint32(src[after+4:]) != uint32(len(dst)-from) {
			return dst, ErrChecksum
		}
		pos = after + 8
		if pos == len(src) {
			return dst, nil
		}
	}
}

// The flags of a gzip header.
const (
	gzipText    = 1 << 0
	gzipHCRC    = 1 << 1
	gzipExtra   = 1 << 2
	gzipName    = 1 << 3
	gzipComment = 1 << 4
)

// maxGzipString bounds the name and the comment of a gzip header, with
// the zero that ends each, as compress/gzip bounds them.
const maxGzipString = 512

// gzipHeader reads the header of a gzip member at src[pos] and returns
// where its deflate stream begins.
func gzipHeader(src []byte, pos int) (int, error) {
	h := src[pos:]
	if len(h) < 10 {
		if len(h) == 0 || h[0] == 0x1f && (len(h) == 1 || h[1] == 0x8b && (len(h) == 2 || h[2] == 8)) {
			// What there is begins as a header would: it is cut off.
			return 0, ErrShort
		}
		return 0, ErrHeader
	}
	if h[0] != 0x1f || h[1] != 0x8b || h[2] != 8 {
		return 0, ErrHeader
	}
	flags := h[3]
	at := 10
	if flags&gzipExtra != 0 {
		if at+2 > len(h) {
			return 0, ErrShort
		}
		at += 2 + int(binary.LittleEndian.Uint16(h[at:]))
		if at > len(h) {
			return 0, ErrShort
		}
	}
	for _, f := range []byte{gzipName, gzipComment} {
		if flags&f != 0 {
			// As compress/gzip reads them: at most 511 bytes and the
			// zero that ends them.
			s := h[at:min(len(h), at+maxGzipString)]
			i := bytes.IndexByte(s, 0)
			switch {
			case i < 0 && len(s) == maxGzipString:
				return 0, ErrHeader
			case i < 0:
				return 0, ErrShort
			}
			at += i + 1
		}
	}
	if flags&gzipHCRC != 0 {
		if at+2 > len(h) {
			return 0, ErrShort
		}
		if binary.LittleEndian.Uint16(h[at:]) != uint16(crc32.ChecksumIEEE(h[:at])) {
			return 0, ErrHeader
		}
		at += 2
	}
	return pos + at, nil
}
