// Package gofastcore defines the document model and extractor interface
// shared by every go-fast-* extraction library.
//
// Every extractor, whatever its input format, produces a *Document:
//
//	Document
//	├── Source      where the bytes came from and what format they were
//	├── Metadata    title, authors, dates, ...
//	├── Outline[]   the source's own table of contents, if it has one
//	├── Parts[]     the format's natural top-level unit: page, slide, sheet, ...
//	│   ├── Blocks[]    content in reading order: heading, paragraph, table, ...
//	│   └── Warnings[]  what went wrong in this part
//	├── Children[]  embedded documents: email attachments, archive members
//	└── Warnings[]  what went wrong for the document as a whole
//
// The model is deliberately flat inside a part. Section hierarchy is not
// stored; it is implied by heading levels, which lets extractors emit blocks
// in a single pass and lets page workers run independently.
//
// The shared model is the common denominator across formats. An extractor
// may offer a richer native API beside it (word boxes, fonts, glyph
// positions for PDF); those details do not belong here.
//
// # Contract for extractors
//
//   - All strings are valid UTF-8 and follow the text conventions below.
//   - A block is at least a paragraph-sized unit, never a fragment of a
//     line or word.
//   - Blocks appear in reading order. Where the format does not declare
//     order (PDF), order is the extractor's best inference. Block.Origin
//     does not cover order; it describes kind, level and boundaries only.
//   - A top-level document has at least one part. Formats with no natural
//     unit (docx, html, txt) use a single PartBody.
//   - Part numbers are the 1-based ordinal of the part in the source and
//     strictly increase. They need not be contiguous: extracting pages
//     40-60 yields parts numbered 40 to 60.
//   - A block that runs across a part boundary is either assigned to the
//     part where it starts or split, with Continued set on the later piece.
//   - Partial extraction returns a Document with warnings, not an error.
//     Errors are for input that yields nothing usable.
//   - Hidden text is extracted and flagged, never dropped; see Hidden.
//     Every extractor calls Part.MarkHidden on each finished part and
//     records in Document.HiddenChecked which reasons it fully checked.
//   - Lists that consumers iterate are never nil: Document.Parts,
//     Part.Blocks and Table.Rows. JSON then has [] where a list is empty,
//     never null, at no encoding cost. New and Part.MarkHidden take care
//     of the first two; a table has at least one row.
//   - Output is deterministic: the same input and options give the same
//     Document whatever Options.Workers is, including where a limit
//     truncates it.
//
// # Text conventions
//
//   - Text is in logical order, the order in which it is read and typed,
//     also for right-to-left scripts. An extractor that cannot guarantee
//     this for some input says so with a warning.
//   - Soft line breaks inside a paragraph are joined with a single space.
//     A "\n" in Text means a hard break.
//   - End-of-line hyphenation is resolved only when the extractor is
//     certain. Otherwise the hyphen stays and the lines are joined with
//     no space: "inter-" + "national" gives "inter-national", so that a
//     real compound such as "well-" + "known" comes out right.
//   - Presentation-form ligatures (U+FB00 to U+FB06) are expanded to
//     their letters.
//   - No string contains a control character (Unicode category Cc: NUL,
//     form feed, carriage return, ...) other than "\n" and "\t".
//   - Block text is not empty and has no leading or trailing whitespace.
//   - Code blocks are verbatim: the ligature and whitespace rules do not
//     apply to them.
//   - Unicode normalization form is not guaranteed.
//
// Document.Validate checks the mechanical parts of this contract and is
// intended to run in every extractor's test suite.
package gofastcore

import (
	"iter"
	"time"
)

// SchemaVersion identifies the shape of the serialized model. It changes
// when a field is removed or its meaning changes. Additions, including new
// block kinds, do not change it: decoders map names they do not know to
// the Unknown value of the enum.
const SchemaVersion = "0.1"

// Document is the output of every go-fast-* extractor.
type Document struct {
	SchemaVersion string        `json:"schema_version"`
	Source        Source        `json:"source"`
	Metadata      Metadata      `json:"metadata,omitzero"`
	Outline       []OutlineItem `json:"outline,omitempty"`
	// HiddenChecked is the audit record for hidden text: the reasons for
	// which this extraction ran its complete check. For a reason listed
	// here, a block without that flag was looked at and nothing was
	// found, as far as this extractor is able to tell (its documentation
	// states the limits). A reason not listed was not checked, or only in
	// part: it may still appear on a block where a cheaper check happened
	// to find it, but its absence says nothing. HiddenChars is always
	// listed, because Part.MarkHidden is mandatory.
	HiddenChecked Hidden      `json:"hidden_checked,omitempty"`
	Parts         []Part      `json:"parts"`
	Children      []*Document `json:"children,omitempty"`
	// Warnings holds problems that concern the document as a whole.
	// Problems local to a part are in Part.Warnings.
	Warnings []Warning `json:"warnings,omitempty"`
}

// New returns an empty Document for the given format with the schema
// version set. Its Parts list is empty but not nil, so that it is written
// to JSON as [] and never as null.
func New(format string) *Document {
	return &Document{SchemaVersion: SchemaVersion, Source: Source{Format: format}, Parts: []Part{}}
}

