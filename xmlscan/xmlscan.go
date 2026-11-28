// The XML scanner of the go-fast libraries, merged from three copies:
// go-fast-ooxml's internal/xmlscan (the original), go-fast-odf's copy of
// it, and go-fast-xml's, which extended it for XML in general (MIT
// License, Copyright (c) 2025 Andrew O'Shei; see LICENSE).

// Package xmlscan is a streaming pull tokenizer for XML.
//
//   - It does not allocate per token. Names, attribute values and text are
//     slices of the scanner's window, valid until the next call to Next.
//   - It reads through a window that is refilled as it goes, so input of
//     any size is read in bounded memory. Text, CDATA and comments are
//     delivered in chunks when they do not fit; a tag, a processing
//     instruction and the document type declaration must fit, up to
//     Options.MaxTokenBytes. Windows are pooled: Release hands one back.
//   - It reads UTF-8, UTF-16 and what an XML declaration names, decoded by
//     go-fast-core/charset (encoding.go).
//   - It resolves namespace prefixes, to the URI and to small numbers the
//     caller chooses.
//   - It never reads anything but its input. General entity references
//     other than the five predefined ones are given to Options.Resolve,
//     and a replacement read as markup is read from memory, within a bound
//     on nesting and on the bytes all expansions produce together.
//   - By default it reads well-formed XML and stops with ErrSyntax at the
//     first error. With Options.Recover it reads on through damaged markup
//     the way a lenient reader does and counts each repair: an end tag
//     that matches an element further out closes what is inside it, one
//     that matches nothing is dropped, elements left open at the end are
//     closed, a "<" or "&" that starts no markup is text, an attribute
//     without a value or quotes is read as far as it goes, and elements
//     nested deeper than Options.MaxDepth are read as part of their
//     ancestor (their tags passed over, their text kept).
//   - Comments, processing instructions and the document type declaration
//     are tokens with Options.Markup; otherwise they are passed over, and
//     a document type declaration is accepted only if it declares nothing
//     (no internal subset, before the root, only one).
package xmlscan

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core/charset"
)

// Kind is the type of a token.
type Kind uint8

const (
	// EOF means the input ended or an error occurred. Err tells which.
	EOF Kind = iota
	// Start is a start tag. An empty-element tag (<a/>) gives Start and
	// then End.
	Start
	// End is an end tag.
	End
	// Text is character data or a CDATA section. One run of text in the
	// source may arrive as several Text tokens.
	Text
	// Comment is the content of a comment. A long comment arrives as
	// several Comment tokens; Final reports the last one.
	Comment
	// PI is a processing instruction: Target and Text.
	PI
	// Doctype is a document type declaration. Text is what lies between
	// "<!DOCTYPE" and the final ">".
	Doctype
)

// Space identifies an XML namespace. Callers number the namespaces they
// care about from FirstSpace and pass the numbering in Options.Spaces.
type Space uint16

const (
	// NoSpace is the absence of a namespace: an attribute without a
	// prefix, or an element where no default namespace is in force.
	NoSpace Space = iota
	// SpaceXML is the namespace of the reserved "xml" prefix (xml:space).
	SpaceXML
	// SpaceUnknown is any namespace not listed in Options.Spaces, and any
	// prefix that was never declared.
	SpaceUnknown
	// FirstSpace is the first number available to callers.
	FirstSpace
)

// The namespaces bound to the reserved prefixes.
const (
	XMLNamespace   = "http://www.w3.org/XML/1998/namespace"
	XMLNSNamespace = "http://www.w3.org/2000/xmlns/"
)

// Errors reported by Err. Syntax errors wrap ErrSyntax and carry the byte
// offset.
var (
	ErrSyntax = errors.New("xmlscan: malformed XML")
	// ErrDTD: without Options.Markup, a document type declaration with an
	// internal subset, after the root element has begun, or a second one.
	ErrDTD = errors.New("xmlscan: a document type declaration with an internal subset, or out of place, is not accepted")
	// ErrDepth: without Options.Recover, elements nested deeper than
	// Options.MaxDepth.
	ErrDepth = errors.New("xmlscan: elements nested too deeply")
	// ErrTokenTooLarge: a tag, a processing instruction or the document
	// type declaration is larger than Options.MaxTokenBytes.
	ErrTokenTooLarge = errors.New("xmlscan: a tag is larger than the token limit")
	// ErrEncoding: an XML declaration names an encoding that is not
	// decoded.
	ErrEncoding = errors.New("xmlscan: unsupported character encoding")
)

// ErrReferenceTooLarge is the ErrTokenTooLarge of a character reference
// longer than the token limit (leading zeros); errors.Is matches both.
var ErrReferenceTooLarge = fmt.Errorf("%w: a character reference", ErrTokenTooLarge)

// Resolution says how a general entity reference is read.
type Resolution uint8

const (
	// Keep keeps the reference in the text as written.
	Keep Resolution = iota
	// Insert puts the replacement in the text as it is, as characters.
	Insert
	// Expand reads the replacement in place of the reference, as markup.
	Expand
)

// Resolver decides how a general entity reference other than the five
// predefined ones is read, and gives its replacement.
type Resolver func(name []byte) (repl []byte, how Resolution)

// Options configures a Scanner. The zero value is usable.
type Options struct {
	// Spaces maps namespace URIs to the caller's numbers. It is only read.
	Spaces map[string]Space
	// MaxDepth bounds element nesting. Deeper is ErrDepth, or, with
	// Recover, an element read as part of its ancestor and counted in
	// Stats.Flattened. Zero means DefaultMaxDepth.
	MaxDepth int
	// MaxTokenBytes bounds the size of one tag, processing instruction or
	// document type declaration. Zero means DefaultMaxTokenBytes.
	MaxTokenBytes int
	// MaxAttrs bounds the attributes kept from one tag; the rest are
	// counted in Stats.AttrsCut. Zero means DefaultMaxAttrs.
	MaxAttrs int
	// BufferSize is the initial window size when reading from an
	// io.Reader. Zero means DefaultBufferSize.
	BufferSize int
	// Resolve decides general entity references other than the
	// predefined ones. Nil keeps them as written, counted as repairs.
	Resolve Resolver
	// MaxEntityDepth bounds how deeply replacements read as markup nest.
	// Zero means DefaultMaxEntityDepth.
	MaxEntityDepth int
	// ExpansionLimit bounds the bytes of replacement text read in place
	// of references, all expansions together. Zero means no expansion at
	// all; negative means no limit.
	ExpansionLimit int64
	// BaseOffset is the offset of the first byte read, added to every
	// offset the scanner reports.
	BaseOffset int64
	// MaxValueBytes bounds one decoded attribute value; a longer one is
	// cut and counted in Stats.ValuesCut. Zero means no bound but
	// MaxTokenBytes (DefaultMaxValueBytes is a bound for XML from
	// anywhere).
	MaxValueBytes int
	// Interrupt, if set, is called every 1,024 constructs Next passes
	// over without making a token; an error it returns ends the scan.
	Interrupt func() error
	// Recover makes the scanner read on through damaged markup (see the
	// package documentation), counting each repair in Stats.Repairs.
	// Without it the first error ends the scan with ErrSyntax.
	Recover bool
	// Markup makes comments, processing instructions and the document
	// type declaration tokens (Comment, PI, Doctype). Without it they are
	// passed over, and a document type declaration is accepted only if it
	// declares nothing.
	Markup bool
	// Decoded says the input is UTF-8 already, whatever its XML
	// declaration says: it was decoded on the way in.
	Decoded bool
	// Spans makes Span report where each token is in the input. Without
	// it, Span reports it only for Comment, PI and Doctype tokens and
	// markup kept as text.
	Spans bool
	// Bindings are namespace declarations in scope before the input's
	// first element, as if an element around it had made them. They are
	// for XML read out of a larger document, such as a part of a Flat OPC
	// package, which may use a prefix that an ancestor in that document
	// declares.
	Bindings []Binding
}

// Binding declares a namespace: Prefix is "" for the default namespace.
type Binding struct{ Prefix, URI string }

// Defaults for Options.
const (
	DefaultMaxDepth       = 512
	DefaultMaxTokenBytes  = 16 << 20
	DefaultBufferSize     = 64 << 10
	DefaultMaxAttrs       = 1024
	DefaultMaxEntityDepth = 16
	// MaxEntityName is the longest entity name looked up. A longer one is
	// not read as a reference.
	MaxEntityName = 64
	// DefaultMaxValueBytes is a bound on one decoded attribute value for
	// XML from anywhere (Options.MaxValueBytes).
	DefaultMaxValueBytes = 1 << 20
	// MaxNameKept bounds the bytes of an open element's name that are
	// kept, for its End token and for matching its end tag (two names
	// longer than this match when their kept starts and lengths do).
	MaxNameKept = 1024
	// MaxBindingBytes bounds the prefixes and URIs of the namespace
	// declarations in scope.
	MaxBindingBytes = 16 << 20
)

// Stats counts what the scanner did besides reading well-formed input.
type Stats struct {
	// Repairs counts the places where damaged markup was read leniently,
	// and references kept as written (an unknown entity, a character
	// reference without its semicolon).
	Repairs int
	// Flattened counts elements nested deeper than Options.MaxDepth.
	Flattened int
	// AttrsCut counts attributes past Options.MaxAttrs.
	AttrsCut int
	// EntitiesCut counts references kept as written because expanding
	// them would pass Options.MaxEntityDepth or Options.ExpansionLimit.
	EntitiesCut int
	// EntitiesRecursive counts references to an entity inside its own
	// replacement, kept as written.
	EntitiesRecursive int
	// Expanded is the bytes of replacement text read in place of
	// references.
	Expanded int64
	// ValuesCut counts attribute values cut to Options.MaxValueBytes, or
	// to what is left of MaxTokenBytes for all the values of one tag, as
	// they are read.
	ValuesCut int
	// NamesCut counts element names longer than MaxNameKept, of which
	// only the start is kept.
	NamesCut int
	// BindingsCut counts namespace declarations not read because those
	// in scope already hold MaxBindingBytes.
	BindingsCut int
	// CharRefsReplaced counts character references to no character XML
	// allows anywhere (&#0;, a surrogate, a number past U+10FFFF), each
	// read as U+FFFD. Each is counted in Repairs too.
	CharRefsReplaced int
	// InvalidUTF8 counts the bytes of text, attribute values and names
	// read that are not part of a UTF-8 character, each read as U+FFFD
	// (input that is UTF-8 is handed on as it is, and need not be valid).
	// A name is counted once: an element's at its Start token, not again
	// at its End.
	InvalidUTF8 int
}

// attr is an attribute of the current start tag, by its offsets in
// Scanner.tag: no pointers, so the list costs the collector nothing.
type attr struct {
	name, nameEnd   int32
	colon           int32 // offset of ':' in the name, or -1
	value, valueEnd int32
}

