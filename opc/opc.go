// Package opc reads the container of the formats built on the Open
// Packaging Conventions (ECMA-376 Part 2): a zip archive holding parts, a
// table of their content types, and relationship parts that link them.
// Word, Excel, PowerPoint and Visio files are such packages, and so are
// XPS and OpenXPS documents.
//
// It is the one place the libraries touch such an archive, so it is where
// the limits on decompression are enforced. What a package is (which part
// is the document, and of what kind) is for the reader of each format to
// decide: this package reads the container.
//
// Moved from go-fast-ooxml's internal/opc, without what is Office's own
// (the main part, Flat OPC), and with interleaved parts, which XPS uses.
package opc

import (
	"archive/zip"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-core/ziprecover"
)

// Errors returned by this package.
var (
	// ErrNotZip: the input is not a zip archive.
	ErrNotZip = errors.New("opc: not a zip archive")
	// ErrCutOff: the input begins as a zip archive does, the directory at
	// its end cannot be read (the file was cut short, or damaged there),
	// and what the members' own headers lead to is not enough to read a
	// document from.
	ErrCutOff = errors.New("opc: zip archive whose directory cannot be read (the file is cut short or damaged), with too little left to read a document from")
	// ErrDamaged: a package whose own structure cannot be read.
	ErrDamaged = errors.New("opc: damaged package")
	// ErrNotFound: the named part is not in the package.
	ErrNotFound = errors.New("opc: no such part")
	// ErrBudget: reading went over Limits.MaxDecodedBytes.
	ErrBudget = errors.New("opc: decompressed size limit reached")
	// ErrTooManyParts: the archive has more members than Limits.MaxMembers.
	ErrTooManyParts = errors.New("opc: too many parts")
	// ErrEncryptedMember: a zip member is encrypted with zip encryption.
	ErrEncryptedMember = errors.New("opc: zip member is encrypted")
)

// The namespaces of the package's own parts.
const (
	ContentTypesNS  = "http://schemas.openxmlformats.org/package/2006/content-types"
	RelationshipsNS = "http://schemas.openxmlformats.org/package/2006/relationships"
)

// Limits bounds what a package may cost. Values are final: the caller has
// already applied its defaults.
type Limits struct {
	// MaxDecodedBytes is the total number of decompressed bytes that may
	// be read from the package, over all parts.
	MaxDecodedBytes int64
	// MaxMembers is the largest number of zip members accepted.
	MaxMembers int
}

// Rel is one relationship of a part or of the package.
type Rel struct {
	ID   string
	Type string
	// Target is a part name in canonical form, or, when External is set,
	// the URL or path as written, made fit to appear in the output
	// (clean.Address). It is cleaned here, once: every link to it then
	// holds the same string.
	Target   string
	External bool
}

// Part is a part kept outside a zip archive, for FromParts: a part of a
// Flat OPC document, for instance.
type Part struct {
	// Name is the part's name, in any form Canonical accepts.
	Name string
	// ContentType is the part's content type, "" if it has none.
	ContentType string
	// Size is the size of the part's content, as far as is known.
	Size int64
	// Open returns a reader for the part's content.
	Open func() io.ReadCloser
	// Bindings are namespace declarations in scope around the part, for a
	// part that is the XML of an element of a larger document.
	Bindings []xmlscan.Binding
}

// member is a part: one zip member, the pieces of an interleaved part in
// order, or a Part.
type member struct {
	files []*zip.File
	ext   *Part
}

func (m *member) size() int64 {
	if m.ext != nil {
		return m.ext.Size
	}
	n := int64(0)
	for _, f := range m.files {
		n += int64(f.UncompressedSize64)
	}
	return n
}

// Package is an opened package. It is safe for concurrent use.
type Package struct {
	parts     map[string]*member // by canonical part name
	written   map[string]string  // canonical name -> the name as the archive writes it, where they differ
	names     []string           // canonical names, in archive order
	defaults  map[string]string  // lower-case extension -> content type
	overrides map[string]string  // canonical part name -> content type
	typed     bool               // the package has a table of content types
	budget    atomic.Int64       // decompressed bytes still allowed
	ambiguous int                // members that different readers would read differently
	broken    int                // interleaved parts whose pieces do not make a whole
	recovered *ziprecover.Recovery
	trailing  []byte // what follows the end of the archive, up to MaxTrailing
	afterEnd  int64  // how many bytes follow it in all

	cutMu    sync.Mutex
	cut      map[string]int64 // by relationships part: external targets cut to their bound
	cutNames map[string]int64 // names of parts and their types put in the output, cut: the bytes left out
}

