package gofastcore

import (
	"context"
	"errors"
	"io"
	"math"
)

// Extractor turns one or more input formats into the shared document
// model. Every go-fast-* library provides one, so that a dispatcher can
// treat all formats alike.
//
// Implementations must be safe for concurrent use: a pipeline calls
// Extract from many goroutines, one document each.
type Extractor interface {
	// Formats lists the format names this extractor handles, as they
	// appear in Source.Format: "pdf", or "docx", "pptx", "xlsx".
	Formats() []string

	// Extract reads size bytes from r and returns the document. io.ReaderAt
	// is used because the container formats (PDF, zip, OLE) need random
	// access; an *os.File, a bytes.Reader and a memory map all satisfy it.
	//
	// opts may be nil. A partially readable input yields a Document with
	// warnings. An error means nothing usable was extracted, and wraps one
	// of the Err* values where one applies. Extract must return promptly
	// with ctx.Err() once ctx is cancelled.
	Extract(ctx context.Context, r io.ReaderAt, size int64, opts *Options) (*Document, error)
}

// Errors common to all extractors. Extractors wrap them, so test with
// errors.Is.
var (
	// ErrPasswordRequired: the input is encrypted and no password was given.
	ErrPasswordRequired = errors.New("gofastcore: password required")
	// ErrWrongPassword: the input is encrypted and the password given does
	// not open it.
	ErrWrongPassword = errors.New("gofastcore: wrong password")
	// ErrUnsupported: the input is valid but uses a feature or variant the
	// extractor does not implement.
	ErrUnsupported = errors.New("gofastcore: unsupported input")
	// ErrMalformed: the input is not a readable file of the expected format.
	ErrMalformed = errors.New("gofastcore: malformed input")
	// ErrLimitExceeded: the input exceeds a limit that is checked before
	// extraction starts, such as Limits.MaxInputBytes. A limit reached
	// part-way does not produce this error; it truncates the document and
	// adds WarnLimitExceeded.
	ErrLimitExceeded = errors.New("gofastcore: limit exceeded")
)

// Options are the settings common to all extractors. The zero value asks
// for text and structure only, for the whole input, using all CPUs.
type Options struct {
	// Workers bounds the goroutines used inside a single document
	// (e.g. PDF pages). 0 means runtime.GOMAXPROCS(0). Pipelines that
	// already parallelize across documents should set 1.
	Workers int

	// Images selects how much image information to extract.
	Images ImageMode

	// Spans asks for inline formatting and links in Block.Spans.
	Spans bool

	// Password is tried on encrypted input.
	Password string

	// Parts restricts extraction to a range of part numbers. The zero
	// value extracts every part.
	Parts PartRange

	// Limits guards against hostile or oversized input.
	Limits Limits

	// Child extracts embedded documents (email attachments, archive
	// members, files embedded in a PDF). It is how a dispatcher that
	// knows every format is injected into an extractor that knows one,
	// without the extractors importing each other. When nil, embedded
	// documents are listed in Children but not extracted.
	//
	// Extractors do not call Child directly; they call ExtractChild.
	Child ChildFunc

	// Name is recorded as Source.Name.
	Name string

	// Depth is the nesting depth of the document being extracted: 0 for
	// a top-level document. ExtractChild maintains it; callers leave it
	// zero.
	Depth int
}

// ChildFunc extracts one embedded document. name is the attachment or
// member name and mime its declared media type; either may be empty. The
// function is expected to work out the format and route to the right
// extractor, passing opts through.
type ChildFunc func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, opts *Options) (*Document, error)

// ImageMode selects how much image information an extractor produces.
type ImageMode uint8

const (
	// ImagesNone emits no image blocks. Fastest. WarnPartImageOnly is
	// still reported.
	ImagesNone ImageMode = iota
	// ImagesMeta emits image blocks with position and dimensions.
	ImagesMeta
	// ImagesData additionally fills Image.Data with the encoded bytes.
	ImagesData
)

// PartRange selects parts by number, both ends inclusive. A zero First
// means from the first part; a zero Last means through the last.
type PartRange struct {
	First int
	Last  int
}

// Contains reports whether part number n is inside the range.
func (r PartRange) Contains(n int) bool {
	return n >= r.First && (r.Last == 0 || n <= r.Last)
}

// Limits bounds the work an extractor will do for one input. For every
// field, zero means the extractor's default and a negative value means
// no limit.
type Limits struct {
	// MaxInputBytes bounds the input size. Exceeding it is
	// ErrLimitExceeded before any work is done.
	MaxInputBytes int64
	// MaxDecodedBytes bounds the total bytes produced by decompression
	// (zip members, PDF streams), the defense against decompression bombs.
	MaxDecodedBytes int64
	// MaxParts bounds the number of parts extracted.
	MaxParts int
	// MaxDepth bounds how deeply embedded documents are followed. The
	// default is DefaultMaxDepth.
	MaxDepth int
}

// DefaultMaxDepth is how deeply embedded documents are followed when
// Limits.MaxDepth is zero: an attachment inside an archive inside an
// email is depth 2.
const DefaultMaxDepth = 4

// ResolveLimit applies the Limits convention to one value: it returns def
// when v is zero, the largest int64 when v is negative, and v otherwise.
func ResolveLimit(v, def int64) int64 {
	switch {
	case v == 0:
		return def
	case v < 0:
		return math.MaxInt64
	}
	return v
}

// ExtractChild extracts one embedded document on behalf of an extractor
// and always returns a Document to append to Children, so that every
// embedded file is at least listed with its name and size.
//
// The returned document has no parts and a single warning when the child
// was not extracted: WarnChildNotExtracted if no ChildFunc was supplied or
// the depth limit was reached, WarnChildFailed if the ChildFunc returned
// an error. ExtractChild may be called on a nil *Options.
func (o *Options) ExtractChild(ctx context.Context, name, mime string, r io.ReaderAt, size int64) *Document {
	stub := func(severity Severity, code, message string) *Document {
		d := New("")
		d.Source.Name, d.Source.MIME, d.Source.Size = name, mime, size
		d.Warn(severity, code, message)
		return d
	}
	if o == nil || o.Child == nil {
		return stub(SeverityInfo, WarnChildNotExtracted, "no child extractor supplied")
	}
	if int64(o.Depth) >= ResolveLimit(int64(o.Limits.MaxDepth), DefaultMaxDepth) {
		return stub(SeverityWarning, WarnChildNotExtracted, "depth limit reached")
	}
	childOpts := *o
	childOpts.Depth++
	childOpts.Name = name
	childOpts.Parts = PartRange{}
	doc, err := o.Child(ctx, name, mime, r, size, &childOpts)
	if err != nil {
		return stub(SeverityError, WarnChildFailed, err.Error())
	}
	if doc == nil {
		return stub(SeverityError, WarnChildFailed, "child extractor returned no document")
	}
	if doc.Source.Name == "" {
		doc.Source.Name = name
	}
	if doc.Source.MIME == "" {
		doc.Source.MIME = mime
	}
	if doc.Source.Size == 0 {
		doc.Source.Size = size
	}
	return doc
}