// readValue is a value that was decoded or cut the first time it was
// read, kept so that reading it again neither expands its entities again
// nor counts its cut again: attrs[i]'s value is tag or attrBuf[at:at+n].
type readValue struct {
	i, at, n int32
	raw      bool // in tag, from the value's start
}

// maxTag is the longest start tag whose attributes have offsets.
const maxTag = math.MaxInt32

const (
	bindNone       = -1 // no namespace
	bindXML        = -2 // the reserved xml prefix
	bindUndeclared = -3 // a prefix never declared
	bindEnded      = -4 // the End token of an element that declared its own namespace
)

// The int32 offsets cannot overflow: names keeps at most MaxNameKept
// bytes per open element and bindNames at most MaxBindingBytes.
type elem struct {
	nameStart int32 // offset of the qualified name in Scanner.names
	nameLen   int32 // the bytes kept, at most MaxNameKept
	fullLen   int32 // the length of the name as written
	colon     int32 // index of ':' in the name, or -1
	bind      int32 // the binding of the element's namespace, or a bind* value
	space     Space // its number
	bindMark  int32 // len(binds) before this element's declarations
	bindBytes int32 // len(bindNames) before this element's declarations
}

type binding struct {
	start, end int32 // prefix in Scanner.bindNames; empty for the default namespace
	uend       int32 // the URI follows the prefix, to uend
	space      Space
	slot       int32 // index in Scanner.heads of this prefix
	prev       int32 // the binding this one shadows, or -1
}

// maxSlotsKept is how many prefixes keep their slot when out of scope.
const maxSlotsKept = 256

// Above this many namespace declarations in scope, prefixes are looked up
// in a hash table.
const linearBindings = 8

type mode uint8

const (
	modeNormal mode = iota
	modeComment
	modeCDATA
)

// frame is the reading state saved while the replacement of an entity
// reference is read.
type frame struct {
	r        io.Reader
	buf      []byte
	pos, end int
	base     int64
	eof, own bool
	name     []byte
}

// Scanner reads XML tokens. Create one with New or NewBytes, or reuse one
// with Reset or ResetBytes, which keeps its buffers.
type Scanner struct {
	r    io.Reader
	buf  []byte // the window
	pos  int    // start of the unconsumed input in buf
	end  int    // end of valid data in buf
	base int64  // offset in the input of buf[0]
	eof  bool   // the reader is exhausted
	own  bool   // buf belongs to the scanner and may be written
	err  error
	late error // an error the reader gave with its last bytes: met once they are scanned
	mode mode

	recover    bool
	tokens     bool // Options.Markup
	spans      bool // Options.Spans
	decoded    bool
	baseOffset int64
	converted  bool // the input is converted to UTF-8 on the way in
	started    bool // the root element has begun
	doctyped   bool // a document type declaration has been passed over
	poisoned   int  // with the xmlscanpoison tag: the window is overwritten up to here

	spaces    map[string]Space
	maxDepth  int
	maxToken  int
	maxAttrs  int
	resolve   Resolver
	maxEntity int
	expLeft   int64 // expansion bytes left; negative once a reference was refused
	closing   int   // elements still to be closed by a recovered end tag
	over      int   // open elements beyond maxDepth
	interrupt func() error
	skips     int // constructs passed over without a token
	maxValue  int
	valueEnd  int  // attrBuf may not grow past this while a value is decoded
	valueFull bool // the value being decoded reached valueEnd

	frames           []frame
	refStart, refEnd int64 // the outermost reference being expanded
	constructStart   int64 // where the current comment or CDATA section began

	// The current token.
	kind       Kind
	qname      []byte
	local      []byte
	bind       int32
	space      Space // the current token's namespace number
	text       []byte
	target     []byte
	final      bool
	tokStart   int64
	tokEnd     int64
	attrs      []attr
	read       []readValue // values of attrs decoded or cut
	tag        []byte      // the current start tag, which attrs index
	pendingEnd bool        // an empty-element tag still owes its End

	stack     []elem
	names     []byte // qualified names of the open elements
	binds     []binding
	bindNames []byte
	slots     map[string]int32 // prefix -> index in heads
	heads     []int32          // per prefix: its innermost binding, or -1
	freeSlots []int32          // slots of heads no prefix in scope uses
	// The last prefix resolved and its binding, while the bindings in
	// scope are those of generation gen: a document's elements and
	// attributes mostly share one prefix, which then costs no lookup.
	lastPrefix []byte
	lastBind   int32
	lastGen    uint64
	gen        uint64   // changes whenever the bindings in scope change
	scratch    []byte   // decoded text of the current token
	attrBuf    []byte   // decoded attribute values of the current token
	fixed      []byte   // the text of the current token, made valid UTF-8 (validText)
	ascii      bool     // the text of the token being read is known to be ASCII
	fixValue   []byte   // an attribute value made valid UTF-8, before it is copied to attrBuf
	namesHigh  bool     // a name in the current start tag has a byte beyond ASCII
	mendedName bool     // an element name of the document was not valid UTF-8 (endTag mends end tags to match)
	attrFix    [][]byte // the attribute names made valid UTF-8, by index; nil for one that is
	attrNested [][]byte // entities being expanded in an attribute value
	endURI     []byte
	endSpace   Space

	// Stats counts repairs, cuts and expansion.
	Stats Stats
}

// New returns a Scanner reading from r.
func New(r io.Reader, opts *Options) *Scanner {
	s := &Scanner{}
	s.Reset(r, opts)
	return s
}

// NewBytes returns a Scanner reading from data, which it does not modify
// or copy.
func NewBytes(data []byte, opts *Options) *Scanner {
	s := &Scanner{}
	s.ResetBytes(data, opts)
	return s
}

// windows keeps the windows of scanners released, by size: 4 KiB, 8, 16,
// 32 and 64 KiB, for the next stream read, of this document or another.
var windows [5]sync.Pool

// windowClass is the class of windows that holds size bytes, or -1.
func windowClass(size int) int {
	for c := range windows {
		if size <= 4096<<c {
			return c
		}
	}
	return -1
}

func getWindow(size int) []byte {
	c := windowClass(size)
	if c < 0 {
		return make([]byte, size)
	}
	if p, _ := windows[c].Get().(*[]byte); p != nil {
		return *p
	}
	return make([]byte, 4096<<c)
}

// Release hands the scanner's window back to be reused, and leaves the
// scanner at the end of its stream until it is Reset. What it read is a
// view of the window, so nothing of it may be held after: that holds
// already, since the window slides over the stream as it is read.
func (s *Scanner) Release() {
	if s.own {
		if poison {
			s.poisonReleased()
		}
		if c := windowClass(len(s.buf)); c >= 0 && len(s.buf) == 4096<<c && cap(s.buf) == len(s.buf) {
			b := s.buf
			windows[c].Put(&b)
		}
	}
	for i := range s.frames {
		// The windows of the input under entity replacements.
		if f := &s.frames[i]; f.own {
			if c := windowClass(len(f.buf)); c >= 0 && len(f.buf) == 4096<<c && cap(f.buf) == len(f.buf) {
				b := f.buf
				windows[c].Put(&b)
			}
		}
	}
	s.frames = s.frames[:0]
	s.buf, s.own, s.r = nil, false, nil
	s.pos, s.end, s.eof = 0, 0, true
	s.qname, s.local, s.text, s.target = nil, nil, nil, nil
	s.attrs, s.tag = s.attrs[:0], nil
}

// Reset makes the scanner read from r, keeping its buffers.
func (s *Scanner) Reset(r io.Reader, opts *Options) {
	size := DefaultBufferSize
	if opts != nil && opts.BufferSize > 0 {
		size = opts.BufferSize
	}
	if !s.own || len(s.buf) < size || len(s.frames) > 0 {
		s.Release()
		s.buf = getWindow(size)
	}
	s.reset(opts)
	if len(s.buf) > s.maxToken {
		// A tag must not fit where the limit says it does not.
		s.buf = s.buf[:s.maxToken:s.maxToken]
	}
	s.r, s.own = r, true
	if !s.decoded {
		s.sniff()
	}
}

// ResetBytes makes the scanner read from data, keeping its buffers.
func (s *Scanner) ResetBytes(data []byte, opts *Options) {
	if len(s.frames) > 0 {
		s.Release()
	}
	s.reset(opts)
	s.r, s.own = nil, false
	s.buf, s.end, s.eof = data, len(data), true
	if !s.decoded {
		s.sniff()
	}
}

func (s *Scanner) reset(opts *Options) {
	s.pos, s.end, s.eof, s.err, s.late, s.mode = 0, 0, false, nil, nil, modeNormal
	s.kind, s.pendingEnd, s.converted, s.started, s.doctyped = EOF, false, false, false, false
	s.Stats = Stats{}
	// No view of the stream read before is left for the first tokens
	// to show.
	s.qname, s.local, s.text, s.target, s.final = nil, nil, nil, nil, false
	s.bind, s.space, s.tokStart, s.tokEnd = bindNone, NoSpace, 0, 0
	s.stack, s.names, s.binds, s.bindNames = s.stack[:0], s.names[:0], s.binds[:0], s.bindNames[:0]
	s.mendedName = false
	s.gen++
	if len(s.slots) > maxSlotsKept {
		s.slots, s.heads, s.freeSlots = nil, s.heads[:0], s.freeSlots[:0] // do not carry a hostile file's prefixes around
	}
	for i := range s.heads {
		s.heads[i] = -1
	}
	s.attrs, s.scratch, s.attrBuf, s.tag, s.read = s.attrs[:0], s.scratch[:0], s.attrBuf[:0], nil, s.read[:0]
	s.frames, s.attrNested = s.frames[:0], s.attrNested[:0]
	s.closing, s.over, s.skips = 0, 0, 0

	s.maxDepth, s.maxToken, s.maxAttrs, s.maxEntity = DefaultMaxDepth, DefaultMaxTokenBytes, DefaultMaxAttrs, DefaultMaxEntityDepth
	s.maxValue = 0
	s.spaces, s.resolve, s.baseOffset, s.interrupt = nil, nil, 0, nil
	s.recover, s.tokens, s.decoded, s.spans, s.expLeft = false, false, false, false, 0
	if opts != nil {
		s.spaces, s.resolve, s.baseOffset, s.interrupt = opts.Spaces, opts.Resolve, opts.BaseOffset, opts.Interrupt
		s.recover, s.tokens, s.decoded, s.spans = opts.Recover, opts.Markup, opts.Decoded, opts.Spans
		if opts.MaxDepth > 0 {
			s.maxDepth = opts.MaxDepth
		}
		if opts.MaxTokenBytes > 0 {
			s.maxToken = opts.MaxTokenBytes
		}
		if opts.MaxAttrs > 0 {
			s.maxAttrs = opts.MaxAttrs
		}
		if opts.MaxEntityDepth > 0 {
			s.maxEntity = opts.MaxEntityDepth
		}
		s.maxValue = opts.MaxValueBytes
		s.expLeft = opts.ExpansionLimit
		if s.expLeft < 0 {
			s.expLeft = math.MaxInt64
		}
		// Declarations made before the first element belong to no
		// element: none closes them.
		for _, b := range opts.Bindings {
			s.declare([]byte(b.Prefix), []byte(b.URI))
		}
	}
	if s.maxValue <= 0 {
		s.maxValue = s.maxToken
	}
	s.base = s.baseOffset
}

