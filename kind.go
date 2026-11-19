package gofastcore

import (
	"fmt"
	"strings"
)

// The enums in this file are one byte in memory and lowercase strings in
// JSON. Each has an Unknown value: decoding a name this version does not
// know yields Unknown instead of an error, so that a consumer built
// against an older core can still read documents from a newer extractor.
// Extractors never produce Unknown, and Validate rejects it.

// Kind is the type of a Block.
type Kind uint8

const (
	// KindInvalid is the zero value: a block whose kind was never set.
	KindInvalid Kind = iota
	// KindUnknown is a kind this version of the package does not know.
	// Consumers should treat it as a paragraph.
	KindUnknown
	KindParagraph
	KindHeading
	KindListItem
	KindTable
	KindImage
	KindCode
	KindQuote
	KindCaption
	KindFootnote
	KindFormula
	KindNote
	KindHeader
	KindFooter
	// KindThematicBreak is a break between topics or scenes that the
	// source marks as such: an HTML <hr>, a Markdown thematic break.
	// It has no text. Page, column and section breaks are layout and are
	// not emitted as blocks.
	KindThematicBreak
	kindEnd
)

var kindNames = [...]string{
	KindInvalid:   "invalid",
	KindUnknown:   "unknown",
	KindParagraph: "paragraph",
	KindHeading:   "heading",
	KindListItem:  "list_item",
	KindTable:     "table",
	KindImage:     "image",
	KindCode:      "code",
	KindQuote:     "quote",
	KindCaption:   "caption",
	KindFootnote:  "footnote",
	KindFormula:   "formula",
	KindNote:      "note",
	KindHeader:    "header",
	KindFooter:    "footer",

	KindThematicBreak: "thematic_break",
}

// Valid reports whether k is a kind an extractor may emit.
func (k Kind) Valid() bool { return k > KindUnknown && k < kindEnd }

// Furniture reports whether k is page furniture rather than content.
func (k Kind) Furniture() bool { return k == KindHeader || k == KindFooter }

func (k Kind) String() string {
	if k < kindEnd {
		return kindNames[k]
	}
	return fmt.Sprintf("kind(%d)", uint8(k))
}

func (k Kind) MarshalText() ([]byte, error) {
	if k == KindInvalid || k >= kindEnd {
		return nil, fmt.Errorf("gofastcore: cannot marshal block kind %d", uint8(k))
	}
	return []byte(kindNames[k]), nil
}

func (k *Kind) UnmarshalText(b []byte) error {
	*k = KindUnknown
	for i := KindParagraph; i < kindEnd; i++ {
		if kindNames[i] == string(b) {
			*k = i
			break
		}
	}
	return nil
}

// PartKind is the type of a Part.
type PartKind uint8

const (
	PartInvalid PartKind = iota
	// PartUnknown is a part kind this version of the package does not know.
	PartUnknown
	// PartBody is the single part of a format with no natural division:
	// docx, odt, html, txt, md, rtf.
	PartBody
	PartPage    // pdf
	PartSlide   // pptx, odp, ppt
	PartSheet   // xlsx, ods, xls, csv
	PartChapter // epub
	PartMessage // eml, msg; one per message in an mbox
	// PartStored holds what belongs to the file as a whole and to no
	// page, sheet or slide, and is shown on none: macro code, document
	// variables, custom XML data, defined names. A document has at most
	// one, after its other parts and numbered after them; its Hidden is
	// HiddenInvisible, so every block in it is flagged. Options.Parts
	// selects it only with the whole document (the zero range); a reader
	// that gives parts one at a time gives it last, after the others.
	PartStored
	partEnd
)

var partNames = [...]string{
	PartInvalid: "invalid",
	PartUnknown: "unknown",
	PartBody:    "body",
	PartPage:    "page",
	PartSlide:   "slide",
	PartSheet:   "sheet",
	PartChapter: "chapter",
	PartMessage: "message",
	PartStored:  "stored",
}

// Valid reports whether k is a part kind an extractor may emit.
func (k PartKind) Valid() bool { return k > PartUnknown && k < partEnd }

func (k PartKind) String() string {
	if k < partEnd {
		return partNames[k]
	}
	return fmt.Sprintf("part(%d)", uint8(k))
}

func (k PartKind) MarshalText() ([]byte, error) {
	if k == PartInvalid || k >= partEnd {
		return nil, fmt.Errorf("gofastcore: cannot marshal part kind %d", uint8(k))
	}
	return []byte(partNames[k]), nil
}

func (k *PartKind) UnmarshalText(b []byte) error {
	*k = PartUnknown
	for i := PartBody; i < partEnd; i++ {
		if partNames[i] == string(b) {
			*k = i
			break
		}
	}
	return nil
}

// Origin says how a block's structure (its kind, level and boundaries)
// was determined. It says nothing about where the text came from.
type Origin uint8

