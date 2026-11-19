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
	// ErrLimitExceeded: a limit stopped the extraction before there was
	// anything to return. That is always the case when the input exceeds
	// Limits.MaxInputBytes, and it is the case for any other limit that is
	// reached before the input could even be identified, such as a
	// decompression bomb in the part that says what the file is. A limit
	// reached once there is a document truncates it and adds
	// WarnLimitExceeded instead of failing.
	ErrLimitExceeded = errors.New("gofastcore: limit exceeded")
	// ErrNoPartsSelected: Options.Parts selects no part of the input,
	// because it lies beyond the last part or First is greater than Last.
	ErrNoPartsSelected = errors.New("gofastcore: part range selects no parts")
)

// Options are the settings common to all extractors. The zero value asks
// for text and structure only, for the whole input, using all CPUs.
//
// Settings that only one format understands (for PDF: memory mapping, a
// cache budget) do not belong here. They are fields on that library's
// extractor value, e.g. gofastpdf.Extractor{...}.Extract(...).
type Options struct {
	// Workers bounds the goroutines used inside a single document
	// (e.g. PDF pages). 0 means runtime.GOMAXPROCS(0). Pipelines that
	// already parallelize across documents should set 1.
	Workers int

	// Images selects how much image information to extract. The zero
	// value is ImagesMeta: every image is a block.
	Images ImageMode

	// Spans asks for inline formatting and links in Block.Spans. Spans
	// that mark hidden text are produced whether or not this is set.
	Spans bool

	// DeepHidden asks for the hidden-text checks that cost extra work,
	// typically those that need to know what is drawn behind or over the
	// text: HiddenLowContrast against a drawn background, HiddenOccluded,
	// HiddenClipped. The checks an extractor gets for free from text
	// state (invisible, tiny, off-page, transparent, invisible
	// characters) always run. Each extractor documents which is which.
	DeepHidden bool

	// Password is tried on encrypted input. It is also passed down to
	// embedded documents, which is what opens an encrypted attachment of
	// an unencrypted email.
	Password string

	// Parts restricts extraction to a range of part numbers. The zero
	// value extracts every part. A range that selects nothing is
	// ErrNoPartsSelected.
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
	// ImagesMeta, the default, emits a block for every image, with what
	// the source says of it: its place, its dimensions, its alternative
	// text, and an ID by which the same image is known wherever it
	// occurs. An image is content: where it stands gives context, and
	// its alternative text is text its author wrote.
	ImagesMeta ImageMode = iota
	// ImagesData additionally fills Image.Data with the encoded bytes,
	// for a caller that looks at the pictures.
	ImagesData
	// ImagesNone emits no image blocks: the alternative text of images
	// is then not in the document. WarnPartImageOnly is still reported.
	// For callers that want text only and know what they leave out.
	ImagesNone
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
	// MaxTextBytes bounds the text a document holds: the text of its
	// blocks and the cells of its tables, each counted as often as it
	// occurs, in the document and the documents embedded in it together.
	// It is the defense against a small file that says a long text many
	// times over (one string that every cell of a sheet refers to, a
	// form drawn on every page), which the decompression limit does not
	// see: what is repeated is stored once. Text outside blocks counts
	// too where the file decides both its length and how often it
	// occurs (outline titles, a name made for every part, list markers,
	// link targets), unless the extractor holds it to the length of a
	// name. When the limit is reached, the part being read is cut off
	// there with WarnLimitExceeded. The default is DefaultMaxTextBytes.
	// See TextBudget.
	MaxTextBytes int64
	// MaxParts bounds the number of parts extracted. When the input has
	// more, the first MaxParts parts of the requested range are returned
	// with WarnLimitExceeded, also where the count is known up front.
	MaxParts int
	// MaxDepth bounds how deeply embedded documents are followed. The
	// default is DefaultMaxDepth.
	MaxDepth int
}

// DefaultMaxDepth is how deeply embedded documents are followed when
// Limits.MaxDepth is zero: an attachment inside an archive inside an
// email is depth 2.
const DefaultMaxDepth = 4

// DefaultMaxTextBytes is the text a document and the documents embedded
// in it may hold when Limits.MaxTextBytes is zero. It is some ten times
// the text of a workbook of two million cells or of a book of a million
// and a half words.
const DefaultMaxTextBytes = 256 << 20

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
// was not extracted. The warning code says why:
//
//	WarnChildNotExtracted  no ChildFunc was supplied, or the depth limit was reached
//	WarnChildUnsupported   the ChildFunc returned ErrUnsupported
//	WarnChildEncrypted     the ChildFunc returned ErrPasswordRequired or ErrWrongPassword
//	WarnChildFailed        the ChildFunc returned any other error
//
// ExtractChild may be called on a nil *Options.
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
	if r == nil {
		// Listed only: the extractor has no content to hand over, as
		// when a limit stopped it first. The child extractor is never
		// called without something to read.
		return stub(SeverityWarning, WarnChildNotExtracted, "not read")
	}
	childOpts := *o
	childOpts.Depth++
	childOpts.Name = name
	childOpts.Parts = PartRange{}
	doc, err := o.Child(ctx, name, mime, r, size, &childOpts)
	switch {
	case err == nil:
	case errors.Is(err, ErrUnsupported):
		return stub(SeverityWarning, WarnChildUnsupported, err.Error())
	case errors.Is(err, ErrPasswordRequired), errors.Is(err, ErrWrongPassword):
		return stub(SeverityError, WarnChildEncrypted, err.Error())
	default:
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