// Open reads the archive's directory and, if there is one, the table of
// content types. A zip archive without the table is opened too (Typed
// reports it): some programs leave it out, and a damaged archive may have
// lost it, and what the archive is then is for the caller to decide.
func Open(r io.ReaderAt, size int64, lim Limits) (*Package, error) {
	// A file whose directory cannot be read but which begins with a
	// member's header is recovered by go-fast-core's ziprecover, which
	// finds the members by their own headers. It registers go-fast-core's
	// decompressor, which takes about half the time of compress/flate and
	// gives the same bytes; archive/zip still checks each part's CRC-32.
	zr, recovered, err := ziprecover.Open(r, size, ziprecover.Limits{MaxMembers: lim.MaxMembers, MaxDecodedBytes: lim.MaxDecodedBytes})
	switch {
	case errors.Is(err, ziprecover.ErrNotZip):
		return nil, fmt.Errorf("%w: %v", ErrNotZip, err)
	case errors.Is(err, ziprecover.ErrBudget):
		return nil, ErrBudget
	case errors.Is(err, ziprecover.ErrTooManyMembers):
		return nil, ErrTooManyParts
	case err != nil:
		return nil, ErrCutOff
	}
	if lim.MaxMembers > 0 && len(zr.File) > lim.MaxMembers {
		return nil, fmt.Errorf("%w: %d, limit %d", ErrTooManyParts, len(zr.File), lim.MaxMembers)
	}
	p := newPackage(len(zr.File), lim)
	p.recovered = recovered
	if recovered == nil {
		p.trailing, p.afterEnd = trailing(r, size)
	}
	var pieces map[string][]piece // by the canonical name of the part they make
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue // a directory entry
		}
		name := Canonical(f.Name)
		if other := unicodePath(f); other != "" && Canonical(other) != name {
			p.ambiguous++ // a second name for this member; it is read under its first
		}
		if _, dup := p.parts[name]; dup {
			p.ambiguous++
			continue // the first of two members with one name is the part
		}
		if part, pc, ok := pieceOf(name); ok {
			if pieces == nil {
				pieces = map[string][]piece{}
			}
			pc.f = f
			pieces[part] = append(pieces[part], pc)
		}
		p.parts[name] = &member{files: []*zip.File{f}}
		p.names = append(p.names, name)
		p.keepWritten(name, f.Name)
	}
	p.interleave(pieces)
	if _, ok := p.parts[contentTypesName]; !ok {
		return p, nil
	}
	p.typed = true
	if err := p.readContentTypes(); err != nil {
		if recovered != nil && !errors.Is(err, ErrBudget) {
			return p, nil // the table breaks off: what it said before that is used
		}
		return nil, err
	}
	return p, nil
}

func newPackage(n int, lim Limits) *Package {
	p := &Package{
		parts:     make(map[string]*member, n),
		names:     make([]string, 0, n),
		defaults:  map[string]string{},
		overrides: map[string]string{},
	}
	p.budget.Store(lim.MaxDecodedBytes)
	return p
}

// FromParts makes a package of parts kept outside a zip archive. Of two
// parts with one name the first is the part, and the second is counted
// in Ambiguous. Written gives a part's Name as given, with forward
// slashes and no leading slash.
func FromParts(parts []Part, lim Limits) *Package {
	p := newPackage(len(parts), lim)
	p.typed = true
	for i := range parts {
		name := Canonical(parts[i].Name)
		if _, dup := p.parts[name]; dup {
			p.ambiguous++
			continue
		}
		p.parts[name] = &member{ext: &parts[i]}
		p.names = append(p.names, name)
		p.keepWritten(name, parts[i].Name)
		if ct := parts[i].ContentType; ct != "" {
			p.overrides[name] = ct
		}
	}
	return p
}

