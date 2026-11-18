package gofastcore

// Block is one unit of content in reading order.
//
// Which fields are meaningful depends on Kind:
//
//	Kind             Text                 Level           Other
//	KindHeading      heading text         1-6             -
//	KindParagraph    paragraph text       -               -
//	KindListItem     item text, no marker nesting, from 1 Ordered, Marker
//	KindTable        empty                -               Table
//	KindImage        alt text / caption   -               Image
//	KindCode         code, verbatim       -               Attrs["language"]
//	KindQuote        quoted text          nesting, from 1 -
//	KindCaption      caption text         -               -
//	KindFootnote     note text            -               Marker
//	KindFormula      LaTeX or plain text  -               -
//	KindNote         speaker note/comment -               -
//	KindHeader       running header       -               -
//	KindFooter       running footer       -               -
//
// KindHeader and KindFooter are page furniture (repeated titles, page
// numbers). They are kept so nothing is silently lost, and tagged so
// consumers can drop them.
type Block struct {
	Kind  Kind   `json:"kind"`
	Level uint8  `json:"level,omitempty"`
	Text  string `json:"text,omitempty"`

	// Origin says whether the block's kind, level and boundaries are
	// declared by the source or were inferred by the extractor. It does
	// not cover the block's position in the reading order: a PDF image is
	// native (the file declares it and its box) even though where it falls
	// among the text is inferred. The zero value is OriginNative, so an
	// extractor that infers structure must set OriginInferred explicitly
	// on every such block.
	Origin Origin `json:"origin,omitempty"`

	// Hidden lists every reason why text in this block is not visible to
	// a reader of the source; zero means none was found. A reason that
	// also appears on a span, or on a HiddenCells region of a table,
	// applies only there; any other reason applies to the whole block. See
	// HiddenWhole and VisibleText. Hidden text is never dropped: it is
	// extracted and flagged.
	Hidden Hidden `json:"hidden,omitempty"`

	// Continued marks a block that continues the last block of the same
	// kind in the preceding part, e.g. the second half of a paragraph
	// split by a page break. Optional: an extractor that cannot tell
	// leaves it false.
	Continued bool `json:"continued,omitempty"`

	// Ordered and Marker apply to list items. Marker is the literal label
	// as it appeared ("3.", "(a)", "4.2.1") when the source provides it.
	Ordered bool   `json:"ordered,omitempty"`
	Marker  string `json:"marker,omitempty"`

	// Spans carries inline formatting and links over Text, and marks the
	// ranges that are hidden. Formatting and links are optional and only
	// produced when Options.Spans is set; spans with Hidden set are always
	// produced.
	Spans []Span `json:"spans,omitempty"`

	Table *Table `json:"table,omitempty"`
	Image *Image `json:"image,omitempty"`

	// Loc says where in the source the block came from.
	Loc Location `json:"loc,omitzero"`

	// Attrs is the escape hatch for format-specific facts. Keys are
	// lowercase. Prefer a real field once two formats need the same key.
	Attrs map[string]string `json:"attrs,omitempty"`
}

// Location pins a block to its place in the source so that a retrieval hit
// can be cited and shown in context. Every field is optional; the part a
// block belongs to is given by its position in Document.Parts.
type Location struct {
	// BBox is the block's bounding box in the coordinate space of its
	// part as displayed: origin top-left, y increasing downward, units as
	// in Part.Width/Height. A box drawn at these coordinates on a rendered
	// image of the part encloses the block.
	BBox Rect `json:"bbox,omitzero"`
	// Range is a cell range in A1 notation for blocks from a sheet,
	// e.g. "B2:F40".
	Range string `json:"range,omitempty"`
	// Offset and Length give the block's byte span in the source for
	// plain-text formats (txt, md, csv, html).
	Offset int64 `json:"offset,omitempty"`
	Length int64 `json:"length,omitempty"`
}

// Rect is an axis-aligned rectangle with (X0,Y0) top-left and (X1,Y1)
// bottom-right.
type Rect struct {
	X0 float64 `json:"x0"`
	Y0 float64 `json:"y0"`
	X1 float64 `json:"x1"`
	Y1 float64 `json:"y1"`
}

// Span marks the range [Start, End) of Block.Text with inline styling, a
// link target, or the reasons that range is hidden. Spans may overlap.
//
// Start and End are byte offsets into the UTF-8 text, not character
// offsets, and always fall on character boundaries. A consumer in a
// language that indexes strings by code point must convert; in Python:
//
//	text.encode("utf-8")[start:end].decode("utf-8")
type Span struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Style Style  `json:"style,omitempty"`
	Href  string `json:"href,omitempty"`
	// Hidden gives the reasons this range is not visible when only part
	// of the block is hidden. Every reason here is also in Block.Hidden.
	Hidden Hidden `json:"hidden,omitempty"`
}

// Table is a rectangular grid of cell text. Every row has the same number
// of columns; a merged region stores its text in its top-left cell, leaves
// the other covered cells empty, and is listed in Merges.
//
// Extractors should split very large sheets into several table blocks
// (each with its own Loc.Range) rather than emit one giant table.
type Table struct {
	// HeaderRows is the number of leading rows that are column headers.
	HeaderRows int        `json:"header_rows,omitempty"`
	Rows       [][]string `json:"rows"`
	Merges     []Merge    `json:"merges,omitempty"`
	// Hidden marks the cells that are hidden when only part of the table
	// is: a hidden row or column, a cell with white text. Every reason
	// here is also in Block.Hidden.
	Hidden []HiddenCells `json:"hidden,omitempty"`
}

// Merge is a merged cell region. Row and Col are 0-based; RowSpan and
// ColSpan are at least 1 and at least one of them is greater than 1.
type Merge struct {
	Row     int `json:"row"`
	Col     int `json:"col"`
	RowSpan int `json:"row_span"`
	ColSpan int `json:"col_span"`
}

// Image describes an image referenced by a KindImage block.
type Image struct {
	// ID identifies the image resource within the document. Equal IDs
	// mean equal bytes, and several blocks may share one ID (a logo on
	// every page). It is not a per-block identifier.
	ID   string `json:"id,omitempty"`
	MIME string `json:"mime,omitempty"`
	// Width and Height are in pixels, 0 if unknown.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// Data holds the encoded image bytes when the extractor was asked for
	// them. Serialized as base64. When several blocks share an ID, Data is
	// guaranteed only on the first of them in document order; later ones
	// may leave it nil.
	Data []byte `json:"data,omitempty"`
}