// SetResolver replaces Options.Resolve, as a reader does once it has read
// the document type declaration.
func (s *Scanner) SetResolver(r Resolver) { s.resolve = r }

// Err returns the error that ended the scan, or nil at a clean end of
// input.
func (s *Scanner) Err() error { return s.err }

// Offset returns the byte offset in the input of the next unread byte. While
// the replacement of an entity reference is read, it is the offset of the
// reference.
func (s *Scanner) Offset() int64 { return s.here() }

func (s *Scanner) here() int64 {
	if len(s.frames) > 0 {
		return s.refStart
	}
	return s.base + int64(s.pos)
}

// at is here for a position in the window read before.
func (s *Scanner) at(pos int) int64 {
	if len(s.frames) > 0 {
		return s.refStart
	}
	return s.base + int64(pos)
}

func (s *Scanner) hereEnd() int64 {
	if len(s.frames) > 0 {
		return s.refEnd
	}
	return s.base + int64(s.pos)
}

// Span returns the byte offsets in the input where the current token
// starts and ends, with Options.Spans. A token read from the replacement
// of an entity reference has the offsets of the outermost reference.
func (s *Scanner) Span() (start, end int64) { return s.tokStart, s.tokEnd }

// InEntity reports whether the current token comes from the replacement
// of an entity reference.
func (s *Scanner) InEntity() bool { return len(s.frames) > 0 }

// Depth returns the number of open elements. After a Start token it
// includes that element; after an End token it does not.
func (s *Scanner) Depth() int { return len(s.stack) }

// Kind returns the kind of the current token, as last returned by Next.
func (s *Scanner) Kind() Kind { return s.kind }

// Local returns the local name of the current Start or End token.
func (s *Scanner) Local() []byte { return s.local }

// Name returns the qualified name of the current Start or End token as it
// is written in the source, prefix included. It is valid UTF-8: a byte
// that is not part of a character is U+FFFD, counted in Stats.InvalidUTF8
// at the Start token. An end tag is matched to its start tag by the name
// so mended, so two names that differ only in such bytes match; a prefix
// is bound by its bytes as written.
func (s *Scanner) Name() []byte { return s.qname }

// Space returns the namespace of the current Start or End token.
func (s *Scanner) Space() Space { return s.space }

// URI returns the namespace URI of the current Start or End token, empty
// for none or an undeclared prefix.
func (s *Scanner) URI() []byte { return s.uriOf(s.bind) }

func (s *Scanner) spaceOf(b int32) Space {
	switch b {
	case bindNone:
		return NoSpace
	case bindXML:
		return SpaceXML
	case bindUndeclared:
		return SpaceUnknown
	case bindEnded:
		return s.endSpace
	}
	return s.binds[b].space
}

var xmlNamespace = []byte(XMLNamespace)

func (s *Scanner) uriOf(b int32) []byte {
	switch b {
	case bindNone, bindUndeclared:
		return nil
	case bindXML:
		return xmlNamespace
	case bindEnded:
		return s.endURI
	}
	bd := &s.binds[b]
	return s.bindNames[bd.end:bd.uend]
}

// Text returns the content of the current Text, Comment, PI or Doctype
// token. Text has references decoded; all have line ends normalized to
// "\n", except Doctype, which is as written. It is valid UTF-8: a byte of
// the input that is not part of a character is U+FFFD, counted in
// Stats.InvalidUTF8.
func (s *Scanner) Text() []byte { return s.text }

// Target returns the target of the current PI token, valid UTF-8 as Name
// is.
func (s *Scanner) Target() []byte { return s.target }

// Final reports whether the current Comment token is the last piece of
// its comment.
func (s *Scanner) Final() bool { return s.final }

func (s *Scanner) fail(err error) {
	if s.err == nil {
		s.err = err
	}
	s.kind = EOF
}

// syntax ends the scan with ErrSyntax.
func (s *Scanner) syntax(what string) {
	if s.late != nil {
		// The input stopped because the reader failed, not because the
		// XML is wrong: the reader's error is the one to give.
		s.fail(s.late)
		return
	}
	s.fail(fmt.Errorf("%w at byte %d: %s", ErrSyntax, s.here(), what))
}

// repair counts one repair, with Options.Recover; without it, it ends
// the scan with ErrSyntax and reports false.
func (s *Scanner) repair(what string) bool {
	if !s.recover {
		s.syntax(what)
		return false
	}
	s.Stats.Repairs++
	return true
}

// Abort ends the scan: from now on Next returns EOF and Err returns err,
// unless the scan had failed already.
func (s *Scanner) Abort(err error) { s.fail(err) }

// more reads more input into the window, keeping the unconsumed part,
// which moves to the front. It reports whether any bytes were added.
func (s *Scanner) more() bool {
	if s.eof && s.late != nil && s.err == nil && len(s.frames) == 0 {
		// The bytes that came with the error have been scanned.
		s.fail(s.late)
	}
	if s.eof || s.err != nil {
		return false
	}
	if s.pos > 0 {
		n := copy(s.buf, s.buf[s.pos:s.end])
		s.base += int64(s.pos)
		s.pos, s.end = 0, n
	}
	if s.end == len(s.buf) {
		if len(s.buf) >= s.maxToken {
			err := ErrTokenTooLarge
			if s.mode == modeNormal && s.end > 0 && s.buf[0] != '<' {
				// Text that cannot be cut: a character reference
				// with more digits than the window holds.
				err = ErrReferenceTooLarge
			}
			s.fail(fmt.Errorf("%w (%d bytes) at byte %d", err, s.maxToken, s.here()))
			return false
		}
		grown := make([]byte, min(2*len(s.buf), s.maxToken))
		copy(grown, s.buf[:s.end])
		s.buf = grown
	}
	// Fill the window: a construct that did not fit is parsed again from
	// its start, so each call must at least double what it can see.
	added := false
	for idle := 0; s.end < len(s.buf); {
		n, err := s.r.Read(s.buf[s.end:])
		s.end += n
		if n > 0 {
			added, idle = true, 0
		}
		if err != nil {
			s.eof = true
			if err != io.EOF {
				// The bytes that came with the error are scanned first,
				// so that where the input stops does not depend on how
				// the reader divides it.
				if added {
					s.late = err
					return true
				}
				s.fail(err)
				return false
			}
			return added
		}
		if n == 0 {
			if idle++; idle == 64 {
				if !added {
					s.fail(io.ErrNoProgress)
				}
				return added
			}
		}
	}
	return added
}

// Next advances to the next token and returns its kind.
func (s *Scanner) Next() Kind {
	if s.pendingEnd {
		s.pendingEnd = false
		s.pop()
		s.kind = End
		s.tokStart = s.tokEnd
		return End
	}
	if s.err != nil {
		return EOF
	}
	if s.closing > 0 {
		s.closing--
		s.pop()
		s.kind = End
		s.tokStart = s.tokEnd
		return End
	}
	if poison {
		s.poisonViews()
	}
	s.attrs, s.attrBuf, s.tag, s.read = s.attrs[:0], s.attrBuf[:0], nil, s.read[:0]
	for {
		if s.pos >= s.end && !s.more() {
			if s.err != nil {
				return EOF
			}
			if s.mode != modeNormal {
				if s.unterminatedMode() {
					return s.kind
				}
				continue
			}
			if len(s.frames) > 0 {
				s.popFrame()
				continue
			}
			if len(s.stack) > 0 {
				// Close what the input left open.
				if !s.repair("unexpected end of input") {
					return EOF
				}
				s.closing = len(s.stack) - 1
				s.over = 0
				s.pop()
				s.kind = End
				s.tokStart, s.tokEnd = s.here(), s.here()
				return End
			}
			s.kind = EOF
			return EOF
		}
		var status scanStatus
		s.ascii = false // set by decodeText for the token it reads
		switch {
		case s.mode == modeComment:
			status = s.comment()
		case s.mode == modeCDATA:
			status = s.cdata()
		case s.buf[s.pos] != '<':
			status = s.charData()
		default:
			status = s.markup()
		}
		switch status {
		case emitted:
			if s.kind >= Text && !s.ascii {
				s.text = s.validText(s.text)
			}
			return s.kind
		case failed:
			return EOF
		case needMore:
			atEnd := s.eof
			if !s.more() {
				if s.err != nil {
					return EOF
				}
				if atEnd {
					// Markup that runs to the end of the input (or of a
					// replacement) is not markup: it is kept as text.
					if !s.repair("unexpected end of input") {
						return EOF
					}
					s.text = s.validText(s.decodeLiteral(s.buf[s.pos:s.end]))
					s.tokStart = s.here()
					s.pos = s.end
					s.tokEnd = s.hereEnd()
					s.kind = Text
					return Text
				}
			}
		}
		// skipped: go round again, asking now and then whether to stop.
		if s.skips++; s.skips&1023 == 0 && s.interrupt != nil {
			if err := s.interrupt(); err != nil {
				s.fail(err)
				return EOF
			}
		}
	}
}

type scanStatus uint8

const (
	emitted  scanStatus = iota // a token is ready
	skipped                    // input was consumed without producing a token
	needMore                   // the window ends inside the construct
	failed                     // the scan has failed
)

// unterminatedMode ends a comment or CDATA section that runs to the end
// of the input, and reports whether that made a token.
func (s *Scanner) unterminatedMode() bool {
	what := "comment is not terminated"
	if s.mode == modeCDATA {
		what = "CDATA section is not terminated"
	}
	if !s.repair(what) {
		return false
	}
	m := s.mode
	s.mode = modeNormal
	if m == modeComment && s.tokens {
		s.text, s.final, s.kind = nil, true, Comment
		s.tokStart, s.tokEnd = s.constructStart, s.hereEnd()
		return true
	}
	return false
}

func (s *Scanner) popFrame() {
	f := s.frames[len(s.frames)-1]
	s.frames = s.frames[:len(s.frames)-1]
	s.r, s.buf, s.pos, s.end, s.base, s.eof, s.own = f.r, f.buf, f.pos, f.end, f.base, f.eof, f.own
}

