package inflate

import "io"

// A Reader decompresses a raw deflate stream held in memory (RFC 1951,
// as a zip member is) a piece at a time. It holds the last 32 KiB of the
// output, which matches may copy from, and a piece of 64 KiB, not the
// whole output as Raw does: for a member too large to hold whole.
//
// It gives the bytes Raw gives and then the error Raw would return, or
// io.EOF where Raw returns none. Reset readies it for another stream,
// keeping its buffer and tables. A Reader is for one goroutine at a time.
type Reader struct {
	d     *decoder
	buf   []byte
	r     int   // the next byte of buf to give
	limit int64 // the output the stream may give
	err   error // the error once the stream is done, io.EOF if none
}

const (
	window = 32 << 10 // the farthest back a match reaches
	piece  = 64 << 10
)

// NewRawReader returns a Reader of the raw deflate stream src. limit
// bounds the output, as for Raw.
func NewRawReader(src []byte, limit int64) *Reader {
	z := &Reader{d: newDecoder(), buf: make([]byte, window+piece)}
	z.Reset(src, limit)
	return z
}

// Reset makes z read the raw deflate stream src from its start.
func (z *Reader) Reset(src []byte, limit int64) {
	d := z.d
	d.start(src, 0)
	d.stream = true
	d.out, d.w, d.base = nil, 0, 0
	z.r, z.limit, z.err = 0, max(limit, 0), nil
}

// Read gives the next bytes of the output.
func (z *Reader) Read(p []byte) (int, error) {
	d := z.d
	for {
		if z.r < d.w {
			n := copy(p, z.buf[z.r:d.w])
			z.r += n
			return n, nil
		}
		if z.err != nil {
			return 0, z.err
		}
		if len(p) == 0 {
			return 0, nil
		}
		if d.w > window {
			// Everything has been given: keep the last 32 KiB for the
			// matches to come. base, where the output began, moves back
			// with it, past the start of the buffer.
			shift := d.w - window
			copy(z.buf, z.buf[shift:d.w])
			d.w -= shift
			d.base -= shift
			z.r = d.w
		}
		// The output may not pass the limit: base+limit, where base is
		// at most 0 and the output given so far is w-base.
		if end := int64(d.base) + z.limit; end <= int64(len(z.buf)) {
			d.out, d.atLimit = z.buf[:end], true
		} else {
			d.out, d.atLimit = z.buf, false
		}
		d.limit = len(d.out)
		switch err := d.blocks(); err {
		case errFull:
		case nil:
			z.err = io.EOF
		default:
			z.err = err
		}
	}
}

// InputOffset returns how many bytes of src the stream has taken. Once
// Read has returned io.EOF it is where the stream ended: the byte after
// its last block, as bytes after it are not read. Before that, or after
// an error, it is how far decoding has read.
func (z *Reader) InputOffset() int64 {
	return int64(z.d.pos - int(z.d.n>>3))
}