// Source describes the input the document was extracted from.
type Source struct {
	// Format is the short lowercase format name: "pdf", "docx", "eml", ...
	// It is empty only for a child that was listed but not extracted.
	Format string `json:"format,omitempty"`
	// Name is the file name, URL, or, for a child document, the attachment
	// or archive member name. Optional.
	Name string `json:"name,omitempty"`
	// MIME is the media type if known.
	MIME string `json:"mime,omitempty"`
	// Size is the input size in bytes, 0 if unknown.
	Size int64 `json:"size,omitempty"`
}

// Metadata holds document-level properties. Every field is optional.
type Metadata struct {
	Title string `json:"title,omitempty"`
	// Authors has one entry per author the source declares. A source with
	// a single free-text author field (PDF) yields one entry, verbatim;
	// extractors must not split a string on guesswork.
	Authors []string `json:"authors,omitempty"`
	Subject string   `json:"subject,omitempty"`
	// Keywords follows the same rule as Authors: a single free-text
	// keywords field yields one entry, verbatim.
	Keywords []string  `json:"keywords,omitempty"`
	Language string    `json:"language,omitempty"` // BCP 47, e.g. "en", "pt-BR"
	Created  time.Time `json:"created,omitzero"`
	Modified time.Time `json:"modified,omitzero"`
	// Application is the software that produced the file.
	Application string `json:"application,omitempty"`
	// Encrypted reports that the input was encrypted (and was decrypted).
	Encrypted bool `json:"encrypted,omitempty"`
	// Extra carries format-specific properties that have no field above,
	// e.g. email "from"/"to"/"message-id", PDF "producer". Keys are
	// lowercase.
	Extra map[string]string `json:"extra,omitempty"`
}

// OutlineItem is one entry of the source's own table of contents: a PDF
// bookmark, an epub nav point. It is structure the source declares, so it
// is reliable even where block structure is inferred.
type OutlineItem struct {
	Title string `json:"title"`
	// Level is the nesting depth, from 1.
	Level int `json:"level"`
	// Part is the Number of the part the entry points to, 0 if the target
	// is unknown. The part may be absent from Parts when only a range was
	// extracted.
	Part int `json:"part,omitempty"`
	// Y is the vertical position of the target within the part, in the
	// part's coordinate space, 0 if unknown.
	Y float64 `json:"y,omitempty"`
}

// Part is a format's natural top-level unit of content.
//
// A part is self-contained: a worker can fill one without touching any
// shared state, and a part handed on by itself carries its own warnings.
type Part struct {
	Kind PartKind `json:"kind"`
	// Number is the 1-based ordinal of this part in the source.
	Number int `json:"number"`
	// Name is what people call the part: the sheet name, slide title,
	// chapter title, or a PDF page label ("iv", "A-3") where it differs
	// from Number. Optional.
	Name string `json:"name,omitempty"`
	// Width and Height give the part's coordinate space for Block.Loc.BBox,
	// as the part is displayed. For PDF: the CropBox after /Rotate and
	// UserUnit are applied, in points. Zero when the part has no geometry.
	Width  float64 `json:"width,omitempty"`
	Height float64 `json:"height,omitempty"`
	// Hidden gives the reasons the whole part is not shown to a reader: a
	// hidden sheet or slide. Every block of the part carries the same
	// reasons (MarkHidden copies them).
	Hidden   Hidden    `json:"hidden,omitempty"`
	Blocks   []Block   `json:"blocks"`
	Warnings []Warning `json:"warnings,omitempty"`
}

// Warn appends a warning to the part.
func (p *Part) Warn(severity Severity, code, message string) {
	p.Warnings = append(p.Warnings, Warning{Code: code, Severity: severity, Message: message})
}

// Has reports whether the part carries a warning with the given code.
func (p *Part) Has(code string) bool {
	for i := range p.Warnings {
		if p.Warnings[i].Code == code {
			return true
		}
	}
	return false
}

// Warn appends a document-level warning. It is not safe for concurrent
// use; workers should warn on their own Part instead.
func (d *Document) Warn(severity Severity, code, message string) {
	d.Warnings = append(d.Warnings, Warning{Code: code, Severity: severity, Message: message})
}

// AllBlocks iterates over every block of the document in reading order,
// together with the part that holds it. Children are not visited.
func (d *Document) AllBlocks() iter.Seq2[*Part, *Block] {
	return func(yield func(*Part, *Block) bool) {
		for i := range d.Parts {
			p := &d.Parts[i]
			for j := range p.Blocks {
				if !yield(p, &p.Blocks[j]) {
					return
				}
			}
		}
	}
}

// AllWarnings iterates over the document-level warnings, for which the
// part is nil, and then over each part's warnings. Children are not
// visited.
func (d *Document) AllWarnings() iter.Seq2[*Part, Warning] {
	return func(yield func(*Part, Warning) bool) {
		for _, w := range d.Warnings {
			if !yield(nil, w) {
				return
			}
		}
		for i := range d.Parts {
			p := &d.Parts[i]
			for _, w := range p.Warnings {
				if !yield(p, w) {
					return
				}
			}
		}
	}
}

// Walk calls fn for d and then for every descendant in Children, depth
// first. It stops early if fn returns false.
func (d *Document) Walk(fn func(doc *Document, depth int) bool) {
	d.walk(fn, 0)
}

func (d *Document) walk(fn func(*Document, int) bool, depth int) bool {
	if !fn(d, depth) {
		return false
	}
	for _, c := range d.Children {
		if c != nil && !c.walk(fn, depth+1) {
			return false
		}
	}
	return true
}