// charData scans text up to the next '<'.
func (s *Scanner) charData() scanStatus {
	b := s.buf[s.pos:s.end]
	if s.resolve == nil && !s.spans && len(b) <= MaxChunk {
		// The usual case, as below with what does nothing here left
		// out: no references to resolve, no span, and no more than one
		// piece of text in view.
		n := bytes.IndexByte(b, '<')
		if n < 0 {
			switch {
			case s.eof:
				n = len(b)
			case s.pos > 0 || s.end < len(s.buf):
				return needMore
			default:
				if n = cutText(b); n == 0 {
					return needMore
				}
			}
		}
		text, used := s.decodeText(b[:n])
		s.pos += used
		if len(text) == 0 {
			return skipped
		}
		s.text = text
		s.kind = Text
		return emitted
	}
	// A complete reference at the start is looked at before the text is
	// measured: measuring looks for white space to cut at, and a long run
	// of references would pay for that once per reference.
	if s.resolve != nil && len(b) > 0 && b[0] == '&' {
		if _, m, ok := refName(b); ok {
			if st, done := s.entityRef(b[:m], s.here()); done {
				return st
			}
		}
	}
	// Look no further than one token can take: a long text from memory
	// would otherwise be searched to its end once for each piece.
	look := b
	if len(look) > MaxChunk+1 {
		look = look[:MaxChunk+1]
	}
	n := bytes.IndexByte(look, '<')
	if n < 0 && len(look) < len(b) {
		n = len(look) // more than a piece of text: cut below
	}
	if n < 0 {
		switch {
		case s.eof:
			n = len(b)
		case s.pos > 0 || s.end < len(s.buf):
			return needMore // there is room to look further before splitting
		default:
			// The window is full of text. Hand over what can be cut
			// cleanly and carry on; text need not fit in the window.
			if n = cutText(b); n == 0 {
				return needMore
			}
		}
	}
	if n > MaxChunk {
		if m := cutText(b[:n]); m > 0 {
			n = m
		}
	}
	raw := b[:n]
	from := s.pos
	if s.resolve != nil && raw[0] == '&' {
		if st, done := s.entityRef(raw, s.here()); done {
			return st
		}
	}
	text, used := s.decodeText(raw)
	s.pos += used
	if s.spans {
		s.tokStart, s.tokEnd = s.at(from), s.hereEnd()
	}
	if len(text) == 0 {
		return skipped
	}
	s.text = text
	s.kind = Text
	return emitted
}

// refName returns the name of the named reference at the start of raw
// (raw[0] is '&') and the length of the reference, or false.
func refName(raw []byte) ([]byte, int, bool) {
	if len(raw) < 3 || raw[1] == '#' {
		return nil, 0, false
	}
	for i := 1; i < len(raw) && i <= MaxEntityName+1; i++ {
		switch raw[i] {
		case ';':
			if i < 2 {
				return nil, 0, false
			}
			return raw[1:i], i + 1, true
		case '&', '<', '>', '"', '\'', ' ', '\t', '\n', '\r', '#':
			return nil, 0, false
		}
	}
	return nil, 0, false
}

func predefined(name []byte) byte {
	switch string(name) {
	case "lt":
		return '<'
	case "gt":
		return '>'
	case "amp":
		return '&'
	case "quot":
		return '"'
	case "apos":
		return '\''
	}
	return 0
}

// entityRef handles a general entity reference at the start of raw other
// than a predefined one. A replacement to insert, or the reference kept as
// written, is a Text token. A replacement read as markup is started, or,
// when a bound or recursion refuses it, the reference as written is a Text
// token. It reports false when raw does not start with such a reference.
func (s *Scanner) entityRef(raw []byte, start int64) (scanStatus, bool) {
	name, n, ok := refName(raw)
	if !ok || predefined(name) != 0 {
		return 0, false
	}
	repl, how := s.resolve(name)
	if how != Expand {
		s.text = raw[:n]
		if how == Insert {
			s.text = repl
		}
		s.pos += n
		s.tokStart, s.tokEnd = start, s.hereEnd()
		if len(s.text) == 0 {
			return skipped, true
		}
		s.kind = Text
		return emitted, true
	}
	refused := false
	for i := range s.frames {
		if bytes.Equal(s.frames[i].name, name) {
			s.Stats.EntitiesRecursive++
			refused = true
			break
		}
	}
	if !refused && (len(s.frames) >= s.maxEntity || s.expLeft < int64(len(repl))) {
		s.Stats.EntitiesCut++
		s.expLeft = -1 // a bound reached cuts there: no expansion after it
		refused = true
	}
	if refused {
		s.text = raw[:n]
		s.pos += n
		s.tokStart, s.tokEnd = start, s.hereEnd()
		s.kind = Text
		return emitted, true
	}
	s.expLeft -= int64(len(repl))
	s.Stats.Expanded += int64(len(repl))
	if len(s.frames) == 0 {
		s.refStart, s.refEnd = start, start+int64(n)
	}
	s.pos += n
	s.frames = append(s.frames, frame{r: s.r, buf: s.buf, pos: s.pos, end: s.end, base: s.base, eof: s.eof, own: s.own, name: name})
	s.r, s.buf, s.pos, s.end, s.base, s.eof, s.own = nil, repl, 0, len(repl), 0, true, false
	return skipped, true
}

// cutText returns how much of the text b to hand over as one token: at
// most MaxChunk bytes, cut after white space where there is some, else
// as much as safeCut allows.
func cutText(b []byte) int {
	n := min(len(b), MaxChunk)
	if m := safeCut(b[:afterSpace(b, n)]); m > 0 {
		return m
	}
	return safeCut(b)
}

// safeCut returns how much of b can be delivered as a text chunk without
// splitting a UTF-8 sequence, an entity reference or a CR LF pair.
func safeCut(b []byte) int {
	n := safeCutRef(b)
	if n > 0 && b[n-1] == '\r' {
		n-- // what was cut back may have left a CR at the end
	}
	return n
}

func safeCutRef(b []byte) int {
	n := len(b)
	if n > 0 && b[n-1] == '\r' {
		n--
	}
	// An incomplete multi-byte sequence at the end.
	for back := 1; back <= 3 && back <= n; back++ {
		c := b[n-back]
		if c < 0x80 {
			break
		}
		if c >= 0xC0 { // a lead byte: is its sequence complete?
			need := 2
			if c >= 0xF0 {
				need = 4
			} else if c >= 0xE0 {
				need = 3
			}
			if back < need {
				n -= back
			}
			break
		}
	}
	// An entity reference that has started but not ended.
	from := max(0, n-MaxEntityName-2)
	if i := bytes.LastIndexByte(b[from:n], '&'); i >= 0 {
		if bytes.IndexByte(b[from+i:n], ';') < 0 {
			n = from + i
		}
		return n
	}
	// And further back, a character written by its number with many
	// zeros in front: "&#", then digits to the end.
	if i := bytes.LastIndexByte(b[:n], '&'); i >= 0 && i+1 < n && b[i+1] == '#' {
		for _, c := range b[i+2 : n] {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == 'x' || c == 'X') {
				return n
			}
		}
		n = i
	}
	return n
}

// MaxChunk bounds the text of one Text or Comment token read from the
// input: longer character data, CDATA sections and comments come in
// pieces, cut at white space where there is some. A consumer that puts
// block boundaries only between tokens then has a source offset for
// each.
const MaxChunk = 64 << 10

// chunkEnd returns where to end a piece of comment or CDATA content b at
// most n bytes long: before a possible partial terminator at the end of
// b, after the last white space if there is some, and never inside a CR
// LF pair or a UTF-8 sequence.
func chunkEnd(b []byte, n int) int {
	n = min(n, len(b)-2)
	if n <= 0 {
		return 0
	}
	n = afterSpace(b, n)
	for n > 0 && !utf8.RuneStart(b[n]) {
		n--
	}
	if n > 0 && b[n-1] == '\r' {
		n--
	}
	return n
}

// afterSpace returns the offset just after the last white space other
// than CR in b[:n], or n if there is none.
func afterSpace(b []byte, n int) int {
	for i := n - 1; i > 0; i-- {
		if c := b[i]; c == ' ' || c == '\n' || c == '\t' {
			return i + 1
		}
	}
	return n
}

// section finds the next piece of a comment or CDATA section ending in
// term. It returns the piece's length, how much input it takes, whether
// it is the last piece, and false when more input is needed.
func (s *Scanner) section(term []byte) (n, used int, final, ok bool) {
	b := s.buf[s.pos:s.end]
	// Look no further than the end of a piece: a long section in memory
	// would otherwise be searched to its end once for each piece.
	lim := min(len(b), MaxChunk+len(term))
	i := bytes.Index(b[:lim], term)
	whole := lim == len(b) // all there is in view was searched
	switch {
	case i >= 0 && i <= MaxChunk:
		return i, i + len(term), true, true
	case i < 0 && whole && s.eof && len(b) <= MaxChunk:
		// Not terminated: what there is is the content.
		what := "comment is not terminated"
		if term[0] == ']' {
			what = "CDATA section is not terminated"
		}
		if !s.repair(what) {
			return 0, 0, false, false
		}
		return len(b), len(b), true, true
	case i < 0 && whole && !s.eof && (s.pos > 0 || s.end < len(s.buf)):
		return 0, 0, false, false // there is room to look further
	}
	n = chunkEnd(b, MaxChunk)
	return n, n, false, n > 0
}

// comment delivers the content of a comment, in pieces of at most
// MaxChunk bytes, each with its own span.
func (s *Scanner) comment() scanStatus {
	if !s.tokens {
		return s.skipComment()
	}
	n, used, final, ok := s.section([]byte("-->"))
	if s.err != nil {
		return failed
	}
	if !ok {
		return needMore
	}
	s.text = s.normalizeLines(s.buf[s.pos : s.pos+n])
	s.pos += used
	if final {
		s.mode = modeNormal
	}
	s.final, s.kind = final, Comment
	s.tokStart, s.tokEnd = s.constructStart, s.hereEnd()
	s.constructStart = s.tokEnd
	return emitted
}

// cdata delivers the content of a CDATA section as text, in pieces of at
// most MaxChunk bytes. The first piece's span starts at "<![CDATA[".
func (s *Scanner) cdata() scanStatus {
	n, used, final, ok := s.section([]byte("]]>"))
	if s.err != nil {
		return failed
	}
	if !ok {
		return needMore
	}
	s.text = s.normalizeLines(s.buf[s.pos : s.pos+n])
	s.pos += used
	if final {
		s.mode = modeNormal
	}
	start := s.constructStart
	s.constructStart = s.hereEnd()
	if n == 0 {
		return skipped
	}
	s.tokStart, s.tokEnd = start, s.hereEnd()
	s.kind = Text
	return emitted
}

// skipComment passes over a comment, which may be larger than the window,
// when comments are not tokens.
func (s *Scanner) skipComment() scanStatus {
	b := s.buf[s.pos:s.end]
	if i := bytes.Index(b, []byte("-->")); i >= 0 {
		s.pos += i + 3
		s.mode = modeNormal
		return skipped
	}
	if s.eof {
		if !s.repair("comment is not terminated") {
			return failed
		}
		s.pos, s.mode = s.end, modeNormal
		return skipped
	}
	// Keep the last two bytes: they may be the start of the terminator.
	if len(b) > 2 {
		s.pos = s.end - 2
	}
	return needMore
}

var (
	cdataOpen   = []byte("<![CDATA[")
	commentOpen = []byte("<!--")
	doctypeOpen = []byte("<!DOCTYPE")
)

