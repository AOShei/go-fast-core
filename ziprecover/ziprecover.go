// Package ziprecover opens a zip archive, and one cut short in transit
// whose directory is lost, the same way for every go-fast library.
//
// A zip archive lists its members in a directory at its end, and a file
// that was cut short (a download stopped, a crawl's size cap) has lost
// it. The members themselves are still there, each behind a header of its
// own, one after another from the start of the file. Open reads those
// headers in order, as far as they go, and writes the directory the file
// should have had; archive/zip then reads the archive as usual, member
// names, checks and CRC-32s included.
//
// Recovery is tried only when the directory cannot be read, so a sound
// archive is never read this way. It follows the headers from the first
// byte and does not search the file for things that look like headers:
// what it finds is what any reader that goes by the headers would find,
// and a header inside a member's data (an archive stored in the archive,
// or compressed bytes that happen to look like one) is never taken for a
// member. It stops at the first offset that does not hold a header.
//
// It was go-fast-ooxml's (internal/opc), which go-fast-odf copied, moved
// here so that every format reads a cut file the same way.
package ziprecover

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core/inflate"
)

var (
	// ErrNotZip: the directory cannot be read, and the file does not
	// begin with a member's header. The error Open returns is
	// archive/zip's, with its text, and matches ErrNotZip with errors.Is.
	ErrNotZip = errors.New("ziprecover: not a zip archive")
	// ErrNoMembers: the file begins as an archive does, but no member
	// could be recovered: it is cut short inside its first header.
	ErrNoMembers = errors.New("ziprecover: the archive has no directory and no member could be recovered")
	// ErrBudget: finding where the members end decompressed more than
	// Limits.MaxDecodedBytes.
	ErrBudget = errors.New("ziprecover: decompressed size limit reached")
	// ErrTooManyMembers: recovery found more members than
	// Limits.MaxMembers.
	ErrTooManyMembers = errors.New("ziprecover: too many members")
)

// Limits bounds what Open does. The zero value bounds nothing but what
// the format does.
type Limits struct {
	// MaxMembers bounds the members recovery finds: more is
	// ErrTooManyMembers, and the headers are not read past it. A
	// recovered archive never has more than 65,535, what a directory of
	// the plain kind can hold. 0: no bound. A sound archive's directory
	// is read by archive/zip whole; its members are for the caller to
	// count, as it counts them today.
	MaxMembers int
	// MaxDecodedBytes bounds what is decompressed to find where the
	// members whose sizes follow their data end, all together: more is
	// ErrBudget. It does not bound reading the members afterwards, which
	// is the caller's. 0: no bound.
	MaxDecodedBytes int64
}

// Recovery says what was found in an archive whose directory could not be
// read.
type Recovery struct {
	// Members is the number of members found by their headers.
	Members int
	// Cut is the name of the last member, if the file ends inside it:
	// its content breaks off, and reading it ends in an error after what
	// is there.
	Cut string
}

// Open reads the zip archive r, of size bytes, as zip.NewReader does,
// with inflate.Decompressor registered for its deflated members. If the
// directory cannot be read and the file begins with a member's header,
// the members are found by their headers and a directory is made for
// them; rec then says what was found. rec is nil for a sound archive.
//
// The errors are ErrNotZip (archive/zip's error, which says why),
// ErrNoMembers, ErrBudget and ErrTooManyMembers.
func Open(r io.ReaderAt, size int64, lim Limits) (zr *zip.Reader, rec *Recovery, err error) {
	zr, err = zip.NewReader(r, size)
	if errors.Is(err, zip.ErrInsecurePath) && zr != nil {
		// Read whole; a name that is not a local path (with GODEBUG
		// zipinsecurepath=0) is the caller's to clean or refuse, as
		// every library cleans names.
		err = nil
	}
	if err == nil && !owns(r, zr) {
		// The directory found is an archive's stored inside this one, cut
		// short after it: archive/zip takes the last directory in the
		// file for the archive's, wherever its members are.
		err = errNotItsOwn
	}
	if err != nil {
		var head [4]byte
		if n, _ := r.ReadAt(head[:], 0); n != len(head) || binary.LittleEndian.Uint32(head[:]) != sigLocal {
			return nil, nil, notZip{err}
		}
		if zr, rec, err = Recover(r, size, lim); err != nil {
			return nil, nil, err
		}
	}
	zr.RegisterDecompressor(zip.Deflate, inflate.Decompressor)
	return zr, rec, nil
}

