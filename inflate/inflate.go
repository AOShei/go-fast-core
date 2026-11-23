// Package inflate decompresses deflate streams (RFC 1951) held in
// memory: raw, as zip members are; in zlib's wrapping (RFC 1950), as PDF
// streams are; and in gzip's (RFC 1952).
//
// Raw, Zlib and Gzip do what compress/flate, compress/zlib and
// compress/gzip do with such a stream and give the same bytes, also for a
// stream that is damaged or cut off: everything that was decompressed
// before the damage. They are faster because they do not have to serve a
// reader: the input is one slice and the output is appended to another,
// which is also the window that matches copy from. The code came from
// go-fast-pdf, where it was written and measured.
package inflate

import (
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
	"sync"
)

// The ways a stream can fail. With each of them the output decompressed
// so far is returned.
var (
	// ErrHeader: the header that starts a zlib or gzip stream is not
	// there.
	ErrHeader = errors.New("inflate: invalid header")
	// ErrCorrupt: the data is not a deflate stream.
	ErrCorrupt = errors.New("inflate: corrupt data")
	// ErrShort: the stream ends before its last block does, or before
	// its trailer.
	ErrShort = errors.New("inflate: the data ends early")
	// ErrChecksum: the output is not what the checksum (or, in gzip, the
	// size) was made of.
	ErrChecksum = errors.New("inflate: checksum does not match")
	// ErrLimit: the output would be longer than the limit. The output up
	// to the limit is returned.
	ErrLimit = errors.New("inflate: output longer than the limit")
)

const (
	// The bits of a code that index the first level of a table. Codes
	// longer than that go through a second level.
	litBits  = 10
	distBits = 7
	lenBits  = 7 // the code of the code lengths has no longer codes

	maxCodeLen = 15
	numLit     = 286 // literal and length symbols that may occur
	numDist    = 30
	endOfBlock = 256

	// fastRoom is the room in the output that one round of the fast loop
	// may use: three literals, the longest match, and the bytes that a
	// copy in steps of eight writes past its end.
	fastRoom = 3 + 258 + 8
	// fastInput is the input one round of the fast loop may load: two
	// times eight bytes.
	fastInput = 16
)

// A table entry, 32 bits:
//
//	bits 0-4    the length of the code: the bits to take from the input
//	bits 5-7    the kind
//	bits 8-11   the number of extra bits that follow the code, or for a
//	            link the number of bits that index the second level
//	bits 16-31  the literal, the base of the length or distance, or for a
//	            link the place of the second level in the table
//
// The zero entry is a code that the block does not have.
const (
	kindBad  = 0 << 5
	kindLit  = 1 << 5
	kindBase = 2 << 5 // a length in the literal table, a distance in the other
	kindEnd  = 3 << 5
	kindLink = 4 << 5
	kindMask = 7 << 5
)

var (
	lengthBase  = [29]uint16{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258}
	lengthExtra = [29]uint8{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}
	distBase    = [30]uint16{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577}
	distExtra   = [30]uint8{0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13}
	// The order in which the lengths of the code length code are stored.
	lengthOrder = [19]uint8{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}
)

// What the symbols of a table stand for.
const (
	forLiterals = iota
	forDistances
	forLengths // the code that the code lengths are written in
)

func entryFor(use, sym int, length uint32) uint32 {
	switch use {
	case forLiterals:
		switch {
		case sym < endOfBlock:
			return uint32(sym)<<16 | kindLit | length
		case sym == endOfBlock:
			return kindEnd | length
		case sym < numLit:
			return uint32(lengthBase[sym-257])<<16 | uint32(lengthExtra[sym-257])<<8 | kindBase | length
		}
	case forDistances:
		if sym < numDist {
			return uint32(distBase[sym])<<16 | uint32(distExtra[sym])<<8 | kindBase | length
		}
	case forLengths:
		return uint32(sym)<<16 | kindLit | length
	}
	// A symbol that has a code and no meaning: 286 and 287 among the
	// literals, 30 and 31 among the distances.
	return kindBad | length
}