func isNameStart(c byte) bool { return nameStarts[c] }

// nameStarts marks the bytes a name may start with.
var nameStarts = func() (t [256]bool) {
	for c := range 256 {
		b := byte(c)
		t[c] = b >= 0x80 || b|0x20 >= 'a' && b|0x20 <= 'z' || b == '_' || b == ':'
	}
	return
}()

// stray makes a '<' that starts no markup a Text token of its own.
func (s *Scanner) stray() scanStatus {
	if !s.repair("a '<' that starts no markup") {
		return failed
	}
	s.tokStart = s.here()
	s.text = s.buf[s.pos : s.pos+1]
	s.pos++
	s.tokEnd = s.hereEnd()
	s.kind = Text
	return emitted
}

// markup scans a construct that starts with '<'.
func (s *Scanner) markup() scanStatus {
	b := s.buf[s.pos:s.end]
	if len(b) < 2 {
		if s.eof {
			return s.stray()
		}
		return needMore
	}
	// A start tag, the most frequent construct, is tested first; its span
	// is set here, so that startTag pays nothing for spans when they are
	// off.
	if nameStarts[b[1]] {
		if !s.spans {
			return s.startTag(b)
		}
		from := s.pos
		st := s.startTag(b)
		if st == emitted {
			s.tokStart, s.tokEnd = s.at(from), s.hereEnd()
		}
		return st
	}
	switch c := b[1]; {
	case c == '/':
		// The usual case, here rather than in endTag: the end tag of the
		// innermost element, written with no space. Compare it in place.
		if n := len(s.stack); n > 0 && s.over == 0 {
			top := &s.stack[n-1]
			end := 2 + int(top.fullLen)
			if end < len(b) && b[end] == '>' && s.named(n-1, b[2:end]) {
				from := s.pos
				s.pos += end + 1
				s.pop()
				s.kind = End
				if s.spans {
					s.tokStart, s.tokEnd = s.at(from), s.hereEnd()
				}
				return emitted
			}
		}
		return s.endTag(b)
	case c == '?':
		return s.pi(b)
	case c == '!':
		switch {
		case bytes.HasPrefix(b, commentOpen):
			s.constructStart = s.here()
			s.pos += len(commentOpen)
			s.mode = modeComment
			return skipped
		case bytes.HasPrefix(b, cdataOpen):
			s.constructStart = s.here()
			s.pos += len(cdataOpen)
			s.mode = modeCDATA
			return skipped
		case len(b) >= len(doctypeOpen) && bytes.EqualFold(b[:len(doctypeOpen)], doctypeOpen):
			if !s.tokens {
				return s.doctype(b)
			}
			// final: no more input will come into view, at the end of
			// the input or with the window at the token limit. The
			// declaration's own window is the token limit.
			final := s.eof || len(b) >= s.maxToken
			win := b
			if final {
				win = b[:min(len(b), s.maxToken)]
			}
			n, repaired := doctypeEnd(win, final)
			if n < 0 {
				return needMore
			}
			if repaired {
				s.Stats.Repairs++
			}
			s.tokStart = s.here()
			s.text = bytes.TrimSuffix(b[len(doctypeOpen):n], []byte(">"))
			s.pos += n
			s.tokEnd = s.hereEnd()
			s.kind = Doctype
			return emitted
		case len(b) < len(doctypeOpen) && !s.eof:
			return needMore
		}
		return s.stray()
	}
	return s.stray()
}

// doctype passes over a document type declaration that declares nothing,
// when the declaration is not a token: one before the root element, the
// only one, with no internal subset. It names the root element and perhaps
// an external DTD, which is never fetched, so passing over it changes
// nothing that is read. Any other is ErrDTD: an internal subset is where
// entities are declared. b starts at "<!DOCTYPE".
func (s *Scanner) doctype(b []byte) scanStatus {
	if s.started || s.doctyped {
		s.fail(fmt.Errorf("%w (at byte %d)", ErrDTD, s.here()))
		return failed
	}
	var quote byte
	for i := len(doctypeOpen); i < len(b); i++ {
		switch c := b[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '[':
			s.fail(fmt.Errorf("%w (at byte %d)", ErrDTD, s.here()))
			return failed
		case c == '<':
			s.syntax("unterminated document type declaration")
			return failed
		case c == '>':
			s.doctyped = true
			s.pos += i + 1
			return skipped
		}
	}
	return needMore
}

// doctypeEnd returns the length of the document type declaration at the
// start of b, or -1 if it does not end in b. Quoted strings, comments and
// processing instructions in its internal subset may hold '>'.
//
// Outside the internal subset a literal (a public or system identifier)
// holds no '<': one that runs into a '<' lacks its closing quote, and the
// declaration ends at the first '>' after the quote, or else before the
// '<'; repaired reports that.
//
// Inside the internal subset, a comment, processing instruction or
// literal that is not closed in b when final (no more input will come)
// ends at the subset's "]>" after it when there is one, else at the end
// of b; repaired reports that too, and the document after it is read.
func doctypeEnd(b []byte, final bool) (n int, repaired bool) {
	depth := 0
	unclosed := func(i int) (int, bool) {
		if !final {
			return -1, false
		}
		if depth > 0 {
			if j := bytes.Index(b[i:], []byte("]>")); j >= 0 {
				return i + j + 2, true
			}
		}
		return len(b), true
	}
	for i := len(doctypeOpen); i < len(b); i++ {
		switch c := b[i]; c {
		case '"', '\'':
			j := bytes.IndexByte(b[i+1:], c)
			if depth == 0 {
				lit := b[i+1:]
				if j >= 0 {
					lit = lit[:j]
				}
				if lt := bytes.IndexByte(lit, '<'); lt >= 0 {
					if gt := bytes.IndexByte(lit[:lt], '>'); gt >= 0 {
						return i + 1 + gt + 1, true
					}
					return i + 1 + lt, true
				}
			}
			if j < 0 {
				return unclosed(i)
			}
			i += j + 1
		case '<':
			if rest := b[i:]; len(rest) < len(commentOpen) && bytes.HasPrefix(commentOpen, rest) {
				return unclosed(i) // what follows decides: see more first
			}
			if bytes.HasPrefix(b[i:], commentOpen) {
				j := bytes.Index(b[i+4:], []byte("-->"))
				if j < 0 {
					return unclosed(i)
				}
				i += 4 + j + 2
			} else if i+1 < len(b) && b[i+1] == '?' {
				j := bytes.Index(b[i+2:], []byte("?>"))
				if j < 0 {
					return unclosed(i)
				}
				i += 2 + j + 1
			} else if depth == 0 {
				return i, true // cut off by the next tag
			}
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '>':
			if depth == 0 {
				return i + 1, false
			}
		}
	}
	return -1, false
}

// pi scans a processing instruction.
func (s *Scanner) pi(b []byte) scanStatus {
	i := bytes.Index(b[2:], []byte("?>"))
	if i < 0 {
		return needMore
	}
	i += 2
	if !s.tokens {
		s.pos += i + 2
		return skipped
	}
	body := b[2:i]
	t := 0
	for t < len(body) && !isSpace(body[t]) {
		t++
	}
	s.target = body[:t]
	if !charset.ValidUTF8(s.target) {
		s.target, _ = mendName(s.target, &s.Stats.InvalidUTF8)
	}
	data := body[t:]
	for len(data) > 0 && isSpace(data[0]) {
		data = data[1:]
	}
	s.tokStart = s.here()
	s.text = s.normalizeLines(data)
	s.pos += i + 2
	s.tokEnd = s.hereEnd()
	s.kind = PI
	return emitted
}

// endTag scans an end tag.
func (s *Scanner) endTag(b []byte) scanStatus {
	from := s.pos
	// The usual case: the end tag of the innermost element, written with
	// no space. Compare it in place.
	if n := len(s.stack); n > 0 && s.over == 0 {
		top := &s.stack[n-1]
		end := 2 + int(top.fullLen)
		if end < len(b) && b[end] == '>' && s.named(n-1, b[2:end]) {
			s.pos += end + 1
			s.pop()
			s.kind = End
			if s.spans {
				s.tokStart, s.tokEnd = s.at(from), s.hereEnd()
			}
			return emitted
		}
	}
	if len(b) < 3 {
		return needMore
	}
	if !isNameStart(b[2]) {
		return s.stray()
	}
	i := bytes.IndexByte(b, '>')
	if i < 0 {
		return needMore
	}
	name := b[2:i]
	for j, c := range name {
		if isSpace(c) {
			name = name[:j]
			break
		}
	}
	if s.mendedName && !charset.ValidUTF8(name) {
		name, _ = mendName(name, nil) // as its start tag's was, counted there
	}
	if s.over > 0 {
		// Inside elements read as part of their ancestor.
		s.over--
		s.pos += i + 1
		return skipped
	}
	matched := len(s.stack) - 1
	if !s.recover && matched >= 0 && !s.named(matched, name) {
		top := &s.stack[matched]
		s.syntax(fmt.Sprintf("end tag </%s> does not match <%s>", clip(name), clip(s.names[top.nameStart:top.nameStart+top.nameLen])))
		return failed
	}
	for ; matched >= 0 && !s.named(matched, name); matched-- {
	}
	if matched < 0 {
		// An end tag that closes nothing is dropped.
		if !s.repair("end tag without a start tag") {
			return failed
		}
		s.pos += i + 1
		return skipped
	}
	// Close the elements left open inside the matched one first; closing
	// is then non-zero and Next hands out their Ends.
	s.closing = len(s.stack) - 1 - matched
	if s.closing > 0 {
		s.Stats.Repairs++
	}
	s.pos += i + 1
	s.pop()
	s.kind = End
	if s.spans {
		s.tokStart, s.tokEnd = s.at(from), s.hereEnd()
	}
	return emitted
}

// named reports whether the open element at stack index i has the given
// qualified name.
func (s *Scanner) named(i int, name []byte) bool {
	e := &s.stack[i]
	return len(name) == int(e.fullLen) && string(s.names[e.nameStart:e.nameStart+e.nameLen]) == string(name[:e.nameLen])
}

func isSpace(c byte) bool { return c == ' ' || c == '\n' || c == '\t' || c == '\r' }

func clip(b []byte) []byte {
	if len(b) > 40 {
		return append(append([]byte{}, b[:40]...), "..."...)
	}
	return b
}

// nameEnds marks the bytes that end a tag's name, and ':' and the bytes
// beyond ASCII, which are noted on the way; attrNameEnds the same for an
// attribute's name, which '=' also ends. One look-up a byte rather than a
// comparison for each: a name of ASCII, the usual case, pays nothing for
// the UTF-8 check.
var nameEnds, attrNameEnds = func() (n, a [256]bool) {
	for _, c := range []byte(">/<: \n\t\r") {
		n[c], a[c] = true, true
	}
	a['='] = true
	for c := 0x80; c < 0x100; c++ {
		n[c], a[c] = true, true // not an end: noted, for the UTF-8 check
	}
	return
}()

