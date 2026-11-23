package gofastcore

import "slices"

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
//	KindFormula      the formula          -               Attrs["notation"]
//	KindNote         the note's text      -               Attrs: type, author, date, field
//	KindHeader       running header       -               -
//	KindFooter       running footer       -               -
//	KindThematicBreak empty               -               -
//
// A heading's Level is 1 to 6. A source with deeper levels (Word has nine)
// has them clamped to 6, with the real level, in decimal, in
// Attrs[AttrLevel]; the part then carries WarnHeadingLevelClamped.
//
// KindHeader and KindFooter are page furniture (repeated titles, page
// numbers). They are kept so nothing is silently lost, and tagged so
// consumers can drop them.
//
// KindThematicBreak marks a break between topics or scenes that the
// source declares (an HTML <hr>, a Markdown thematic break, Word's
// horizontal line, LibreOffice's Horizontal Line paragraph style). It is a
// boundary a chunker can split on. It has no text and is never Continued.
type Block struct {
	// Kind is what the block is; it decides which other fields mean
	// something (the table above).
	Kind Kind `json:"kind"`
	// Level is a heading's level, or the nesting depth of a list item or
	// quote; other kinds do not use it.
	Level uint8 `json:"level,omitempty"`
	// Text is the block's text, following the package's text conventions.
	// It is empty only for a table or a thematic break, and for an image
	// with no alternative text.
	Text string `json:"text,omitempty"`

	// Origin says whether the block's kind, level and boundaries are
	// declared by the source or were inferred by the extractor. It does
	// not cover the block's position in the reading order: a PDF image is
	// native (the file declares it and its box) even though where it falls
	// among the text is inferred. The zero value is OriginNative, so an
	// extractor that infers structure must set OriginInferred explicitly
	// on every such block.
	Origin Origin `json:"origin,omitempty"`

	// Display lists every reason the file declares why content in this
	// block is not shown or not inked; zero means it declares none, which
	// says nothing about whether a reader can see the text. A reason that
	// also appears on a span, or on a DisplayCells region of a table,
	// applies only there; any other reason applies to the whole block. See
	// DisplayWhole and DisplayedText. Such content is never dropped.
	Display Display `json:"display,omitempty"`

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
	// ranges the file declares not shown or not inked. Formatting and links
	// are optional and only produced when Options.Spans is set; spans with
	// Display set are always produced.
	Spans []Span `json:"spans,omitempty"`

	// Table is set exactly on a table block, Image exactly on an image
	// block.
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
	// AttrRole says what a block stands for when its kind alone does not
	// say: a table that is not a grid of cells the source shows as such,
	// the headers of a message, a block of a signature. One of the Role*
	// values.
	AttrRole = "role"
	// AttrChart is the type of a chart, lowercase as the source names it
	// ("bar", "line", "pie", "scatter").
	AttrChart = "chart"
	// AttrAxisX and AttrAxisY are the titles of a chart's axes.
	AttrAxisX = "x_axis"
	AttrAxisY = "y_axis"
	// AttrStored says what kind of stored, never-shown data a block is:
	// one of the Stored* values. It records where the text came from; it
	// sets no Display reason. Such a block is usually in the stored part,
	// but stored data that belongs to a page, sheet or slide (a PDF page's
	// scripts) stays in that part. The renderers leave out every block
	// marked stored, wherever it is, unless RenderOptions.Stored is set.
	AttrStored = "stored"
	// AttrNotation is the notation of a KindFormula block: one of the
	// Notation* values.
	AttrNotation = "notation"
	// AttrField is the name the source gives the form field or control
	// whose value the block is (a PDF form field, a Word form field or
	// content control).
	AttrField = "field"
)

// The values of Attrs[AttrStored], Attrs[AttrNoteType] and Attrs[AttrRole]
// are closed sets: the Stored*, Note* and Role* constants below. A library
// writes no other value, and Validate rejects one. A value a library needs
// is added here, by a change to this package; a fact that only one format
// has goes under a key of that library's own instead.

// Values of Attrs[AttrRole].
const (
	// RoleChart: the table is the data of a chart, as the chart's source
	// stores it. A chart's title is a KindCaption block before it.
	RoleChart = "chart"
	// RolePivot: the table is a pivot table.
	RolePivot = "pivot"
	// RoleHeaders: the block is the headers of a message that a mail
	// program shows (From, To, Date, Subject).
	RoleHeaders = "headers"
	// RoleSignature: the block is part of a message's signature, the text
	// after a line "-- ".
	RoleSignature = "signature"
)

var roleNames = []string{RoleChart, RolePivot, RoleHeaders, RoleSignature}