const (
	// OriginNative: the source declares the structure. A docx style, an
	// HTML tag, a tagged-PDF structure element, a PDF outline entry.
	OriginNative Origin = 0
	// OriginInferred: the extractor deduced the structure from geometry,
	// fonts or other heuristics. A consumer that needs certainty should
	// treat such blocks as plain text and chunk by size.
	OriginInferred Origin = 1
	// OriginUnknown is an origin this version of the package does not know.
	OriginUnknown Origin = 255
)

// Valid reports whether o is an origin an extractor may emit.
func (o Origin) Valid() bool { return o == OriginNative || o == OriginInferred }

func (o Origin) String() string {
	switch o {
	case OriginNative:
		return "native"
	case OriginInferred:
		return "inferred"
	}
	return "unknown"
}

func (o Origin) MarshalText() ([]byte, error) { return []byte(o.String()), nil }

func (o *Origin) UnmarshalText(b []byte) error {
	switch string(b) {
	case "native", "":
		*o = OriginNative
	case "inferred":
		*o = OriginInferred
	default:
		*o = OriginUnknown
	}
	return nil
}

// Severity says how a warning affects the output. Values are ordered, so
// "w.Severity >= SeverityError" is meaningful; an unknown severity sorts
// highest.
type Severity uint8

const (
	// SeverityInvalid is the zero value: a severity that was never set.
	SeverityInvalid Severity = 0
	// SeverityInfo: something notable happened; the output is complete.
	SeverityInfo Severity = 1
	// SeverityWarning: the output is present but degraded or may not
	// represent what a reader of the source would see.
	SeverityWarning Severity = 2
	// SeverityError: content is missing or untrustworthy.
	SeverityError Severity = 3
	// SeverityUnknown is a severity this version of the package does not
	// know.
	SeverityUnknown Severity = 255
)

// Valid reports whether s is a severity an extractor may emit.
func (s Severity) Valid() bool { return s >= SeverityInfo && s <= SeverityError }

func (s Severity) String() string {
	switch s {
	case SeverityInvalid:
		return "invalid"
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	}
	return "unknown"
}

func (s Severity) MarshalText() ([]byte, error) {
	if s == SeverityInvalid {
		return nil, fmt.Errorf("gofastcore: cannot marshal unset severity")
	}
	return []byte(s.String()), nil
}

func (s *Severity) UnmarshalText(b []byte) error {
	switch string(b) {
	case "info":
		*s = SeverityInfo
	case "warning":
		*s = SeverityWarning
	case "error":
		*s = SeverityError
	default:
		*s = SeverityUnknown
	}
	return nil
}

// Style is a set of inline text styles. In JSON it is a "+"-joined list
// of names, e.g. "bold+italic". Names this version does not know are
// ignored on decode.
type Style uint8

const (
	StyleBold Style = 1 << iota
	StyleItalic
	StyleUnderline
	StyleStrike
	StyleCode
	StyleSub
	StyleSuper
)

var styleNames = [...]string{"bold", "italic", "underline", "strike", "code", "sub", "super"}

func (s Style) String() string {
	var parts []string
	for i, n := range styleNames {
		if s&(1<<i) != 0 {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, "+")
}

func (s Style) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

func (s *Style) UnmarshalText(b []byte) error {
	*s = 0
	if len(b) == 0 {
		return nil
	}
	for name := range strings.SplitSeq(string(b), "+") {
		for i, n := range styleNames {
			if n == name {
				*s |= 1 << i
				break
			}
		}
	}
	return nil
}

// CellType is the kind of value a table cell stores (CellDetail.Type).
type CellType uint8

const (
	// CellUnset: the type is not given.
	CellUnset CellType = iota
	// CellUnknown is a cell type this version of the package does not
	// know.
	CellUnknown
	CellNumber
	CellDate
	CellBool
	CellError
	CellText
	cellEnd
)

var cellNames = [...]string{
	CellUnset:   "",
	CellUnknown: "unknown",
	CellNumber:  "number",
	CellDate:    "date",
	CellBool:    "bool",
	CellError:   "error",
	CellText:    "text",
}

// Valid reports whether t is a cell type an extractor may emit.
func (t CellType) Valid() bool { return t > CellUnknown && t < cellEnd }

func (t CellType) String() string {
	if t < cellEnd {
		return cellNames[t]
	}
	return fmt.Sprintf("cell(%d)", uint8(t))
}

func (t CellType) MarshalText() ([]byte, error) {
	if t == CellUnset || t >= cellEnd {
		return nil, fmt.Errorf("gofastcore: cannot marshal cell type %d", uint8(t))
	}
	return []byte(cellNames[t]), nil
}

func (t *CellType) UnmarshalText(b []byte) error {
	*t = CellUnknown
	for i := CellNumber; i < cellEnd; i++ {
		if cellNames[i] == string(b) {
			*t = i
			break
		}
	}
	return nil
}