// keepWritten keeps the name a part is written with, for Written, where it
// differs from its canonical name: with forward slashes and no leading
// slash, but its own capitals and escapes.
func (p *Package) keepWritten(name, raw string) {
	if shown := strings.TrimLeft(strings.ReplaceAll(raw, "\\", "/"), "/"); shown != name {
		if p.written == nil {
			p.written = map[string]string{}
		}
		p.written[name] = shown
	}
}

// piece is one piece of an interleaved part.
type piece struct {
	index int
	last  bool
	f     *zip.File
}

// pieceOf reports whether the canonical name is a piece of an interleaved
// part (ECMA-376 Part 2, 10.2.4): "part/[n].piece", the last one
// "part/[n].last.piece".
func pieceOf(name string) (part string, pc piece, ok bool) {
	dir, base := path.Split(name)
	if dir == "" || !strings.HasPrefix(base, "[") || !strings.HasSuffix(base, ".piece") {
		return "", pc, false
	}
	rest := strings.TrimSuffix(base[1:], ".piece")
	if r, last := strings.CutSuffix(rest, ".last"); last {
		rest, pc.last = r, true
	}
	digits, ok := strings.CutSuffix(rest, "]")
	if !ok || digits == "" || len(digits) > 9 || digits[0] == '+' {
		return "", pc, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 || strconv.Itoa(n) != digits {
		return "", pc, false
	}
	pc.index = n
	return strings.TrimSuffix(dir, "/"), pc, true
}

// interleave makes a part of the pieces of each interleaved part that are
// whole: numbered from 0 with no gap, the last marked last and no other.
// The part takes the place of its first piece in archive order; its
// pieces are not parts of their own. Pieces that do not make a whole are
// left as they are, and counted in BrokenPieces.
func (p *Package) interleave(pieces map[string][]piece) {
	if len(pieces) == 0 {
		return
	}
	made := map[string]bool{}
	for part, pcs := range pieces {
		slices.SortFunc(pcs, func(a, b piece) int { return a.index - b.index })
		whole := true
		for i, pc := range pcs {
			if pc.index != i || pc.last != (i == len(pcs)-1) {
				whole = false
				break
			}
		}
		if _, taken := p.parts[part]; taken || !whole {
			p.broken++
			continue
		}
		m := &member{}
		for _, pc := range pcs {
			m.files = append(m.files, pc.f)
			delete(p.parts, Canonical(pc.f.Name))
		}
		p.parts[part] = m
		made[part] = true
	}
	if len(made) == 0 {
		return
	}
	names := p.names[:0]
	for _, name := range p.names {
		if _, ok := p.parts[name]; ok {
			names = append(names, name)
			continue
		}
		if part, _, ok := pieceOf(name); ok && made[part] {
			names = append(names, part)
			delete(made, part) // in the place of its first piece only
		}
	}
	p.names = names
}

// BrokenPieces returns how many interleaved parts have pieces that do not
// make a whole (one missing, or no last one): their pieces are left as
// parts of their own.
func (p *Package) BrokenPieces() int { return p.broken }

// Typed reports whether the package has a table of content types.
func (p *Package) Typed() bool { return p.typed }

// Mimetype returns what a member named "mimetype" says, trimmed, if it has
// one of at most 256 bytes: how OpenDocument and EPUB, also zip archives,
// say what they are.
func (p *Package) Mimetype() string {
	m, ok := p.parts["mimetype"]
	if !ok || m.ext != nil || len(m.files) != 1 || m.files[0].UncompressedSize64 > 256 {
		return ""
	}
	rc, err := m.files[0].Open()
	if err != nil {
		return ""
	}
	defer rc.Close()
	said, _ := io.ReadAll(io.LimitReader(rc, 256))
	return strings.TrimSpace(string(said))
}

// Label is clean.Label for a name of a part, or its content type, that is
// put in the output (a picture's identifier, an embedded file's name): a
// name cut to its bound is counted, once however often it is used.
func (p *Package) Label(s string) string {
	v, cut := clean.LabelCut(s)
	if cut {
		p.cutMu.Lock()
		if p.cutNames == nil {
			p.cutNames = map[string]int64{}
		}
		p.cutNames[s] = int64(max(len(strings.TrimSpace(s))-clean.MaxLabel, 1))
		p.cutMu.Unlock()
	}
	return v
}

// CutNames returns how many names Label cut, and the bytes it left out.
func (p *Package) CutNames() (n, bytes int64) {
	p.cutMu.Lock()
	defer p.cutMu.Unlock()
	for _, b := range p.cutNames {
		n, bytes = n+1, bytes+b
	}
	return n, bytes
}

// cutTargets records that the relationships part name has n external
// targets longer than an address is kept. A part read again says the
// same, and is counted once.
func (p *Package) cutTargets(name string, n int64) {
	if n == 0 {
		return
	}
	p.cutMu.Lock()
	defer p.cutMu.Unlock()
	if p.cut == nil {
		p.cut = map[string]int64{}
	}
	p.cut[name] = max(p.cut[name], n)
}

// CutTargets returns how many external targets of the relationships read
// so far were cut to the length an address is kept to.
func (p *Package) CutTargets() int64 {
	p.cutMu.Lock()
	defer p.cutMu.Unlock()
	n := int64(0)
	for _, k := range p.cut {
		n += k
	}
	return n
}

// Trailing returns the bytes that follow the end of the archive (its
// end-of-directory record and the comment it states), as many as
// MaxTrailing of them, and how many follow it in all; nil and 0 for none.
// Office stops reading there; the bytes are data appended to the file.
func (p *Package) Trailing() (data []byte, total int64) { return p.trailing, p.afterEnd }

// MaxTrailing bounds what is read of the data after the archive's end.
const MaxTrailing = 1 << 20

// trailing finds the archive's end-of-directory record as archive/zip
// does, the last one in the final 64 KiB whose comment fits in the file,
// and returns what follows the record and its comment.
func trailing(r io.ReaderAt, size int64) ([]byte, int64) {
	const record = 22 // the record without its comment
	window := min(size, record+0xFFFF+1024)
	if window < record {
		return nil, 0
	}
	buf := make([]byte, window)
	if n, err := r.ReadAt(buf, size-window); int64(n) != window && err != nil {
		return nil, 0
	}
	for i := len(buf) - record; i >= 0; i-- {
		if buf[i] != 'P' || buf[i+1] != 'K' || buf[i+2] != 5 || buf[i+3] != 6 {
			continue
		}
		comment := int(buf[i+20]) | int(buf[i+21])<<8
		end := i + record + comment
		if end > len(buf) {
			return nil, 0 // a comment longer than the file: archive/zip refuses it too
		}
		total := int64(len(buf) - end)
		if total == 0 {
			return nil, 0
		}
		return append([]byte(nil), buf[end:end+int(min(total, MaxTrailing))]...), total
	}
	return nil, 0
}

// Recovered returns what was found by the members' own headers, if the
// archive had no directory that could be read; nil for a sound archive.
func (p *Package) Recovered() *ziprecover.Recovery { return p.recovered }

const contentTypesName = "[content_types].xml"

// Canonical returns the form of a part name used as a key: no leading
// slash, forward slashes, percent-escapes decoded, ASCII letters in lower
// case. Part names are case-insensitive, and producers disagree on the
// rest.
func Canonical(name string) string {
	plain := true
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '\\' || c == '%' || (c >= 'A' && c <= 'Z') || (i == 0 && c == '/') {
			plain = false
			break
		}
	}
	if plain {
		return name
	}
	b := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '\\':
			c = '/'
		case c == '%' && i+2 < len(name) && isHex(name[i+1]) && isHex(name[i+2]):
			c = unhex(name[i+1])<<4 | unhex(name[i+2])
			i += 2
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b = append(b, c)
	}
	for len(b) > 0 && b[0] == '/' {
		b = b[1:]
	}
	return string(b)
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// Resolve returns the canonical name of the part that target refers to
// when written in a relationship of the part named source ("" for the
// package). A target that starts with a slash is relative to the package
// root; any other is relative to the directory of source.
func Resolve(source, target string) string {
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target = target[:i] // a fragment addresses a place inside the part
	}
	if strings.HasPrefix(target, "/") || strings.HasPrefix(target, "\\") {
		return Canonical(path.Clean(strings.ReplaceAll(target, "\\", "/")))
	}
	dir := path.Dir(source)
	if source == "" || dir == "." {
		dir = ""
	}
	joined := path.Clean(path.Join(dir, strings.ReplaceAll(target, "\\", "/")))
	// A target that climbs out of the package stays at its root.
	for strings.HasPrefix(joined, "../") {
		joined = joined[3:]
	}
	if joined == ".." || joined == "." {
		joined = ""
	}
	return Canonical(joined)
}