// Values of Attrs[AttrStored].
const (
	// StoredPivotCache: the records a pivot table was built from, kept in
	// the file where the source data is gone.
	StoredPivotCache = "pivot_cache"
	// StoredDefinedName: a name a workbook defines for a value, a formula
	// or a range of cells.
	StoredDefinedName = "defined_name"
	// StoredDocumentVariable: a variable a document stores for fields and
	// macros to use (Word's document variables).
	StoredDocumentVariable = "document_variable"
	// StoredCustomXML: data a program stored in the file as XML of its
	// own.
	StoredCustomXML = "custom_xml"
	// StoredFieldCode: the instruction of a field (a NoteField note, an
	// RTF index or table-of-contents entry). Every field note carries it.
	StoredFieldCode = "field_code"
	// StoredMacro: the source code of a macro or script, read as text and
	// never run.
	StoredMacro = "macro"
	// StoredMarkupComment: a comment in the file's markup (an HTML or XML
	// <!-- --> comment), which no reader displays. It stays where it
	// stands. A reviewer's comment, which is displayed, is a NoteComment
	// note and is not stored.
	StoredMarkupComment = "markup_comment"
	// StoredFormField: a field of a form that is shown on no page, such as
	// a PDF form field with no widget.
	StoredFormField = "form_field"
	// StoredAfterEnd: data that follows the logical end of the file (after
	// an RTF document's closing brace, after a PDF's last %%EOF, appended
	// to a zip archive). The part also carries WarnInputTrailingData.
	StoredAfterEnd = "after_end"
	// StoredManifestItem: a resource a publication lists in its manifest
	// and no chapter shows, such as an EPUB's image that no page uses.
	StoredManifestItem = "manifest_item"
	// StoredStylesheet: the text of a style sheet (an HTML <style>
	// element).
	StoredStylesheet = "stylesheet"
	// StoredGenerator: the name of the program that wrote the file, where
	// the file records it as data (RTF \generator).
	StoredGenerator = "generator"
	// StoredCustomProperty: a property of the document that its author or
	// a program defined, with its name in Attrs[AttrName] (RTF
	// \userprops).
	StoredCustomProperty = "custom_property"
)

var storedKindNames = []string{
	StoredPivotCache, StoredDefinedName, StoredDocumentVariable, StoredCustomXML,
	StoredFieldCode, StoredMacro, StoredMarkupComment, StoredFormField, StoredAfterEnd,
	StoredManifestItem, StoredStylesheet, StoredGenerator, StoredCustomProperty,
}

// StoredKindNames returns every value of Attrs[AttrStored], for a command
// that takes kinds by name (RenderOptions.StoredKinds). The slice is the
// caller's.
func StoredKindNames() []string { return slices.Clone(storedKindNames) }

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
	// ("HYPERLINK ...", "MERGEFIELD Name"). It follows its paragraph. The
	// instruction is never shown, but that is what a field is, not a
	// declaration: the note sets no Display reason. It is stored data:
	// every field note is marked Attrs[AttrStored] = StoredFieldCode, so
	// the renderers leave it out unless RenderOptions.Stored or
	// StoredKinds asks for it.
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
	// list; the note sets no Display reason.
	NoteChoices = "choices"
	// NoteAttribute is text a markup element keeps in an attribute rather
	// than in its content, and which a reader meets only through assistive
	// technology or by pointing at it: aria-label, title, placeholder, an
	// image map area's alt. It stands at the element's place, with the
	// attribute's name in Attrs["attribute"]. No Display reason.
	NoteAttribute = "attribute"
	// NoteHeaders: the headers of a message that a mail program does not
	// show, one "Name: value" per line. No Display reason: the format
	// declares none.
	NoteHeaders = "headers"
	// NoteMIMEPreamble and NoteMIMEEpilogue: the text of a multipart
	// message body before its first part and after its last, which no mail
	// program shows. No Display reason: the format declares none.
	NoteMIMEPreamble = "mime_preamble"
	NoteMIMEEpilogue = "mime_epilogue"
)

var noteTypeNames = []string{
	NoteComment, NoteDeletion, NoteSpeaker, NoteField, NoteChoices,
	NoteAttribute, NoteHeaders, NoteMIMEPreamble, NoteMIMEEpilogue,
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
	// plain-text formats (txt, md, html).
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
// link target, or the reasons the file declares that range not shown or
// not inked. Spans may overlap.
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
	// Display gives the reasons declared for this range when they apply to
	// only part of the block. Every reason here is also in Block.Display.
	Display Display `json:"display,omitempty"`
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
	// Display marks the cells the file declares not shown or not inked
	// when that is only part of the table: a hidden row or column. Every
	// reason here is also in Block.Display.
	Display []DisplayCells `json:"display,omitempty"`
	// Cells holds what some cells have besides the text they display, in
	// order of row, then column; a cell with nothing more has no entry.
	// Links come with Options.Spans; formulas, types and stored values
	// with Options.Cells. Everything here counts against
	// Limits.MaxTextBytes. The renderers write links, never formulas or
	// values: a formula can hold text that nobody sees on the sheet.
	Cells []CellDetail `json:"cells,omitempty"`
	// RowLocs says where each row came from, for a source that can say it: a
	// byte span, an anchor the source gives the row, a box on the page. It is
	// empty, or has one entry per row in the order of Rows; a row with
	// nothing to say has a zero Location. Block.Loc still covers the whole table.
	RowLocs []Location `json:"row_locs,omitempty"`
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
