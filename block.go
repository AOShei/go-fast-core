package gofastcore

// Block is one unit of content in reading order.
//
// Which fields are meaningful depends on Kind:
//
//	Kind             Text                 Level           Other
//	KindHeading      heading text         1-6             Marker
//	KindParagraph    paragraph text       -               -
//	KindListItem     item text, no marker nesting, from 1 Ordered, Marker
//	KindTable        empty                -               Table
//	KindImage        alt text / caption   -               Image
//	KindCode         code, verbatim       -               Attrs["language"]
//	KindQuote        quoted text          nesting, from 1 -
//	KindCaption      caption text         -               -
//	KindFootnote     note text            -               Marker
//	KindFormula      LaTeX or plain text  -               -
//	KindNote         note, comment, list  -               Attrs: type, author, date
//	KindHeader       running header       -               -
//	KindFooter       running footer       -               -
//
// A heading's Level is 1 to 6. A source with deeper levels (Word has nine)
// has them clamped to 6, with the real level, in decimal, in
// Attrs[AttrLevel]; the part then carries WarnHeadingLevelClamped.
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

	// Continued marks a block that continues the nearest block of the
	// same kind before it: the last one of the preceding part, as with
	// the second half of a paragraph split by a page break, or the one
	// before it in the same part, as with a table too long for one block
	// and split by rows. A continued table has the columns of the table
	// it continues; its HeaderRows count only header rows it repeats.
	// Optional: an extractor that cannot tell leaves it false.
	Continued bool `json:"continued,omitempty"`

	// Ordered applies to list items.
	//
	// Marker is the label a reader sees in front of the block, as it
	// appeared, when the source provides or defines one: the label of a
	// list item ("3.", "(a)", "4.2.1"), the number of a numbered heading
	// ("2.3"), the mark of a footnote ("1", "*"). It is content: a
	// contract refers to "clause 4.2". It is kept out of Text, and the
	// renderers put it back in front.
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
	// The keys every format spells the same way are the Attr* constants.
	Attrs map[string]string `json:"attrs,omitempty"`
}

// Keys of Block.Attrs that mean the same in every format.
const (
	// AttrNoteType says what a KindNote block is: one of the Note*
	// values.
	AttrNoteType = "type"
	// AttrAuthor is who wrote a note or made a tracked change, as the
	// source names them.
	AttrAuthor = "author"
	// AttrDate is when a note was written or a change made, as the
	// source writes it, normally ISO 8601.
	AttrDate = "date"
	// AttrLevel is the real level of a heading deeper than six, in
	// decimal ("8").
	AttrLevel = "level"
	// AttrLanguage is the programming language of a KindCode block.
	AttrLanguage = "language"
	// AttrName is the name the source gives a table, a pivot table or a
	// chart ("SalesTable").
	AttrName = "name"
	// AttrRole says what a table block stands for when it is not a grid
	// of cells the source shows as such: one of the Role* values.
	AttrRole = "role"
	// AttrChart is the type of a chart, lowercase as the source names it
	// ("bar", "line", "pie", "scatter").
	AttrChart = "chart"
	// AttrAxisX and AttrAxisY are the titles of a chart's axes.
	AttrAxisX = "x_axis"
	AttrAxisY = "y_axis"
	// AttrStored says what kind of stored, never-shown data a block is:
	// one of the Stored* values. Such a block is flagged HiddenInvisible.
	AttrStored = "stored"
	// AttrNotation is the notation of a KindFormula block: one of the
	// Notation* values.
	AttrNotation = "notation"
	// AttrField is the name the source gives the form field or control
	// whose value the block is (a PDF form field, a Word form field or
	// content control).
	AttrField = "field"
)

// Values of Attrs[AttrRole].
const (
	// RoleChart: the table is the data of a chart, as the chart's source
	// stores it. A chart's title is a KindCaption block before it.
	RoleChart = "chart"
	// RolePivot: the table is a pivot table.
	RolePivot = "pivot"
)

// Values of Attrs[AttrStored].
const (
	StoredPivotCache       = "pivot_cache"
	StoredDefinedName      = "defined_name"
	StoredDocumentVariable = "document_variable"
	StoredCustomXML        = "custom_xml"
	StoredFieldCode        = "field_code"
	StoredMacro            = "macro"
	// StoredFormField: a field of a form that is shown on no page, such as
	// a PDF form field with no widget.
	StoredFormField = "form_field"
)

// Values of Attrs[AttrNotation].
const (
	NotationLaTeX       = "latex"
	NotationUnicodeMath = "unicodemath" // the linear form Word uses
	NotationPlain       = "plain"
)

// Values of Attrs[AttrNoteType].
const (
	// NoteComment is a reviewer's comment or annotation attached to the
	// content.
	NoteComment = "comment"
	// NoteDeletion is text removed by a tracked change. The document's
	// text is given as if the change were accepted; the removed text is
	// kept here.
	NoteDeletion = "deletion"
	// NoteSpeaker is the speaker's notes of a slide.
	NoteSpeaker = "speaker"
	// NoteField is the instruction of a field whose result is in the text
	// ("HYPERLINK ...", "MERGEFIELD Name"). It follows its paragraph and is
	// flagged HiddenInvisible: the instruction is never shown.
	NoteField = "field"
	// NoteChoices is the list of values a control offers to choose from:
	// the drop-down of a spreadsheet cell (a data validation list), a
	// drop-down form field or content control, the options of a PDF choice
	// field. Its Text is the values, one per line, in the source's order.
	// A list the source takes from cells elsewhere is given as the
	// reference to them ("=Lists!$A$1:$A$12"), since the values are
	// already in the text where those cells are. It follows the block of
	// the control, or for a sheet stands at the place of the cells it is
	// for (Loc.Range), with the control's name in Attrs[AttrField] where
	// the source names it. The values are shown to a reader who opens the
	// list, so the block is not flagged hidden for that alone.
	NoteChoices = "choices"
)

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
	// Anchor is an identifier the source itself gives the block, stable
	// for as long as the source is: a Word paragraph id, an HTML id
	// attribute, the fragment of an e-book link. It is what a citation
	// points at in a format that has no pages and no byte offsets.
	Anchor string `json:"anchor,omitempty"`
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
	// Cells holds what some cells have besides the text they display, in
	// order of row, then column; a cell with nothing more has no entry.
	// Links come with Options.Spans; formulas, types and stored values
	// with Options.Cells. Everything here counts against
	// Limits.MaxTextBytes. The renderers write links, never formulas or
	// values: a formula can hold text that nobody sees on the sheet.
	Cells []CellDetail `json:"cells,omitempty"`
}

// CellDetail is what one cell of a table holds besides its displayed
// text.
type CellDetail struct {
	Row int `json:"row"`
	Col int `json:"col"`
	// Href is where the cell links to: an address, or "#place" inside the
	// document.
	Href string `json:"href,omitempty"`
	// Formula is the formula whose result the cell displays, as the
	// source writes it, without a leading "=". It is never evaluated.
	Formula string `json:"formula,omitempty"`
	// Type is the kind of value the cell stores. Value is that value
	// where the displayed text does not show it in full: a number its
	// format rounds, a date's serial number. Value is set only with Type.
	Type  CellType `json:"type,omitempty"`
	Value string   `json:"value,omitempty"`
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