// notZip is archive/zip's error for a file that is not a zip archive,
// which also matches ErrNotZip.
type notZip struct{ err error }

func (e notZip) Error() string        { return e.err.Error() }
func (e notZip) Unwrap() error        { return e.err }
func (e notZip) Is(target error) bool { return target == ErrNotZip }

var errNotItsOwn = errors.New("the directory found lists none of the archive's members")

// owns reports whether the directory zr read is the file's own. A file
// that begins with a member's header has that member in its directory; a
// directory without it lists the members of something else: an archive
// stored in the file, whose own directory is lost.
func owns(r io.ReaderAt, zr *zip.Reader) bool {
	var hdr [30]byte
	if _, err := r.ReadAt(hdr[:], 0); err != nil || binary.LittleEndian.Uint32(hdr[:]) != sigLocal {
		return true // data before the archive (a self-extracting program), which archive/zip allows
	}
	nameLen := int(binary.LittleEndian.Uint16(hdr[26:]))
	name := make([]byte, nameLen)
	if _, err := r.ReadAt(name, int64(len(hdr))); err != nil {
		return true // the file ends in the header: not a directory's to judge
	}
	first := int64(len(hdr)+nameLen) + int64(binary.LittleEndian.Uint16(hdr[28:]))
	// By name first, which costs nothing, then where its data is.
	for _, f := range zr.File {
		if f.Name != string(name) {
			continue
		}
		if off, err := f.DataOffset(); err == nil && off == first {
			return true
		}
	}
	return false
}

// Message is the text of the input.repaired warning for a recovered
// archive, worded the same in every library: "the archive's directory
// cannot be read: the file is cut short or damaged. N members were found
// by their own headers", then "; the last, NAME, breaks off" or "; any
// that followed are lost". label cleans the member's name for the
// message, as the library cleans names; nil removes control characters
// and bounds it to 256 bytes.
func (r *Recovery) Message(label func(string) string) string {
	if label == nil {
		label = defaultLabel
	}
	s := fmt.Sprintf("the archive's directory cannot be read: the file is cut short or damaged. %d members were found by their own headers", r.Members)
	if r.Cut != "" {
		return s + "; the last, " + label(r.Cut) + ", breaks off"
	}
	return s + "; any that followed are lost"
}

const maxLabel = 256

func defaultLabel(s string) string {
	s = strings.TrimSpace(strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || c == utf8.RuneError {
			return -1
		}
		return c
	}, strings.ToValidUTF8(s, "")))
	if len(s) <= maxLabel {
		return s
	}
	cut := maxLabel
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

const (
	sigLocal      = 0x04034b50
	sigDirectory  = 0x02014b50
	sigEnd        = 0x06054b50
	sigDescriptor = 0x08074b50

	flagDescriptor = 1 << 3 // the sizes follow the data instead of standing in the header
)

// maxHeld bounds a file read into memory to find where its members end
// with inflate; a larger one is read with compress/flate, a byte at a
// time from the file. A variable for the tests.
var maxHeld int64 = 32 << 20

// found is one member found by its header.
type found struct {
	name                    []byte
	flags, method           uint16
	time, date              uint16
	crc, compressed, actual uint32
	header                  uint32 // where its header is
}