// Ambiguous returns the number of archive members that programs disagree
// about: a member whose name is given twice, differently (once in its
// header and once in a Unicode-path field), and a member with the same name
// as an earlier one. Such an archive shows one document to one reader and
// another to the next. This package reads each member under its header
// name and takes the first of two with one name.
func (p *Package) Ambiguous() int { return p.ambiguous }

// unicodePath returns the name a member's Info-ZIP Unicode Path extra
// field gives it, or "" if it has no valid one.
func unicodePath(f *zip.File) string {
	extra := f.Extra
	for len(extra) >= 4 {
		id := uint16(extra[0]) | uint16(extra[1])<<8
		size := int(extra[2]) | int(extra[3])<<8
		if size > len(extra)-4 {
			return ""
		}
		field := extra[4 : 4+size]
		extra = extra[4+size:]
		if id != 0x7075 || len(field) < 5 || field[0] != 1 {
			continue
		}
		// The field is only valid for the header name it was written for.
		sum := uint32(field[1]) | uint32(field[2])<<8 | uint32(field[3])<<16 | uint32(field[4])<<24
		if crc32.ChecksumIEEE([]byte(f.Name)) != sum {
			return ""
		}
		return string(field[5:])
	}
	return ""
}

// Has reports whether the package has the named part.
func (p *Package) Has(name string) bool {
	_, ok := p.parts[Canonical(name)]
	return ok
}