// startTag parses a start tag or empty-element tag beginning at b[0]. The
// whole tag must be in the window. markup sets its span.
func (s *Scanner) startTag(b []byte) scanStatus {
	if len(b) > maxTag {
		b = b[:maxTag]
	}
	i, colon := 1, -1
	s.namesHigh = false
	for ; i < len(b); i++ {
		if c := b[i]; nameEnds[c] {
			if c != ':' {
				if c < 0x80 {
					break
				}
				s.namesHigh = true // to be checked for UTF-8
				continue
			}
			if colon < 0 {
				colon = i - 1
			}
		}
	}
	if i == len(b) {
		return needMore
	}
	name := b[1:i]
	s.attrs = s.attrs[:0]
	var selfClose, declares bool
	switch {
	case b[i] == '>':
		i++ // no attributes, the usual case: no call
	case b[i] == '/' && i+1 < len(b) && b[i+1] == '>':
		selfClose = true
		i += 2
	default:
		var st scanStatus
		if s.recover {
			i, selfClose, declares, st = s.attrsLenient(b, i)
		} else {
			i, selfClose, declares, st = s.attrsStrict(b, i)
		}
		if st != emitted {
			return st
		}
	}
	if len(s.stack) >= s.maxDepth || s.over > 0 {
		if !s.recover {
			s.fail(fmt.Errorf("%w (limit %d) at byte %d", ErrDepth, s.maxDepth, s.here()))
			return failed
		}
		// Too deep: read as part of the ancestor.
		s.Stats.Flattened++
		if !selfClose {
			s.over++
		}
		s.pos += i
		s.attrs = s.attrs[:0]
		return skipped
	}
	s.tag = b[:i]
	s.push(name, colon, declares)
	s.pos += i
	s.pendingEnd = selfClose
	s.kind = Start
	return emitted // markup sets the span
}

// attrsStrict reads the attributes of a start tag from b[i:], up to and
// including the tag's end, as well-formed XML has them: any error ends the
// scan. It returns where the tag ends, whether it is an empty-element tag,
// whether an attribute may declare a namespace, and emitted, or what to
// do instead. It is go-fast-ooxml's loop, in a function of its own so
// that it keeps its variables in registers; measured, it is slower in
// startTag, and slower merged with attrsLenient.
func (s *Scanner) attrsStrict(b []byte, i int) (int, bool, bool, scanStatus) {
	declares := false
	for {
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i == len(b) {
			return i, false, false, needMore
		}
		switch b[i] {
		case '>':
			return i + 1, false, declares, emitted
		case '/':
			if i+1 == len(b) {
				return i, false, false, needMore
			}
			if b[i+1] != '>' {
				s.syntax("stray / in a tag")
				return i, false, false, failed
			}
			return i + 2, true, declares, emitted
		case '<':
			s.syntax("tag is not closed")
			return i, false, false, failed
		}
		// An attribute: name, '=', quoted value.
		start, acolon := i, -1
		for ; i < len(b); i++ {
			if c := b[i]; attrNameEnds[c] {
				if c != ':' {
					if c < 0x80 {
						break
					}
					s.namesHigh = true // to be checked for UTF-8
					continue
				}
				if acolon < 0 {
					acolon = i - start
				}
			}
		}
		if i == len(b) {
			return i, false, false, needMore
		}
		nameEnd := i
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i == len(b) {
			return i, false, false, needMore
		}
		if b[i] != '=' || nameEnd == start {
			s.syntax("attribute without a value")
			return i, false, false, failed
		}
		i++
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i == len(b) {
			return i, false, false, needMore
		}
		quote := b[i]
		if quote != '"' && quote != '\'' {
			s.syntax("attribute value is not quoted")
			return i, false, false, failed
		}
		i++
		// Most values are a few bytes long: look by hand first, and only
		// call out for the long ones.
		n, limit := 0, min(len(b)-i, 24)
		for n < limit && b[i+n] != quote {
			n++
		}
		if n == limit {
			m := bytes.IndexByte(b[i+n:], quote)
			if m < 0 {
				return i, false, false, needMore
			}
			n += m
		}
		if len(s.attrs) >= s.maxAttrs {
			s.Stats.AttrsCut++
		} else {
			if b[start] == 'x' && nameEnd-start >= 5 {
				declares = true
			}
			s.attrs = append(s.attrs, attr{int32(start), int32(nameEnd), int32(acolon), int32(i), int32(i + n)})
		}
		i += n + 1
	}
}

// attrsLenient reads the attributes of a start tag from b[i:], up to and
// including the tag's end, for Options.Recover: it reads on through damaged
// attributes, counting each repair. It returns where the tag ends, whether
// it is an empty-element tag, whether an attribute may declare a
// namespace, and emitted, or what to do instead.
func (s *Scanner) attrsLenient(b []byte, i int) (int, bool, bool, scanStatus) {
	selfClose, declares := false, false
	repairs, cut := 0, 0
	for {
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i == len(b) {
			return i, false, false, needMore
		}
		c := b[i]
		if c == '>' {
			i++
			break
		}
		if c == '<' {
			// A tag that never closed: it ends where the next begins.
			repairs++
			break
		}
		if c == '/' {
			if i+1 == len(b) {
				return i, false, false, needMore
			}
			if b[i+1] == '>' {
				selfClose = true
				i += 2
				break
			}
			repairs++
			i++
			continue
		}
		// An attribute: name, '=', quoted value.
		start, acolon := i, -1
		for ; i < len(b); i++ {
			if c := b[i]; attrNameEnds[c] {
				if c != ':' {
					if c < 0x80 {
						break
					}
					s.namesHigh = true // to be checked for UTF-8
					continue
				}
				if acolon < 0 {
					acolon = i - start
				}
			}
		}
		if i == len(b) {
			return i, false, false, needMore
		}
		nameEnd := i
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i == len(b) {
			return i, false, false, needMore
		}
		if b[i] != '=' {
			repairs++
			continue
		}
		i++
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i == len(b) {
			return i, false, false, needMore
		}
		var vs, ve int
		if quote := b[i]; quote == '"' || quote == '\'' {
			i++
			// Most values are a few bytes long: look by hand first, and
			// only call out for the long ones.
			n, limit := 0, min(len(b)-i, 24)
			for n < limit && b[i+n] != quote && b[i+n] != '<' {
				n++
			}
			if n == limit {
				m := bytes.IndexByte(b[i+n:], quote)
				if m < 0 {
					m = len(b) - i - n
				}
				if lt := bytes.IndexByte(b[i+n:i+n+m], '<'); lt >= 0 {
					m = lt
				} else if i+n+m == len(b) && !s.eof {
					return i, false, false, needMore
				}
				n += m
			}
			if i+n < len(b) && b[i+n] == quote {
				vs, ve = i, i+n
				i += n + 1
			} else {
				// A value can hold no '<': the closing quote is
				// missing. The value ends at the first '>', which
				// ends the tag, or else where the next tag begins.
				repairs++
				if gt := bytes.IndexByte(b[i:i+n], '>'); gt >= 0 {
					n = gt
				}
				vs, ve = i, i+n
				i += n
				if i == len(b) {
					return i, false, false, needMore // at the end of the input: Next keeps it as text
				}
			}
		} else {
			repairs++ // a value without quotes runs to white space or '>'
			vs = i
			for i < len(b) && !isSpace(b[i]) && b[i] != '>' {
				i++
			}
			if i == len(b) {
				return i, false, false, needMore
			}
			ve = i
		}
		if nameEnd == start {
			repairs++
			continue
		}
		if len(s.attrs) >= s.maxAttrs {
			cut++
			continue
		}
		if b[start] == 'x' && nameEnd-start >= 5 {
			declares = true
		}
		s.attrs = append(s.attrs, attr{int32(start), int32(nameEnd), int32(acolon), int32(vs), int32(ve)})
	}
	s.Stats.Repairs += repairs
	s.Stats.AttrsCut += cut
	return i, selfClose, declares, emitted
}

var xmlnsPrefix = []byte("xmlns")

// push opens an element: it records the namespace declarations among the
// attributes just parsed, resolves the element's own namespace and puts
// the element on the stack. colon is the index of ':' in name, or -1.
// declares says an attribute name starts with "x" and is long enough to
// be "xmlns", the only way the tag can hold a namespace declaration.
func (s *Scanner) push(name []byte, colon int, declares bool) {
	s.started = true
	full, fcolon := name, colon
	if s.namesHigh {
		full, fcolon = s.fixNames(name, colon)
	}
	kept := full
	if len(kept) > MaxNameKept {
		// Cut where a character starts, as charset.CutUTF8 does, written
		// out: a call here costs every tag, cut or not.
		n := MaxNameKept
		for n > 0 && !utf8.RuneStart(kept[n]) {
			n--
		}
		kept = kept[:n]
		s.Stats.NamesCut++
	}
	e := elem{
		nameStart: int32(len(s.names)),
		nameLen:   int32(len(kept)),
		fullLen:   int32(len(full)),
		colon:     int32(fcolon),
		bindMark:  int32(len(s.binds)),
		bindBytes: int32(len(s.bindNames)),
	}
	for i := 0; declares && i < len(s.attrs); i++ {
		a := &s.attrs[i]
		aname := s.attrName(a)
		if len(aname) < 5 || aname[0] != 'x' || !bytes.HasPrefix(aname, xmlnsPrefix) {
			continue
		}
		var prefix []byte
		switch {
		case len(aname) == 5:
		case aname[5] == ':':
			prefix = aname[6:]
		default:
			continue // an ordinary attribute whose name starts with "xmlns"
		}
		s.declare(prefix, s.attrValue(i))
	}
	if int(e.bindMark) != len(s.binds) {
		s.gen++
	}

	// The prefix as written: declarations bind prefixes as written.
	var prefix []byte
	if colon >= 0 {
		prefix = name[:colon]
	}
	// A child usually has its parent's prefix. If this tag declared
	// nothing, the parent's answer is the answer.
	if n := len(s.stack); n > 0 && int(e.bindMark) == len(s.binds) && s.stack[n-1].nameLen == s.stack[n-1].fullLen {
		p := &s.stack[n-1]
		var parentPrefix []byte
		if p.colon >= 0 {
			parentPrefix = s.names[p.nameStart : p.nameStart+p.colon]
		}
		if string(parentPrefix) == string(prefix) {
			e.bind, e.space = p.bind, p.space
			goto resolved
		}
	}
	e.bind = s.resolveBind(prefix, true)
	e.space = s.spaceOf(e.bind)
resolved:
	s.names = append(s.names, kept...)
	s.stack = append(s.stack, e)
	s.qname = s.names[e.nameStart : e.nameStart+e.nameLen]
	s.local = localOf(s.qname, e.colon)
	s.bind, s.space = e.bind, e.space
}