// Recover reads the member headers of r, as Open does when its directory
// cannot be read, without trying the directory first and without
// registering a decompressor. It is for a caller that has already tried
// zip.NewReader.
func Recover(r io.ReaderAt, size int64, lim Limits) (*zip.Reader, *Recovery, error) {
	// What a directory of the plain kind can describe.
	if size >= 1<<32-1 {
		return nil, nil, ErrNoMembers
	}
	budget := lim.MaxDecodedBytes
	if budget <= 0 {
		budget = 1<<63 - 1
	}
	var (
		members []found
		rec     Recovery
		hdr     [30]byte
		e       = ender{r: r, size: size}
	)
scan:
	for off := int64(0); off+int64(len(hdr)) <= size; {
		if _, err := r.ReadAt(hdr[:], off); err != nil || binary.LittleEndian.Uint32(hdr[:]) != sigLocal {
			break // the directory begins here, or the file is damaged from here on
		}
		m := found{
			flags:      binary.LittleEndian.Uint16(hdr[6:]),
			method:     binary.LittleEndian.Uint16(hdr[8:]),
			time:       binary.LittleEndian.Uint16(hdr[10:]),
			date:       binary.LittleEndian.Uint16(hdr[12:]),
			crc:        binary.LittleEndian.Uint32(hdr[14:]),
			compressed: binary.LittleEndian.Uint32(hdr[18:]),
			actual:     binary.LittleEndian.Uint32(hdr[22:]),
			header:     uint32(off),
		}
		nameLen, extraLen := int64(binary.LittleEndian.Uint16(hdr[26:])), int64(binary.LittleEndian.Uint16(hdr[28:]))
		data := off + int64(len(hdr)) + nameLen + extraLen
		if nameLen == 0 || data > size {
			break // the file ends inside this header
		}
		m.name = make([]byte, nameLen)
		if _, err := r.ReadAt(m.name, off+int64(len(hdr))); err != nil {
			break
		}
		if lim.MaxMembers > 0 && len(members) >= lim.MaxMembers || len(members) >= 1<<16-1 {
			return nil, nil, ErrTooManyMembers
		}
		whole := true
		next := size
		switch {
		case m.compressed == 1<<32-1 || m.actual == 1<<32-1:
			break scan // sizes of the 64-bit kind: the file would be too large
		case m.flags&flagDescriptor == 0:
			// The header says how long the member is.
			if next = data + int64(m.compressed); next > size {
				whole, m.compressed = false, uint32(size-data)
			}
		case m.method == zip.Store:
			// Stored, with its length after it: it ends at the first
			// descriptor that gives the length it would have there and
			// the CRC-32 of the bytes before it.
			n, sum, after, spent, ok, err := e.storedEnd(data, budget)
			budget -= spent
			if err != nil {
				return nil, nil, err
			}
			if ok {
				m.compressed, m.actual, m.crc = uint32(n), uint32(n), sum
				next = after
			} else {
				// None: the file ends inside it, or its descriptor is
				// damaged. It is all that is there. Its size is given as
				// a byte more, so that reading it reports it cut after
				// its bytes: archive/zip checks no CRC-32 of 0.
				whole = false
				m.compressed, m.actual, m.crc = uint32(size-data), uint32(size-data)+1, 0
			}
		case m.method != zip.Deflate:
			break scan // another method, with its length after it: not something to decompress here
		default:
			// The length follows the data. Where the data ends is found
			// by decompressing it, which also gives the length and the
			// checksum the descriptor would have given.
			used, n, sum, err := e.end(data, budget)
			budget -= n
			if errors.Is(err, ErrBudget) {
				return nil, nil, ErrBudget
			}
			if n >= 1<<32-1 {
				break scan
			}
			whole = err == nil
			m.actual, m.crc = uint32(n), sum
			if whole {
				m.compressed = uint32(used)
				next = descriptorEnd(r, data+used, size)
			} else {
				// The member breaks off, and says so when it is read: its
				// CRC is unknown, and its data is all that is there.
				m.compressed, m.crc = uint32(size-data), 0
			}
		}
		if !whole && m.compressed == 0 {
			break // nothing of it is there
		}
		m.flags &^= flagDescriptor // the directory gives the sizes
		members = append(members, m)
		if !whole {
			rec.Cut = string(m.name)
			break
		}
		off = next
	}
	if len(members) == 0 {
		return nil, nil, ErrNoMembers
	}
	rec.Members = len(members)

	// The directory, and the record that ends an archive.
	var dir []byte
	for i := range members {
		m := &members[i]
		var e [46]byte
		binary.LittleEndian.PutUint32(e[0:], sigDirectory)
		binary.LittleEndian.PutUint16(e[4:], 20)
		binary.LittleEndian.PutUint16(e[6:], 20)
		binary.LittleEndian.PutUint16(e[8:], m.flags)
		binary.LittleEndian.PutUint16(e[10:], m.method)
		binary.LittleEndian.PutUint16(e[12:], m.time)
		binary.LittleEndian.PutUint16(e[14:], m.date)
		binary.LittleEndian.PutUint32(e[16:], m.crc)
		binary.LittleEndian.PutUint32(e[20:], m.compressed)
		binary.LittleEndian.PutUint32(e[24:], m.actual)
		binary.LittleEndian.PutUint16(e[28:], uint16(len(m.name)))
		binary.LittleEndian.PutUint32(e[42:], m.header)
		dir = append(append(dir, e[:]...), m.name...)
	}
	var end [22]byte
	binary.LittleEndian.PutUint32(end[0:], sigEnd)
	binary.LittleEndian.PutUint16(end[8:], uint16(len(members)))
	binary.LittleEndian.PutUint16(end[10:], uint16(len(members)))
	binary.LittleEndian.PutUint32(end[12:], uint32(len(dir)))
	binary.LittleEndian.PutUint32(end[16:], uint32(size))
	tail := append(dir, end[:]...)

	zr, err := zip.NewReader(&withDirectory{r: r, size: size, tail: tail}, size+int64(len(tail)))
	if err != nil {
		return nil, nil, ErrNoMembers
	}
	return zr, &rec, nil
}