// Written returns a part's name as the archive writes it (or as Part.Name
// gives it, for a package made by FromParts), with its own capitals. Part names are matched without regard to case, but a name
// shown to a person, or used as an identifier in the output, should be the
// file's own. An interleaved part has the name of the folder its pieces
// are in.
func (p *Package) Written(name string) string {
	name = Canonical(name)
	if shown, ok := p.written[name]; ok {
		return shown
	}
	if m, ok := p.parts[name]; ok && len(m.files) > 1 {
		if dir, _ := path.Split(strings.TrimLeft(strings.ReplaceAll(m.files[0].Name, "\\", "/"), "/")); dir != "" {
			return strings.TrimSuffix(dir, "/")
		}
	}
	return name
}

// Names returns the canonical names of all parts, in archive order. The
// slice must not be modified.
func (p *Package) Names() []string { return p.names }

// Size returns the uncompressed size the archive declares for a part, or
// -1 if there is no such part. A hostile archive can declare anything.
func (p *Package) Size(name string) int64 {
	m, ok := p.parts[Canonical(name)]
	if !ok {
		return -1
	}
	return m.size()
}

// ContentType returns the content type of a part: its override if it has
// one, else the default for its extension, else "".
func (p *Package) ContentType(name string) string {
	name = Canonical(name)
	if ct, ok := p.overrides[name]; ok {
		return ct
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 && !strings.Contains(name[i:], "/") {
		return p.defaults[name[i+1:]]
	}
	return ""
}

// Override returns the content type the table gives the part by name, if
// it gives one, not by its extension.
func (p *Package) Override(name string) (string, bool) {
	ct, ok := p.overrides[Canonical(name)]
	return ct, ok
}

// HasContentType reports whether any part is declared with the given
// content type.
func (p *Package) HasContentType(ct string) bool {
	for _, v := range p.overrides {
		if v == ct {
			return true
		}
	}
	for ext, v := range p.defaults {
		if v != ct {
			continue
		}
		for _, n := range p.names {
			if strings.HasSuffix(n, "."+ext) {
				return true
			}
		}
	}
	return false
}

// Remaining returns how many decompressed bytes may still be read.
func (p *Package) Remaining() int64 { return max(p.budget.Load(), 0) }

// Budget returns the decompression budget as it stands, and Restore puts
// it back to a value Budget returned. They are for a reader that has to
// read a stretch of the package again and must find the limit where it
// found it the first time.
func (p *Package) Budget() int64 { return p.budget.Load() }

// Restore sets the decompression budget. See Budget.
func (p *Package) Restore(budget int64) { p.budget.Store(budget) }

// Charge takes n bytes from the decompression budget, for what a reader
// decompresses itself from a part it has read (the code of a VBA project).
// It reports false if the budget has run out.
func (p *Package) Charge(n int64) bool { return p.budget.Add(-n) >= 0 }

// Open returns a reader for a part's decompressed content. Every byte read
// is charged to the package's budget, and a read that goes over it fails
// with ErrBudget, so no part can expand without bound however small it is
// in the archive.
func (p *Package) Open(name string) (io.ReadCloser, error) {
	rc, err := p.openRaw(name)
	if err != nil {
		return nil, err
	}
	return &budgetReader{rc: rc, p: p}, nil
}

// openRaw opens a part, decompressing it, without charging the budget.
func (p *Package) openRaw(name string) (io.ReadCloser, error) {
	m, ok := p.parts[Canonical(name)]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if m.ext != nil {
		return m.ext.Open(), nil
	}
	for _, f := range m.files {
		if f.Flags&1 != 0 {
			return nil, fmt.Errorf("%w: %s", ErrEncryptedMember, name)
		}
	}
	if len(m.files) == 1 {
		rc, err := m.files[0].Open()
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrDamaged, name, err)
		}
		return rc, nil
	}
	return &piecesReader{name: name, files: m.files}, nil
}