// build makes the decoding table of the code whose lengths are given,
// in t, and returns it with the length of its shortest code. ok is false
// for lengths that do not describe a code: too many codes or too few.
// As in zlib, one code of one bit is allowed, and so is no code at all;
// what such a table does not have reads as a code that is not there.
func build(t []uint32, lens []uint8, first uint, use int) (table []uint32, shortest uint, ok bool) {
	var count [maxCodeLen + 1]int
	for _, n := range lens {
		count[n&15]++
	}
	count[0] = 0
	longest := uint(0)
	for n := uint(1); n <= maxCodeLen; n++ {
		if count[n] > 0 {
			if shortest == 0 {
				shortest = n
			}
			longest = n
		}
	}
	// What is left in the table from the code before must not be taken
	// for a link.
	t = t[:1<<first]
	clear(t)
	if longest == 0 {
		return t, 0, true
	}
	// The codes of each length take their share of the code space; at
	// the end all of it must be taken.
	left := 1
	for n := uint(1); n <= longest; n++ {
		left = left<<1 - count[n]
		if left < 0 {
			return t, 0, false
		}
	}
	if left > 0 {
		if longest != 1 || count[1] != 1 {
			return t, 0, false
		}
	}

	// The first code of each length, and the symbols in the order of
	// their codes.
	var next [maxCodeLen + 2]uint32
	var start [maxCodeLen + 2]int
	code, at := uint32(0), 0
	for n := uint(1); n <= longest; n++ {
		code <<= 1
		next[n] = code
		code += uint32(count[n])
		start[n] = at
		at += count[n]
	}
	var sorted [numLit + 2 + numDist + 2]uint16
	for sym, n := range lens {
		if n != 0 {
			sorted[start[n]] = uint16(sym)
			start[n]++
		}
	}

	sub := uint(0) // the bits that index a second level
	if longest > first {
		sub = longest - first
	}
	i := 0
	for n := uint(1); n <= longest; n++ {
		for c := count[n]; c > 0; c-- {
			sym := int(sorted[i])
			i++
			// The code is read from the input lowest bit first.
			rev := uint32(bits.Reverse16(uint16(next[n]))) >> (16 - n)
			next[n]++
			e := entryFor(use, sym, uint32(n))
			if n <= first {
				for j := rev; j < 1<<first; j += 1 << n {
					t[j] = e
				}
				continue
			}
			head := rev & (1<<first - 1)
			link := t[head]
			if link&kindMask != kindLink {
				// The first code with these first bits: a second level
				// for all that share them. A complete code fills it.
				link = uint32(len(t))<<16 | uint32(sub)<<8 | kindLink
				t[head] = link
				t = append(t, make([]uint32, 1<<sub)...)
			}
			second := t[link>>16:]
			for j := rev >> first; j < 1<<sub; j += 1 << (n - first) {
				second[j] = e
			}
		}
	}
	return t, shortest, true
}

// The code of a block that does not bring its own.
var fixed struct {
	once sync.Once
	lit  []uint32
	dist []uint32
}

func fixedTables() ([]uint32, []uint32) {
	fixed.once.Do(func() {
		var lens [288]uint8
		for i := range lens {
			switch {
			case i < 144:
				lens[i] = 8
			case i < 256:
				lens[i] = 9
			case i < 280:
				lens[i] = 7
			default:
				lens[i] = 8
			}
		}
		fixed.lit, _, _ = build(make([]uint32, 0, 1<<litBits), lens[:], litBits, forLiterals)
		for i := range lens[:32] {
			lens[i] = 5
		}
		fixed.dist, _, _ = build(make([]uint32, 0, 1<<distBits), lens[:32], distBits, forDistances)
	})
	return fixed.lit, fixed.dist
}

// decoder is the state of one decompression. Its tables are kept for the
// next one.
type decoder struct {
	src []byte
	pos int    // the input loaded into acc
	acc uint64 // bits not yet used, the next one lowest
	n   uint   // the number of bits in acc that count

	out   []byte // the output so far and room after it
	w     int    // the place of the next byte of output
	base  int    // where the output of this stream starts
	limit int    // the place the output may not pass

	lit, dist, len []uint32
	lens           [numLit + 2 + numDist + 2]uint8

	// Where the stream is, so that a Reader can stop when its buffer is
	// full and go on: the block being read, and what was left of a
	// symbol when it stopped.
	state      uint8 // stateHeader, stateStored, stateHuffman
	final      bool  // the block being read is the last
	curLit     []uint32
	curDist    []uint32
	least      uint
	storedLeft int  // bytes of a stored block still to copy; -1 before its length is read
	hasLit     bool // litByte is still to be written
	litByte    byte
	pendLen    int // bytes of a match still to be copied, from pendBack back
	pendBack   int
	// stream: a Reader's decoder, whose out is a buffer it empties, not
	// grows; atLimit: the end of that buffer is also the limit.
	stream, atLimit bool
}

