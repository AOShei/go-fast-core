package charset

import (
	"io"
	"unicode/utf8"
)

// MaxChar is the most bytes one character takes in any encoding decoded
// here (a GB18030 four-byte sequence, a UTF-16 surrogate pair).
const MaxChar = 4

// A Decoder decodes a stream in one encoding, in pieces: Decode for a
// piece at a time, Byte for a byte at a time. It carries what one piece
// leaves for the next: the start of a character, and ISO-2022-JP's
// character set. The zero value decodes UTF-8.
type Decoder struct {
	enc  Encoding
	st   state
	pend [MaxChar]byte // Byte: the start of a character
	n    int
}

// NewDecoder returns a Decoder for enc.
func NewDecoder(enc Encoding) *Decoder { return &Decoder{enc: enc} }

// Reset makes d decode enc from the start of a stream, dropping what it
// holds.
func (d *Decoder) Reset(enc Encoding) { *d = Decoder{enc: enc} }

// Encoding returns the encoding d decodes.
func (d *Decoder) Encoding() Encoding { return d.enc }

// byteWise reports whether every ASCII byte is itself in enc, whatever
// came before it.
func byteWise(enc Encoding) bool {
	return enc != UTF16LE && enc != UTF16BE && enc != ISO2022JP
}

// Decode decodes src to UTF-8, appending to dst. Unless final, it stops
// before a character src ends inside of, which the next piece completes.
// It returns the extended dst, the source bytes used and the number of
// byte sequences that were not a character and became U+FFFD.
func (d *Decoder) Decode(dst, src []byte, final bool) (out []byte, used, invalid int) {
	fast := byteWise(d.enc)
	s := 0
	for s < len(src) {
		if b := src[s]; b < 0x80 && fast {
			dst = append(dst, b)
			s++
			continue
		}
		if !final && d.incomplete(src, s) {
			break
		}
		ch, mark, n, bad := d.st.next(src, s, d.enc)
		if bad {
			invalid++
		}
		dst = appendRune(dst, ch, mark)
		s += n
	}
	return dst, s, invalid
}

// Step returns how many bytes of UTF-8 Decode makes of the character at
// src[s:] and how many source bytes it takes, and moves past it as Decode
// does. With a copy of the Decoder kept where a piece starts, it takes
// offsets in the decoded text back to the source.
func (d *Decoder) Step(src []byte, s int) (dec, n int) {
	if src[s] < 0x80 && byteWise(d.enc) {
		return 1, 1
	}
	return d.st.step(src, s, d.enc)
}

// Byte decodes the next byte of the stream. It calls emit for each
// character the byte completes (none while a character is half read),
// with ok false for a U+FFFD that stands for bytes that are not a
// character.
func (d *Decoder) Byte(b byte, emit func(r rune, ok bool)) {
	d.pend[d.n] = b
	d.n++
	d.drain(false, emit)
}

// Flush decodes what Byte holds of a character the stream ended inside
// of, as Decode does at the end of a stream.
func (d *Decoder) Flush(emit func(r rune, ok bool)) { d.drain(true, emit) }

func (d *Decoder) drain(final bool, emit func(r rune, ok bool)) {
	p := d.pend[:d.n]
	s := 0
	for s < len(p) {
		if !final && d.incomplete(p, s) {
			break
		}
		ch, mark, n, bad := d.st.next(p, s, d.enc)
		if ch != noRune {
			emit(ch, !bad)
		}
		if mark != 0 {
			emit(mark, true)
		}
		s += n
	}
	d.n = copy(d.pend[:], p[s:])
}

// incomplete reports whether src[s:] is shorter than the character that
// starts at s may be, so that more input could change how it decodes.
func (d *Decoder) incomplete(src []byte, s int) bool {
	left := len(src) - s
	b := src[s]
	need := 1
	switch d.enc {
	case UTF8:
		switch {
		case b >= 0xF0:
			need = 4
		case b >= 0xE0:
			need = 3
		case b >= 0xC0:
			need = 2
		}
		if need > left && utf8.FullRune(src[s:]) {
			return false // already not a character
		}
	case UTF16LE, UTF16BE:
		need = 2
		if left >= 2 {
			u := rune(src[s+1])<<8 | rune(b)
			if d.enc == UTF16BE {
				u = rune(b)<<8 | rune(src[s+1])
			}
			if u >= 0xD800 && u < 0xDC00 {
				need = 4
			}
		}
	case ShiftJIS, GBK, EUCKR, Big5:
		if isLead(d.enc.codePage(), b) {
			need = 2
		}
	case GB18030:
		if b >= 0x81 && b <= 0xFE {
			need = 2
			if left >= 2 && src[s+1] >= 0x30 && src[s+1] <= 0x39 {
				need = 4
			}
		}
	case EUCJP:
		switch {
		case b == 0x8F:
			need = 3
		case b == 0x8E || b >= 0xA1 && b <= 0xFE:
			need = 2
		}
	case ISO2022JP:
		switch {
		case b == 0x1B:
			need = 3
		case d.st.iso == isoJIS && b >= 0x21 && b <= 0x7E:
			need = 2
		}
	}
	return need > left
}

// A Reader decodes a stream as it is read.
type Reader struct {
	r       io.Reader
	d       Decoder
	in      []byte // read and not yet decoded: in[inPos:inEnd]
	inPos   int
	inEnd   int
	out     []byte // decoded and not yet returned: out[outPos:]
	outPos  int
	err     error
	invalid int
}

// NewReader returns a Reader that reads r, in enc, as UTF-8.
func NewReader(r io.Reader, enc Encoding) *Reader {
	return &Reader{r: r, d: Decoder{enc: enc}, in: make([]byte, 32<<10)}
}

// Invalid returns how many byte sequences read so far were not a
// character and became U+FFFD.
func (r *Reader) Invalid() int { return r.invalid }

// Read reads decoded text into p.
func (r *Reader) Read(p []byte) (int, error) {
	for idle := 0; r.outPos == len(r.out); {
		if r.err != nil && r.inPos == r.inEnd {
			return 0, r.err
		}
		if r.err == nil {
			// Keep what a character was cut inside of, and read more.
			r.inEnd = copy(r.in, r.in[r.inPos:r.inEnd])
			r.inPos = 0
			n, err := r.r.Read(r.in[r.inEnd:])
			r.inEnd += n
			if err != nil {
				r.err = err
			} else if n == 0 {
				if idle++; idle == 100 {
					return 0, io.ErrNoProgress
				}
				continue
			}
		}
		var used, bad int
		r.out, used, bad = r.d.Decode(r.out[:0], r.in[r.inPos:r.inEnd], r.err != nil)
		r.outPos = 0
		r.inPos += used
		r.invalid += bad
	}
	n := copy(p, r.out[r.outPos:])
	r.outPos += n
	return n, nil
}