// declare binds prefix ("" for the default namespace) to uri, until the
// element being opened, if any, closes.
func (s *Scanner) declare(prefix, uri []byte) {
	space := SpaceUnknown
	if len(uri) == 0 {
		space = NoSpace // xmlns="" undeclares the default namespace
	} else if v, ok := s.spaces[string(uri)]; ok {
		space = v
	}
	if len(s.bindNames)+len(prefix)+len(uri) > MaxBindingBytes {
		s.Stats.BindingsCut++
		return
	}
	slot, known := s.slots[string(prefix)]
	if !known {
		if s.slots == nil {
			s.slots = map[string]int32{}
		}
		if n := len(s.freeSlots); n > 0 {
			slot = s.freeSlots[n-1]
			s.freeSlots = s.freeSlots[:n-1]
		} else {
			slot = int32(len(s.heads))
			s.heads = append(s.heads, -1)
		}
		s.slots[string(prefix)] = slot
	}
	start := int32(len(s.bindNames))
	s.bindNames = append(s.bindNames, prefix...)
	end := int32(len(s.bindNames))
	s.bindNames = append(s.bindNames, uri...)
	s.binds = append(s.binds, binding{start, end, int32(len(s.bindNames)), space, slot, s.heads[slot]})
	s.heads[slot] = int32(len(s.binds) - 1)
	s.gen++
}

// localOf returns the local part of a kept name; empty when the name was
// cut before its colon.
func localOf(qname []byte, colon int32) []byte {
	return qname[min(int(colon)+1, len(qname)):]
}

// resolveBind returns the binding of prefix. An element without a prefix
// takes the default namespace; an attribute without one has none.
func (s *Scanner) resolveBind(prefix []byte, element bool) int32 {
	if len(prefix) == 0 && !element {
		return bindNone
	}
	if s.lastGen == s.gen && s.lastPrefix != nil && string(s.lastPrefix) == string(prefix) {
		return s.lastBind
	}
	b := s.lookupBind(prefix)
	s.lastPrefix, s.lastBind, s.lastGen = append(s.lastPrefix[:0], prefix...), b, s.gen
	return b
}

// lookupBind is resolveBind without the last answer kept.
func (s *Scanner) lookupBind(prefix []byte) int32 {
	if len(s.binds) <= linearBindings {
		for i := len(s.binds) - 1; i >= 0; i-- {
			b := &s.binds[i]
			if string(s.bindNames[b.start:b.end]) == string(prefix) {
				return s.emptyIsNone(int32(i))
			}
		}
	} else if slot, ok := s.slots[string(prefix)]; ok && s.heads[slot] >= 0 {
		return s.emptyIsNone(s.heads[slot])
	}
	switch {
	case len(prefix) == 0:
		return bindNone
	case string(prefix) == "xml":
		return bindXML
	}
	return bindUndeclared
}

func (s *Scanner) emptyIsNone(i int32) int32 {
	if b := &s.binds[i]; b.uend == b.end {
		return bindNone
	}
	return i
}

// pop closes the innermost element and makes it the current token.
func (s *Scanner) pop() {
	e := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	s.qname = s.names[e.nameStart : e.nameStart+e.nameLen]
	s.local = localOf(s.qname, e.colon)
	s.bind, s.space = e.bind, e.space
	// The name stays readable until the next push: names are only
	// truncated here, and qname points below the new length.
	s.names = s.names[:e.nameStart]
	if int(e.bindMark) == len(s.binds) {
		return
	}
	if e.bind >= e.bindMark {
		// The element declared its own namespace, which goes out of
		// scope here: keep a copy for the End token.
		s.endURI = append(s.endURI[:0], s.uriOf(e.bind)...)
		s.endSpace = s.binds[e.bind].space
		s.bind = bindEnded
	}
	for i := len(s.binds) - 1; i >= int(e.bindMark); i-- {
		b := &s.binds[i]
		s.heads[b.slot] = b.prev // what this element shadowed is visible again
		if b.prev < 0 && len(s.slots) > maxSlotsKept {
			// The prefix is out of scope. A document that keeps
			// declaring new prefixes makes its slot free again, so the
			// table holds only the prefixes of the open elements; the
			// few a document uses throughout are kept, so they cost no
			// map entry each time.
			delete(s.slots, string(s.bindNames[b.start:b.end]))
			s.freeSlots = append(s.freeSlots, b.slot)
		}
	}
	s.binds = s.binds[:e.bindMark]
	s.bindNames = s.bindNames[:e.bindBytes]
	s.gen++
}

// Is reports whether the current Start or End token is the element with
// the given namespace and local name.
func (s *Scanner) Is(space Space, local string) bool {
	return s.space == space && string(s.local) == local
}

func (s *Scanner) attrName(a *attr) []byte  { return s.tag[a.name:a.nameEnd] }
func (s *Scanner) attrLocal(a *attr) []byte { return s.tag[a.name+a.colon+1 : a.nameEnd] }
func (s *Scanner) attrPrefix(a *attr) []byte {
	if a.colon < 0 {
		return nil
	}
	return s.tag[a.name : a.name+a.colon]
}

// Attr returns the value of the attribute with the given namespace and
// local name on the current Start token. An attribute written without a
// prefix has namespace NoSpace. The value has references decoded, is
// valid UTF-8 as Text is, and is good until the next call to Next.
func (s *Scanner) Attr(space Space, local string) ([]byte, bool) {
	for i := range s.attrs {
		a := &s.attrs[i]
		if string(s.attrLocal(a)) != local {
			continue
		}
		if s.spaceOf(s.resolveBind(s.attrPrefix(a), false)) != space {
			continue
		}
		return s.attrValue(i), true
	}
	return nil, false
}

// AttrURI returns the value of the attribute with the given namespace URI
// (empty for none) and local name on the current Start token.
func (s *Scanner) AttrURI(uri, local string) ([]byte, bool) {
	for i := range s.attrs {
		a := &s.attrs[i]
		if string(s.attrLocal(a)) != local {
			continue
		}
		if string(s.attrURI(a)) == uri {
			return s.attrValue(i), true
		}
	}
	return nil, false
}

// AttrAny returns the value of the first attribute with the given local
// name, whatever its namespace. It is for attributes that producers write
// both with and without a prefix.
func (s *Scanner) AttrAny(local string) ([]byte, bool) {
	for i := range s.attrs {
		a := &s.attrs[i]
		if string(s.attrLocal(a)) == local {
			return s.attrValue(i), true
		}
	}
	return nil, false
}

// NumAttr returns the number of attributes kept on the current Start
// token, namespace declarations included.
func (s *Scanner) NumAttr() int { return len(s.attrs) }

// AttrNameAt returns the qualified name of the i-th attribute of the
// current Start token as written in the source, prefix included. It is
// valid UTF-8 as Name is.
func (s *Scanner) AttrNameAt(i int) []byte {
	if s.namesHigh && i < len(s.attrFix) && s.attrFix[i] != nil {
		return s.attrFix[i]
	}
	return s.attrName(&s.attrs[i])
}

// fixedLocal returns the local name of the i-th attribute made valid
// UTF-8, or local when it is: for a start tag with a name beyond ASCII,
// out of line so that the usual case costs a test.
func (s *Scanner) fixedLocal(i int, local []byte) []byte {
	if i < len(s.attrFix) && s.attrFix[i] != nil {
		f := s.attrFix[i]
		return f[bytes.IndexByte(f, ':')+1:]
	}
	return local
}

// AttrAt returns the i-th attribute of the current Start token: its
// namespace, local name and decoded value. A namespace declaration's
// namespace is that of the prefix "xmlns" (SpaceUnknown), or NoSpace for
// the default namespace's.
func (s *Scanner) AttrAt(i int) (space Space, local, value []byte) {
	a := &s.attrs[i]
	local = s.attrLocal(a)
	if s.namesHigh {
		local = s.fixedLocal(i, local)
	}
	return s.spaceOf(s.resolveBind(s.attrPrefix(a), false)), local, s.attrValue(i)
}

// AttrURIAt returns the i-th attribute of the current Start token: its
// namespace URI (empty for none), local name and decoded value. A
// namespace declaration has the xmlns namespace.
func (s *Scanner) AttrURIAt(i int) (uri, local, value []byte) {
	a := &s.attrs[i]
	local = s.attrLocal(a)
	if s.namesHigh {
		local = s.fixedLocal(i, local)
	}
	return s.attrURI(a), local, s.attrValue(i)
}

func (s *Scanner) attrURI(a *attr) []byte {
	prefix := s.attrPrefix(a)
	switch {
	case a.colon < 0 && string(s.attrName(a)) == "xmlns":
		return xmlnsNamespace
	case a.colon >= 0 && string(prefix) == "xmlns":
		return xmlnsNamespace
	case a.colon >= 0:
		return s.uriOf(s.resolveBind(prefix, false))
	}
	return nil
}

var xmlnsNamespace = []byte(XMLNSNamespace)

// attrValue decodes an attribute's value, within the bounds: one value to
// Options.MaxValueBytes, all those read from one tag together to
// Options.MaxTokenBytes.
func (s *Scanner) attrValue(i int) []byte {
	a := &s.attrs[i]
	raw := s.tag[a.value:a.valueEnd]
	if len(raw) <= s.maxValue && !needsAttrDecode(raw) {
		return raw // the usual case: nothing to decode or cut
	}
	for _, r := range s.read {
		if int(r.i) == i {
			if r.raw {
				return raw[:r.n]
			}
			return s.attrBuf[r.at : r.at+r.n]
		}
	}
	start := len(s.attrBuf)
	v := s.decodeValue(raw)
	r := readValue{i: int32(i), at: int32(start), n: int32(len(v))}
	r.raw = len(v) == 0 || &v[0] == &raw[0]
	s.read = append(s.read, r)
	return v
}

// decodeValue returns the decoded form of a raw attribute value, cut to
// the bounds and counted when it passes them.
func (s *Scanner) decodeValue(raw []byte) []byte {
	if bytes.IndexByte(raw, '&') < 0 && !hasAttrSpace(raw) {
		// Nothing to decode: bytes beyond ASCII, which must be UTF-8.
		v := raw
		if !charset.ValidUTF8(v) {
			start := len(s.attrBuf)
			s.attrBuf = s.appendValid(s.attrBuf, v)
			v = s.attrBuf[start:]
		}
		if len(v) > s.maxValue {
			s.Stats.ValuesCut++
			v = charset.CutUTF8(v, s.maxValue)
		}
		return v
	}
	room := min(s.maxValue, max(s.maxToken-len(s.attrBuf), 0))
	start := len(s.attrBuf)
	s.valueEnd, s.valueFull = start+room, false
	s.attrBuf = s.decodeAttr(s.attrBuf, raw)
	v := s.attrBuf[start:]
	if !charset.ValidUTF8(v) {
		s.fixValue = s.appendValid(s.fixValue[:0], v)
		s.attrBuf = append(s.attrBuf[:start], s.fixValue...)
		v = s.attrBuf[start:]
	}
	if s.valueFull || len(v) > room {
		s.Stats.ValuesCut++
		v = charset.CutUTF8(v, room)
		s.attrBuf = s.attrBuf[:start+len(v)]
	}
	// A value that needed decoding may leave attrBuf's backing array when
	// it grows; earlier values keep pointing at the old array, which stays
	// valid, so they are unaffected.
	return v
}