// The states of a decoder between calls of blocks.
const (
	stateHeader  = iota // at the start of a block
	stateStored         // in a stored block
	stateHuffman        // in a compressed block
)

// errFull: a Reader's buffer is full; the decoder stopped where it can
// go on.
var errFull = errors.New("inflate: buffer full")

func newDecoder() *decoder {
	return &decoder{
		lit:  make([]uint32, 0, 1<<litBits+512),
		dist: make([]uint32, 0, 1<<distBits+256),
		len:  make([]uint32, 0, 1<<lenBits),
	}
}

var decoders = sync.Pool{New: func() any { return newDecoder() }}

// start readies d for a stream that begins at src[pos].
func (d *decoder) start(src []byte, pos int) {
	d.src, d.pos, d.acc, d.n = src, pos, 0, 0
	d.state, d.final, d.storedLeft = stateHeader, false, -1
	d.hasLit, d.pendLen, d.pendBack = false, 0, 0
}

// full is the error for output that has reached the end of out: errFull
// for a Reader whose buffer it is, ErrLimit at the limit.
func (d *decoder) full() error {
	if d.stream && !d.atLimit {
		return errFull
	}
	return ErrLimit
}

// Zlib appends the decompressed form of the zlib stream src to dst and
// returns the result. limit bounds what is appended; at the limit the
// result is cut and the error is ErrLimit. For a damaged stream the
// result holds what was decompressed before the damage.
func Zlib(dst, src []byte, limit int64) ([]byte, error) {
	if len(src) < 2 {
		return dst, ErrShort
	}
	if src[0]&0x0f != 8 || src[0]>>4 > 7 || (uint(src[0])<<8|uint(src[1]))%31 != 0 {
		return dst, ErrHeader
	}
	pos := 2
	if src[1]&0x20 != 0 {
		// A preset dictionary is named. There is none here, and only
		// the checksum of an empty one is taken for one.
		if len(src) < 6 {
			return dst, ErrShort
		}
		if binary.BigEndian.Uint32(src[2:]) != 1 {
			return dst, ErrHeader
		}
		pos = 6
	}
	base := len(dst)
	out, trailer, err := deflate(dst, src, pos, outputEnd(base, limit))
	if err != nil {
		return out, err
	}
	// The checksum follows the last byte that the blocks used.
	if trailer+4 > len(src) {
		return out, ErrShort
	}
	if binary.BigEndian.Uint32(src[trailer:]) != adler32(out[base:]) {
		return out, ErrChecksum
	}
	return out, nil
}

// Raw appends the decompressed form of the deflate stream src (RFC 1951,
// with no header and no checksum, as in a zip member) to dst and returns
// the result. Bytes after the last block are not read. limit and the
// errors are as for Zlib.
func Raw(dst, src []byte, limit int64) ([]byte, error) {
	out, _, err := deflate(dst, src, 0, outputEnd(len(dst), limit))
	return out, err
}

// outputEnd is the place in the output that limit bytes after base reach,
// or the largest int.
func outputEnd(base int, limit int64) int {
	limit = max(limit, 0)
	if limit < int64(math.MaxInt-base) {
		return base + int(limit)
	}
	return math.MaxInt
}

// deflate appends the output of the deflate stream that starts at
// src[pos] to dst, up to the place end, and returns it with the place in
// src just after the stream's last block.
func deflate(dst, src []byte, pos, end int) ([]byte, int, error) {
	base := len(dst)
	if cap(dst)-len(dst) < 512 {
		// A first guess at the size: text compresses to about a third.
		// Counted in 64 bits: three times a large input is more than
		// an int holds on a 32-bit system.
		guess := int(min(int64(len(src)-pos)*3+512, int64(end-base)))
		grown := make([]byte, len(dst), len(dst)+guess)
		copy(grown, dst)
		dst = grown
	}

	d := decoders.Get().(*decoder)
	d.start(src, pos)
	d.stream = false
	d.out, d.w, d.base, d.limit = dst[:min(cap(dst), end)], base, base, end
	err := d.blocks()
	out, w, after := d.out, d.w, d.pos-int(d.n>>3)
	d.src, d.out = nil, nil
	decoders.Put(d)
	return out[:w], after, err
}