// ender finds where a deflated member's data ends. A file of at most
// maxHeld bytes is read into memory once and decompressed with inflate;
// a larger one with compress/flate, which takes no byte past the end of
// the data from a ByteReader, a byte at a time from the file.
type ender struct {
	r    io.ReaderAt
	size int64
	all  []byte // the file, once read
	held bool   // all has been read
	z    *inflate.Reader
	src  byteSource
	buf  [32 << 10]byte
}

// end decompresses the deflated data at data, up to budget bytes, and
// returns the bytes of the file it took (when it ends whole), the bytes
// it decompressed, their CRC-32, and nil, ErrBudget, or the error that
// ended it early.
func (e *ender) end(data, budget int64) (used, n int64, sum uint32, err error) {
	if !e.held && e.size <= maxHeld {
		e.held = true
		e.all = make([]byte, e.size)
		if k, _ := e.r.ReadAt(e.all, 0); int64(k) != e.size {
			e.all = nil // read from the file instead
		}
	}
	var src io.Reader
	if e.all != nil {
		if e.z == nil {
			e.z = inflate.NewRawReader(nil, 0)
		}
		e.z.Reset(e.all[data:], budget)
		src = e.z
	} else {
		e.src.reset(e.r, data, e.size)
		src = flate.NewReader(&e.src)
	}
	for {
		k, rerr := src.Read(e.buf[:])
		if n += int64(k); n > budget {
			return 0, n, 0, ErrBudget
		}
		sum = crc32.Update(sum, crc32.IEEETable, e.buf[:k])
		switch {
		case rerr == io.EOF:
			if e.all != nil {
				return e.z.InputOffset(), n, sum, nil
			}
			return e.src.n, n, sum, nil
		case errors.Is(rerr, inflate.ErrLimit):
			return 0, n, 0, ErrBudget
		case rerr != nil:
			return 0, n, 0, rerr
		}
	}
}

// storedEnd finds where a stored member whose sizes follow its data
// ends: at the first descriptor after data, with its signature or
// without, whose two sizes are the distance from data, which is followed
// by the file's end or a header, and whose CRC-32 is that of the bytes
// before it. It returns the data's length and CRC-32, where the next
// member begins, the bytes it checksummed (charged to budget), and
// whether one was found. The sizes and the CRC-32 together cannot match
// by chance.
func (e *ender) storedEnd(data, budget int64) (n int64, sum uint32, after, spent int64, ok bool, err error) {
	const chunk = 64 << 10
	buf := make([]byte, chunk+16)
	var tail [4]byte
	for base := data; base < e.size || base == data; base += chunk {
		got, _ := e.r.ReadAt(buf[:min(int64(len(buf)), e.size-base)], base)
		b := buf[:got]
		for i := 0; i < chunk && i < len(b); i++ {
			k := base + int64(i) - data
			if k >= 1<<32-1 {
				return 0, 0, 0, spent, false, nil
			}
			var crc uint32
			var end int64
			switch rest := b[i:]; {
			case len(rest) >= 16 && binary.LittleEndian.Uint32(rest) == sigDescriptor &&
				int64(binary.LittleEndian.Uint32(rest[8:])) == k && int64(binary.LittleEndian.Uint32(rest[12:])) == k:
				crc, end = binary.LittleEndian.Uint32(rest[4:]), base+int64(i)+16
			case len(rest) >= 12 && int64(binary.LittleEndian.Uint32(rest[4:])) == k && int64(binary.LittleEndian.Uint32(rest[8:])) == k:
				crc, end = binary.LittleEndian.Uint32(rest), base+int64(i)+12
			default:
				continue
			}
			if end != e.size {
				// A header follows, or as much of one as the file holds.
				k := min(e.size-end, 4)
				if _, err := e.r.ReadAt(tail[:k], end); err != nil {
					continue
				}
				if !headerStart(tail[:k]) {
					continue
				}
			}
			if spent += k; spent > budget {
				return 0, 0, 0, spent, false, ErrBudget
			}
			h := crc32.NewIEEE()
			if _, err := io.Copy(h, io.NewSectionReader(e.r, data, k)); err != nil {
				continue
			}
			if h.Sum32() == crc {
				return k, crc, end, spent, true, nil
			}
		}
		if base+chunk >= e.size {
			break
		}
	}
	return 0, 0, 0, spent, false, nil
}