// piecesReader reads the pieces of an interleaved part one after another,
// each opened when the one before it ends.
type piecesReader struct {
	name  string
	files []*zip.File
	cur   io.ReadCloser
}

func (r *piecesReader) Read(b []byte) (int, error) {
	for {
		if r.cur == nil {
			if len(r.files) == 0 {
				return 0, io.EOF
			}
			rc, err := r.files[0].Open()
			if err != nil {
				return 0, fmt.Errorf("%w: %s: %v", ErrDamaged, r.name, err)
			}
			r.cur, r.files = rc, r.files[1:]
		}
		n, err := r.cur.Read(b)
		if err == io.EOF {
			r.cur.Close()
			r.cur = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (r *piecesReader) Close() error {
	if r.cur != nil {
		return r.cur.Close()
	}
	return nil
}

type budgetReader struct {
	rc io.ReadCloser
	p  *Package
}

// Read hands on what the budget has room for and no more: where a part
// is cut by the budget does not depend on how much each read asks for.
func (b *budgetReader) Read(buf []byte) (int, error) {
	n, err := b.rc.Read(buf)
	if n > 0 {
		if left := b.p.budget.Add(-int64(n)); left < 0 {
			return max(n+int(max(left, -int64(n))), 0), ErrBudget
		}
	}
	return n, err
}

func (b *budgetReader) Close() error { return b.rc.Close() }

// Scan opens a part and returns a scanner over it. The caller closes the
// returned closer when done.
func (p *Package) Scan(name string, s *xmlscan.Scanner, opts *xmlscan.Options) (io.Closer, error) {
	rc, err := p.Open(name)
	if err != nil {
		return nil, err
	}
	s.Reset(rc, p.Window(name, opts))
	return released{rc, s}, nil
}

// released closes a part's reader and hands its scanner's window back to
// be reused: the caller closes it when it has read the part.
type released struct {
	io.Closer
	s *xmlscan.Scanner
}

func (r released) Close() error {
	r.s.Release()
	return r.Closer.Close()
}

// Window returns opts with a window for a part read whole: one that holds
// the size the archive declares for it, in steps of 4 KiB, up to the
// scanner's default. A part of a kilobyte needs no 64 KiB window, and
// most parts of a document are a few kilobytes. Text longer than the
// window is handed over in pieces, so a part larger than it declares is
// read as it is with any window. It is Options as well.
func (p *Package) Window(name string, opts *xmlscan.Options) *xmlscan.Options {
	opts = p.Options(name, opts)
	size := p.Size(name)
	if size < 0 || size >= xmlscan.DefaultBufferSize || (opts != nil && opts.BufferSize > 0) {
		return opts
	}
	var o xmlscan.Options
	if opts != nil {
		o = *opts
	}
	o.BufferSize = int(size+1+4095) &^ 4095
	return &o
}

// Options returns the options to scan a part with: opts, with the
// namespaces declared around the part when it is a Part that has them.
func (p *Package) Options(name string, opts *xmlscan.Options) *xmlscan.Options {
	m, ok := p.parts[Canonical(name)]
	if !ok || m.ext == nil || len(m.ext.Bindings) == 0 {
		return opts
	}
	var o xmlscan.Options
	if opts != nil {
		o = *opts
	}
	o.Bindings = slices.Concat(m.ext.Bindings, o.Bindings)
	return &o
}

const (
	spaceTypes = xmlscan.FirstSpace + iota
	spaceRels
)

var scanOptions = &xmlscan.Options{Spaces: map[string]xmlscan.Space{
	ContentTypesNS:  spaceTypes,
	RelationshipsNS: spaceRels,
}}

func (p *Package) readContentTypes() error {
	var s xmlscan.Scanner
	c, err := p.Scan(contentTypesName, &s, scanOptions)
	if err != nil {
		return err
	}
	defer c.Close()
	for {
		switch s.Next() {
		case xmlscan.Start:
			switch {
			case s.Is(spaceTypes, "Default"):
				ext, _ := s.Attr(xmlscan.NoSpace, "Extension")
				ct, _ := s.Attr(xmlscan.NoSpace, "ContentType")
				if len(ext) > 0 {
					p.defaults[strings.ToLower(string(ext))] = string(ct)
				}
			case s.Is(spaceTypes, "Override"):
				part, _ := s.Attr(xmlscan.NoSpace, "PartName")
				ct, _ := s.Attr(xmlscan.NoSpace, "ContentType")
				if len(part) > 0 {
					p.overrides[Canonical(string(part))] = string(ct)
				}
			}
		case xmlscan.EOF:
			if err := s.Err(); err != nil {
				if errors.Is(err, ErrBudget) {
					return err
				}
				return fmt.Errorf("%w: content types: %v", ErrDamaged, err)
			}
			return nil
		}
	}
}

// RelsName returns the name of the relationships part of source ("" for
// the package).
func RelsName(source string) string {
	if source == "" {
		return "_rels/.rels"
	}
	source = Canonical(source)
	return path.Join(path.Dir(source), "_rels", path.Base(source)+".rels")
}

// Rels returns the relationships of the part named source, or of the
// package when source is "". A part without a relationships part has none,
// which is not an error.
func (p *Package) Rels(source string) ([]Rel, error) {
	var s xmlscan.Scanner
	return p.RelsWith(source, &s)
}

// RelsWith is Rels using the caller's scanner, which a caller that reads
// the relationships of many parts keeps, with its buffers.
func (p *Package) RelsWith(source string, s *xmlscan.Scanner) ([]Rel, error) {
	name := RelsName(source)
	if !p.Has(name) {
		return nil, nil
	}
	c, err := p.Scan(name, s, scanOptions)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	var rels []Rel
	cut := int64(0) // external targets cut to their bound
	defer func() { p.cutTargets(name, cut) }()
	for {
		switch s.Next() {
		case xmlscan.Start:
			if !s.Is(spaceRels, "Relationship") {
				continue
			}
			id, _ := s.Attr(xmlscan.NoSpace, "Id")
			typ, _ := s.Attr(xmlscan.NoSpace, "Type")
			target, _ := s.Attr(xmlscan.NoSpace, "Target")
			mode, _ := s.Attr(xmlscan.NoSpace, "TargetMode")
			r := Rel{ID: string(id), Type: string(typ), External: string(mode) == "External"}
			if r.External {
				var wasCut bool
				r.Target, wasCut = clean.AddressCut(string(target))
				if wasCut {
					cut++
				}
			} else {
				r.Target = Resolve(source, string(target))
			}
			rels = append(rels, r)
		case xmlscan.EOF:
			if err := s.Err(); err != nil {
				if errors.Is(err, ErrBudget) {
					return rels, err
				}
				return rels, fmt.Errorf("%w: %s: %v", ErrDamaged, name, err)
			}
			return rels, nil
		}
	}
}