// fill loads input until 56 bits are there or the input is at its end.
// There are never more than 63: the fast loop counts on that.
func (d *decoder) fill() {
	for d.n < 56 && d.pos < len(d.src) {
		d.acc |= uint64(d.src[d.pos]) << d.n
		d.pos++
		d.n += 8
	}
}

// take returns the next n bits, n at most 16. ok is false when the input
// ends before them.
func (d *decoder) take(n uint) (v uint32, ok bool) {
	if d.n < n {
		d.fill()
		if d.n < n {
			return 0, false
		}
	}
	v = uint32(d.acc) & (1<<n - 1)
	d.acc >>= n
	d.n -= n
	return v, true
}

// grow makes room for more output, as far as the limit allows.
func (d *decoder) grow() {
	size := len(d.out)
	add := max(min(size, d.limit-size), fastRoom)
	if size+add > d.limit {
		add = d.limit - size
	}
	grown := make([]byte, size+add)
	copy(grown, d.out[:d.w])
	d.out = grown
}

// blocks decodes the blocks of the stream, from where d is. With
// errFull it stops where it can go on.
func (d *decoder) blocks() error {
	for {
		var err error
		switch d.state {
		case stateHeader:
			head, ok := d.take(3)
			if !ok {
				return ErrShort
			}
			d.final = head&1 != 0
			switch head >> 1 {
			case 0:
				d.state, d.storedLeft = stateStored, -1
			case 1:
				d.curLit, d.curDist = fixedTables()
				d.least, d.state = 7, stateHuffman
			case 2:
				shortest, err := d.header()
				if err != nil {
					return err
				}
				d.curLit, d.curDist, d.least, d.state = d.lit, d.dist, shortest, stateHuffman
			default:
				return ErrCorrupt
			}
			continue
		case stateStored:
			err = d.stored()
		case stateHuffman:
			err = d.huffman(d.curLit, d.curDist, d.least)
		}
		if err != nil {
			return err
		}
		d.state = stateHeader
		if d.final {
			return nil
		}
	}
}

// stored copies a block that is not compressed, or what is left of it.
func (d *decoder) stored() error {
	src := d.src
	if d.storedLeft < 0 {
		// The block starts at the next whole byte. Give back the bytes
		// that were loaded and not used.
		d.pos -= int(d.n >> 3)
		d.acc, d.n = 0, 0
		if d.pos+4 > len(src) {
			d.pos = len(src)
			return ErrShort
		}
		size := int(binary.LittleEndian.Uint16(src[d.pos:]))
		if uint16(size) != ^binary.LittleEndian.Uint16(src[d.pos+2:]) {
			return ErrCorrupt
		}
		d.pos += 4
		d.storedLeft = size
	}
	n := min(d.storedLeft, len(src)-d.pos)
	room := min(n, d.limit-d.w)
	for len(d.out)-d.w < room {
		d.grow()
	}
	copy(d.out[d.w:], src[d.pos:d.pos+room])
	d.w += room
	d.pos += room
	d.storedLeft -= room
	switch {
	case room < n:
		return d.full()
	case d.storedLeft > 0:
		return ErrShort
	}
	return nil
}