// headerStart reports whether b is the signature of a member's header, a
// directory entry or the end record, or as much of one as b holds.
func headerStart(b []byte) bool {
	for _, sig := range []uint32{sigLocal, sigDirectory, sigEnd} {
		var want [4]byte
		binary.LittleEndian.PutUint32(want[:], sig)
		if bytes.HasPrefix(want[:], b) {
			return true
		}
	}
	return false
}

// descriptorEnd returns where the next member begins after data that
// ended at off and is followed by its descriptor: twelve bytes, or twenty
// where the sizes are of the 64-bit kind, with or without a signature in
// front. The form is told by what follows it.
func descriptorEnd(r io.ReaderAt, off, size int64) int64 {
	var sig [4]byte
	for _, n := range []int64{16, 12, 24, 20} {
		at := off + n
		if at == size {
			return at
		}
		if at+4 > size {
			continue
		}
		if _, err := r.ReadAt(sig[:], at); err != nil {
			continue
		}
		switch binary.LittleEndian.Uint32(sig[:]) {
		case sigLocal, sigDirectory, sigEnd:
			return at
		}
	}
	return size // the file ends in or right after the descriptor
}

// byteSource reads the bytes of an archive from one place on, one at a
// time, and counts them: the decompressor then takes not a byte more
// than the data it decompresses, and the count is where that data ends.
type byteSource struct {
	r        io.ReaderAt
	off, end int64
	buf      [16 << 10]byte
	lo, hi   int
	n        int64
}

func (b *byteSource) reset(r io.ReaderAt, off, end int64) {
	b.r, b.off, b.end, b.lo, b.hi, b.n = r, off, end, 0, 0, 0
}

func (b *byteSource) ReadByte() (byte, error) {
	if b.lo == b.hi {
		want := min(int64(len(b.buf)), b.end-b.off)
		if want <= 0 {
			return 0, io.EOF
		}
		got, err := b.r.ReadAt(b.buf[:want], b.off)
		if got == 0 {
			if err == nil {
				err = io.ErrNoProgress
			}
			return 0, err
		}
		b.off += int64(got)
		b.lo, b.hi = 0, got
	}
	c := b.buf[b.lo]
	b.lo++
	b.n++
	return c, nil
}

func (b *byteSource) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c, err := b.ReadByte()
	if err != nil {
		return 0, err
	}
	p[0] = c
	return 1, nil
}

// withDirectory is an archive with the directory it should have had put
// after its last byte.
type withDirectory struct {
	r    io.ReaderAt
	size int64
	tail []byte
}

func (w *withDirectory) ReadAt(p []byte, off int64) (n int, err error) {
	if off < 0 {
		return 0, errors.New("ziprecover: negative offset")
	}
	if off < w.size {
		want := int(min(int64(len(p)), w.size-off))
		n, err = w.r.ReadAt(p[:want], off)
		if n < want {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return n, err
		}
		off += int64(n)
	}
	if n < len(p) {
		at := off - w.size
		if at >= int64(len(w.tail)) {
			return n, io.EOF
		}
		n += copy(p[n:], w.tail[at:])
		if n < len(p) {
			return n, io.EOF
		}
	}
	return n, nil
}