// needsAttrDecode reports whether a value holds '&', a byte below 0x0E
// (tab, line feed and carriage return become spaces) or a byte beyond
// ASCII (UTF-8, to be checked), looking at eight bytes at a time. A value
// of printable ASCII is used as it stands.
func needsAttrDecode(v []byte) bool {
	const ones, highs = 0x0101010101010101, 0x8080808080808080
	for len(v) >= 8 {
		x := binary.LittleEndian.Uint64(v)
		y := x ^ (0x26 * ones) // '&' becomes zero
		if ((y-ones)&^y|(x-0x0E*ones)&^x|x)&highs != 0 {
			break
		}
		v = v[8:]
	}
	for _, c := range v {
		if c == '&' || c == '\t' || c == '\n' || c == '\r' || c >= 0x80 {
			return true
		}
	}
	return false
}

// hasAttrSpace reports whether a value holds a tab, a line feed or a
// carriage return, which become spaces.
func hasAttrSpace(v []byte) bool {
	for _, c := range v {
		if c == '\t' || c == '\n' || c == '\r' {
			return true
		}
	}
	return false
}

// validText returns t, or, when t is not valid UTF-8, a copy in which
// each byte that is not part of a character is U+FFFD.
func (s *Scanner) validText(t []byte) []byte {
	if charset.ValidUTF8(t) {
		return t
	}
	s.fixed = s.appendValid(s.fixed[:0], t)
	return s.fixed
}

// appendValid appends b to dst with each byte that is not part of a UTF-8
// character replaced by U+FFFD, and counts those bytes in
// Stats.InvalidUTF8.
func (s *Scanner) appendValid(dst, b []byte) []byte {
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r == utf8.RuneError && n == 1 {
			dst = utf8.AppendRune(dst, utf8.RuneError)
			s.Stats.InvalidUTF8++
		} else {
			dst = append(dst, b[:n]...)
		}
		b = b[n:]
	}
	return dst
}

// mendName returns a copy of name with each byte that is not part of a
// UTF-8 character replaced by U+FFFD, and its local part (after the
// first ':'), adding the bytes replaced to *count when count is not nil.
// It is for names, which are rarely not ASCII and more rarely not valid:
// the copy is not kept for reuse, so a name handed out stays as it was.
func mendName(name []byte, count *int) (qname, local []byte) {
	n := 0
	q := make([]byte, 0, len(name)+8)
	for b := name; len(b) > 0; {
		r, m := utf8.DecodeRune(b)
		if r == utf8.RuneError && m == 1 {
			q = utf8.AppendRune(q, utf8.RuneError)
			n++
		} else {
			q = append(q, b[:m]...)
		}
		b = b[m:]
	}
	if count != nil {
		*count += n
	}
	return q, q[bytes.IndexByte(q, ':')+1:]
}

// fixNames makes the names of the start tag being read valid UTF-8, those
// that are not. It returns the element's name and the offset of its ':',
// mended: kept so, the element has it at its Start and its End, and
// endTag mends an end tag's name to match. Its attributes' names are kept
// in attrFix.
func (s *Scanner) fixNames(name []byte, colon int) ([]byte, int) {
	if !charset.ValidUTF8(name) {
		name, _ = mendName(name, &s.Stats.InvalidUTF8)
		colon = bytes.IndexByte(name, ':')
		s.mendedName = true
	}
	s.attrFix = s.attrFix[:0]
	for i := range s.attrs {
		var f []byte
		if an := s.attrName(&s.attrs[i]); !charset.ValidUTF8(an) {
			f, _ = mendName(an, &s.Stats.InvalidUTF8)
		}
		s.attrFix = append(s.attrFix, f)
	}
	return name, colon
}

// decodeAttr appends the decoded form of raw to buf. White space
// characters written literally become spaces, as XML requires for
// attribute values. Entities to expand are expanded here, as text.
func (s *Scanner) decodeAttr(buf, raw []byte) []byte {
	for i := 0; i < len(raw); {
		switch c := raw[i]; c {
		case '&':
			if len(buf) > s.valueEnd {
				// Past the bound: nothing more is expanded or kept.
				s.valueFull = true
				return buf
			}
			if name, n, ok := refName(raw[i:]); ok && predefined(name) == 0 && s.resolve != nil {
				switch repl, how := s.resolve(name); how {
				case Expand:
					buf = s.expandAttr(buf, raw[i:i+n], name, repl)
				case Insert:
					buf = append(buf, repl...)
				default:
					buf = append(buf, raw[i:i+n]...)
				}
				i += n
				continue
			}
			var n int
			buf, n, _ = s.reference(buf, raw[i:])
			i += n
		case '\r':
			// Line ends are normalized first, so CR LF is one line end
			// and becomes one space.
			buf = append(buf, ' ')
			i++
			if i < len(raw) && raw[i] == '\n' {
				i++
			}
		case '\t', '\n':
			buf = append(buf, ' ')
			i++
		default:
			buf = append(buf, c)
			i++
		}
	}
	return buf
}

// expandAttr appends the expansion of an entity reference in an
// attribute value, or the reference as written where a bound or
// recursion refuses it.
func (s *Scanner) expandAttr(buf, ref, name, repl []byte) []byte {
	for _, open := range s.attrNested {
		if bytes.Equal(open, name) {
			s.Stats.EntitiesRecursive++
			return append(buf, ref...)
		}
	}
	if len(s.attrNested)+len(s.frames) >= s.maxEntity || s.expLeft < int64(len(repl)) {
		s.Stats.EntitiesCut++
		s.expLeft = -1
		return append(buf, ref...)
	}
	s.expLeft -= int64(len(repl))
	s.Stats.Expanded += int64(len(repl))
	s.attrNested = append(s.attrNested, name)
	buf = s.decodeAttr(buf, repl)
	s.attrNested = s.attrNested[:len(s.attrNested)-1]
	return buf
}

// decodeText returns raw with references decoded and line ends
// normalized, and how much of raw it took: it stops before a reference
// whose replacement is read as markup.
func (s *Scanner) decodeText(raw []byte) ([]byte, int) {
	if len(raw) <= 32 {
		// Short text, the usual case in a spreadsheet: one look by hand
		// is cheaper than two calls. It also finds text that is ASCII,
		// which Next need not check for UTF-8.
		plain, high := true, byte(0)
		for _, c := range raw {
			if c == '&' || c == '\r' {
				plain = false
				break
			}
			high |= c
		}
		if plain {
			s.ascii = high < 0x80
			return raw, len(raw)
		}
	}
	if bytes.IndexByte(raw, '&') < 0 {
		return s.normalizeLines(raw), len(raw)
	}
	buf := s.scratch[:0]
	i := 0
	for i < len(raw) {
		switch c := raw[i]; c {
		case '&':
			var n int
			var stop bool
			buf, n, stop = s.reference(buf, raw[i:])
			if stop {
				s.scratch = buf
				return buf, i
			}
			i += n
		case '\r':
			buf = append(buf, '\n')
			i++
			if i < len(raw) && raw[i] == '\n' {
				i++
			}
		default:
			// Copy the run up to the next special byte in one go.
			j := i + 1
			for j < len(raw) && raw[j] != '&' && raw[j] != '\r' {
				j++
			}
			buf = append(buf, raw[i:j]...)
			i = j
		}
	}
	s.scratch = buf
	return buf, i
}

// decodeLiteral returns raw with line ends normalized and nothing else
// decoded, for markup kept as text.
func (s *Scanner) decodeLiteral(raw []byte) []byte { return s.normalizeLines(raw) }

// normalizeLines turns CR LF and lone CR into LF, as XML requires.
func (s *Scanner) normalizeLines(raw []byte) []byte {
	if bytes.IndexByte(raw, '\r') < 0 {
		return raw
	}
	buf := s.scratch[:0]
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '\r' {
			c = '\n'
			if i+1 < len(raw) && raw[i+1] == '\n' {
				i++
			}
		}
		buf = append(buf, c)
	}
	s.scratch = buf
	return buf
}

// reference decodes the entity or character reference at the start of raw
// (raw[0] is '&'), appends the result to buf and returns how many bytes of
// raw it consumed. It reports stop, consuming nothing, for a reference
// whose replacement is read as markup. A reference that cannot be decoded
// is kept as written.
func (s *Scanner) reference(buf, raw []byte) ([]byte, int, bool) {
	if len(raw) >= 2 && raw[1] == '#' {
		// A character by its number, which may be written with any
		// number of zeros in front: the digits run to the semicolon.
		i, base := 2, rune(10)
		if i < len(raw) && (raw[i] == 'x' || raw[i] == 'X') {
			base, i = 16, i+1
		}
		first := i
		var r rune
	digits:
		for ; i < len(raw); i++ {
			var d rune
			switch c := raw[i]; {
			case c >= '0' && c <= '9':
				d = rune(c - '0')
			case base == 16 && c >= 'a' && c <= 'f':
				d = rune(c-'a') + 10
			case base == 16 && c >= 'A' && c <= 'F':
				d = rune(c-'A') + 10
			default:
				break digits
			}
			if r = r*base + d; r > utf8.MaxRune {
				r = utf8.MaxRune + 1
			}
		}
		if i == first || i == len(raw) || raw[i] != ';' {
			s.Stats.Repairs++
			return append(buf, '&'), 1, false
		}
		if r == 0 || r > utf8.MaxRune || (r >= 0xD800 && r <= 0xDFFF) {
			// Not a character XML allows anywhere.
			s.Stats.Repairs++
			s.Stats.CharRefsReplaced++
			r = utf8.RuneError
		}
		// A reference to another control character gives the character;
		// the text conventions are applied where text is made.
		return utf8.AppendRune(buf, r), i + 1, false
	}
	name, n, ok := refName(raw)
	if !ok {
		s.Stats.Repairs++
		return append(buf, '&'), 1, false
	}
	if c := predefined(name); c != 0 {
		return append(buf, c), n, false
	}
	if s.resolve == nil {
		s.Stats.Repairs++
		return append(buf, raw[:n]...), n, false
	}
	// The reference is read by entityRef, as a token of its own.
	return buf, 0, true
}

// Skip reads past the end of the element whose Start token is current. It
// reports false if the input ended first.
func (s *Scanner) Skip() bool {
	depth := len(s.stack)
	for len(s.stack) >= depth {
		if s.Next() == EOF {
			return false
		}
	}
	return true
}

// AppendText reads to the end of the element whose Start token is current
// and appends all character data inside it, at any depth, to dst.
func (s *Scanner) AppendText(dst []byte) []byte {
	depth := len(s.stack)
	for len(s.stack) >= depth {
		switch s.Next() {
		case Text:
			dst = append(dst, s.text...)
		case EOF:
			return dst
		}
	}
	return dst
}