// header reads the code of a block that brings its own, into d.lit and
// d.dist. It returns the fewest bits that a literal or length may be
// read from.
func (d *decoder) header() (uint, error) {
	v, ok := d.take(14)
	if !ok {
		return 0, ErrShort
	}
	nlit, ndist, nlen := int(v&31)+257, int(v>>5&31)+1, int(v>>10)+4
	if nlit > numLit || ndist > numDist {
		return 0, ErrCorrupt
	}
	var order [19]uint8
	for i := 0; i < nlen; i++ {
		v, ok := d.take(3)
		if !ok {
			return 0, ErrShort
		}
		order[lengthOrder[i]] = uint8(v)
	}
	var shortest uint
	if d.len, shortest, ok = build(d.len, order[:], lenBits, forLengths); !ok {
		return 0, ErrCorrupt
	}
	lens := d.lens[:nlit+ndist]
	for i := 0; i < len(lens); {
		d.fill()
		e := d.len[d.acc&(1<<lenBits-1)]
		n := uint(e & 31)
		if d.n < shortest || n > d.n {
			return 0, ErrShort
		}
		if e&kindMask == kindBad {
			return 0, ErrCorrupt
		}
		d.acc >>= n
		d.n -= n
		sym := e >> 16
		if sym < 16 {
			lens[i] = uint8(sym)
			i++
			continue
		}
		var repeat int
		var value uint8
		var extra uint
		switch sym {
		case 16:
			if i == 0 {
				return 0, ErrCorrupt
			}
			repeat, extra, value = 3, 2, lens[i-1]
		case 17:
			repeat, extra = 3, 3
		default:
			repeat, extra = 11, 7
		}
		more, ok := d.take(extra)
		if !ok {
			return 0, ErrShort
		}
		repeat += int(more)
		if i+repeat > len(lens) {
			return 0, ErrCorrupt
		}
		for ; repeat > 0; repeat-- {
			lens[i] = value
			i++
		}
	}
	var okLit, okDist bool
	d.lit, shortest, okLit = build(d.lit, lens[:nlit], litBits, forLiterals)
	d.dist, _, okDist = build(d.dist, lens[nlit:], distBits, forDistances)
	if !okLit || !okDist {
		return 0, ErrCorrupt
	}
	// Every block ends with the end-of-block code, so no fewer bits than
	// that code has can be the end of a valid stream. compress/flate
	// reads no symbol from fewer, and so a stream that is cut off gives
	// the same output here as there.
	return max(shortest, uint(lens[endOfBlock])), nil
}

// huffman decodes the data of a compressed block with the tables given.
// least is the fewest bits that a literal or length is read from.
func (d *decoder) huffman(lit, dist []uint32, least uint) error {
	src, pos, acc, n := d.src, d.pos, d.acc, d.n
	out, w, base := d.out, d.w, d.base
	var err error
	_ = lit[1<<litBits-1]
	_ = dist[1<<distBits-1]
	// What a Reader's full buffer left of a symbol.
	if d.hasLit {
		if w == len(out) {
			return d.full()
		}
		out[w] = d.litByte
		w++
		d.hasLit = false
	}
	for d.pendLen > 0 {
		if w == len(out) {
			d.w = w
			return d.full()
		}
		from := w - d.pendBack
		fits := min(d.pendLen, len(out)-w)
		for i := 0; i < fits; i++ {
			out[w+i] = out[from+i]
		}
		w += fits
		d.pendLen -= fits
	}
loop:
	for {
		var e uint32
		if len(src)-pos >= fastInput && len(out)-w >= fastRoom {
			// The fast loop: there is input for two loads of eight bytes
			// and room for all that one round can write, so nothing is
			// checked inside it.
			acc |= binary.LittleEndian.Uint64(src[pos:]) << (n & 63)
			pos += int((63 - n) >> 3)
			n |= 56
			e = lit[acc&(1<<litBits-1)]
			if e&kindMask == kindLit {
				acc >>= e & 31
				n -= uint(e & 31)
				out[w] = byte(e >> 16)
				w++
				e = lit[acc&(1<<litBits-1)]
				if e&kindMask == kindLit {
					acc >>= e & 31
					n -= uint(e & 31)
					out[w] = byte(e >> 16)
					w++
					e = lit[acc&(1<<litBits-1)]
					if e&kindMask == kindLit {
						acc >>= e & 31
						n -= uint(e & 31)
						out[w] = byte(e >> 16)
						w++
						continue
					}
				}
				// Not a literal: what follows may take 48 bits.
				acc |= binary.LittleEndian.Uint64(src[pos:]) << (n & 63)
				pos += int((63 - n) >> 3)
				n |= 56
			}
			if e&kindMask == kindLink {
				e = lit[e>>16+uint32(acc>>litBits)&(1<<(e>>8&15)-1)]
			}
			switch e & kindMask {
			case kindLit:
				acc >>= e & 31
				n -= uint(e & 31)
				out[w] = byte(e >> 16)
				w++
				continue
			case kindEnd:
				acc >>= e & 31
				n -= uint(e & 31)
				break loop
			case kindBad:
				err = ErrCorrupt
				break loop
			}
			acc >>= e & 31
			n -= uint(e & 31)
			extra := uint(e >> 8 & 15)
			length := int(e>>16) + int(acc&(1<<extra-1))
			acc >>= extra
			n -= extra

			e = dist[acc&(1<<distBits-1)]
			if e&kindMask == kindLink {
				e = dist[e>>16+uint32(acc>>distBits)&(1<<(e>>8&15)-1)]
			}
			if e&kindMask != kindBase {
				err = ErrCorrupt
				break loop
			}
			acc >>= e & 31
			n -= uint(e & 31)
			extra = uint(e >> 8 & 15)
			back := int(e>>16) + int(acc&(1<<extra-1))
			acc >>= extra
			n -= extra
			if back > w-base {
				err = ErrCorrupt
				break loop
			}
			from := w - back
			switch {
			case length > 32:
				// A long match: in pieces that each end where the one
				// before was written, so that none runs into itself. The
				// pieces double.
				for at, end := w, w+length; at < end; {
					at += copy(out[at:end], out[from:at])
				}
			case back >= 8:
				// Eight bytes at a time. The last step may write past the
				// match, into room that is there.
				for i := 0; i < length; i += 8 {
					binary.LittleEndian.PutUint64(out[w+i:], binary.LittleEndian.Uint64(out[from+i:]))
				}
			case back == 1:
				// One byte repeated.
				v := uint64(out[from]) * 0x0101010101010101
				for i := 0; i < length; i += 8 {
					binary.LittleEndian.PutUint64(out[w+i:], v)
				}
			default:
				// The match runs into itself: byte by byte.
				for i := 0; i < length; i++ {
					out[w+i] = out[from+i]
				}
			}
			w += length
			continue
		}

		// The careful way, one symbol at a time: near the end of the
		// input, and near the end of the room for the output. Room that
		// fits the output exactly is not given up for more.
		for n < 56 && pos < len(src) {
			acc |= uint64(src[pos]) << n
			pos++
			n += 8
		}
		e = lit[acc&(1<<litBits-1)]
		if e&kindMask == kindLink {
			e = lit[e>>16+uint32(acc>>litBits)&(1<<(e>>8&15)-1)]
		}
		if n < least || uint(e&31) > n {
			err = ErrShort
			break loop
		}
		acc >>= e & 31
		n -= uint(e & 31)
		switch e & kindMask {
		case kindLit:
			if w == len(out) {
				if len(out) == d.limit {
					if err = d.full(); err == errFull {
						d.hasLit, d.litByte = true, byte(e>>16)
					}
					break loop
				}
				d.w = w
				d.grow()
				out = d.out
			}
			out[w] = byte(e >> 16)
			w++
			continue
		case kindEnd:
			break loop
		case kindBad:
			err = ErrCorrupt
			break loop
		}
		extra := uint(e >> 8 & 15)
		if extra > n {
			err = ErrShort
			break loop
		}
		length := int(e>>16) + int(acc&(1<<extra-1))
		acc >>= extra
		n -= extra
		for n < 56 && pos < len(src) {
			acc |= uint64(src[pos]) << n
			pos++
			n += 8
		}
		e = dist[acc&(1<<distBits-1)]
		if e&kindMask == kindLink {
			e = dist[e>>16+uint32(acc>>distBits)&(1<<(e>>8&15)-1)]
		}
		if e&kindMask != kindBase {
			err = ErrCorrupt
			break loop
		}
		extra = uint(e >> 8 & 15)
		if uint(e&31)+extra > n {
			err = ErrShort
			break loop
		}
		acc >>= e & 31
		n -= uint(e & 31)
		back := int(e>>16) + int(acc&(1<<extra-1))
		acc >>= extra
		n -= extra
		if back > w-base {
			err = ErrCorrupt
			break loop
		}
		for {
			from := w - back
			fits := min(length, len(out)-w)
			if fits < 16 {
				for i := 0; i < fits; i++ {
					out[w+i] = out[from+i]
				}
			} else {
				for at, end := w, w+fits; at < end; {
					at += copy(out[at:end], out[from:at])
				}
			}
			w += fits
			length -= fits
			if length == 0 {
				break
			}
			if len(out) == d.limit {
				if err = d.full(); err == errFull {
					d.pendLen, d.pendBack = length, back
				}
				break loop
			}
			d.w = w
			d.grow()
			out = d.out
		}
	}
	d.pos, d.acc, d.n, d.w = pos, acc, n, w
	return err
}
