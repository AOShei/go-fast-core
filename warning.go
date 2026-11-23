package gofastcore

// Warning records a non-fatal problem encountered during extraction.
//
// A warning states what happened. It never recommends what to do next:
// the go-fast libraries do no OCR and call no external service, so a code
// says "this part has images and no text", not "this part needs OCR".
// The caller decides what follows from the fact.
type Warning struct {
	// Code is a stable machine-readable identifier. The codes shared by
	// all extractors are the Warn* constants; an extractor may add its own
	// under a format prefix, e.g. "pdf.font_missing".
	Code string `json:"code"`
	// Severity says how the problem affects the output.
	Severity Severity `json:"severity"`
	// Message is free text for people. Programs should switch on Code.
	Message string `json:"message,omitempty"`
}

// Warning codes shared by all extractors. Each describes an observed fact
// about the input or the output.
const (
	// WarnPartFailed: the part could not be parsed. Its blocks are missing
	// or incomplete. Severity error.
	WarnPartFailed = "part.failed"

	// WarnPartNoText: the part was parsed and contains no text and no
	// images, e.g. a blank page or a page of vector drawing only.
	WarnPartNoText = "part.no_text"

	// WarnPartImageOnly: the part was parsed and contains no text, but
	// does contain image content. This is what a scanned page looks like.
	// It is reported whatever Options.Images is set to.
	WarnPartImageOnly = "part.image_only"

	// WarnTextUnmappedGlyphs: some text has no Unicode mapping in the
	// source and appears as U+FFFD or is absent.
	WarnTextUnmappedGlyphs = "text.unmapped_glyphs"

	// WarnTextControlCharactersRemoved: control characters (Unicode Cc
	// other than tab and line feed: NUL, form feed, carriage return, the
	// C1 controls, ...) were removed from extracted text, as the text
	// conventions require; they have no meaning as text. The message
	// says how many. Severity info. Every extractor that removes any
	// raises it, once per part or document with the count.
	WarnTextControlCharactersRemoved = "text.control_characters_removed"
	// WarnTextEncodingDetected: the input declares no character encoding
	// and is not valid UTF-8; it was read in the encoding its bytes were
	// recognised as, which the message names, with how to give one
	// instead. Severity warning: the encoding is not declared, though an
	// extractor uses one only where it is certain and refuses the input
	// otherwise.
	WarnTextEncodingDetected = "text.encoding_detected"
	// WarnTextTrackedChanges: the part has tracked changes. Its text is
	// given as if all were accepted; each deletion is kept as a note block
	// of type NoteDeletion.
	WarnTextTrackedChanges = "text.tracked_changes"

	// WarnHeadingLevelClamped: the part has headings deeper than six
	// levels. Each has Level 6 and its real level in Attrs[AttrLevel].
	// Severity info: nothing is lost.
	WarnHeadingLevelClamped = "structure.heading_level_clamped"

	// WarnImageUnsupportedEncoding: image data was requested
	// (ImagesData) and an image uses an encoding the extractor cannot
	// deliver; its block is present without data.
	WarnImageUnsupportedEncoding = "image.unsupported_encoding"

	// WarnInputRepaired: the input was structurally damaged and was read
	// by a recovery path. Severity info if nothing is known to be lost.
	WarnInputRepaired = "input.repaired"

	// WarnInputUnreadContent: the input holds content of a kind the
	// extractor does not read (an equation, a sound or video clip, a
	// plug-in, a dialog sheet); the message names the kind and how many.
	// It is not in the output. Severity warning.
	WarnInputUnreadContent = "input.unread_content"
	// WarnInputStreamDamaged: a stream or member of the file other than
	// its main content (styles, numbering, notes, document properties, a
	// manifest, a VBA project, an embedded object) could not be read to
	// its end; what was read before the damage is used. The message names
	// it. "Stream" is a piece of the file, not a Part of the document.
	// Severity warning.
	WarnInputStreamDamaged = "input.stream_damaged"
	// WarnInputDecrypted: the input was encrypted and was decrypted, and
	// Metadata.Encrypted is set. The message names the scheme, and which
	// password (the caller's, or one the format defines for files meant to
	// open without one) or certificate opened it. Severity info.
	WarnInputDecrypted = "input.decrypted"
	// WarnInputActiveContent: the input contains something meant to be
	// run, or an instruction to run something: macros, scripts, an action
	// that starts a program or opens a file (a PDF Launch action). It is
	// not read and never run. An instruction that names a file or address
	// outside the input is also WarnInputExternalReference; the two are
	// separate facts.
	WarnInputActiveContent = "input.active_content"

	// WarnInputExternalReference: the input refers to a file or address
	// outside itself other than by an ordinary hyperlink: a remote
	// template, a linked picture, a linked object, an action that sends
	// form data to or loads data from an address. Nothing is fetched or
	// sent.
	WarnInputExternalReference = "input.external_reference"

	// WarnInputAmbiguous: the input can be read as two different
	// documents, depending on the program: an archive that names a member
	// twice in two ways, or has two members with one name. Which reading
	// this extractor takes is in its documentation.
	WarnInputAmbiguous = "input.ambiguous"

	// WarnInputTrailingData: data follows the logical end of the file
	// (after an RTF document's closing brace, after a PDF's last %%EOF,
	// appended to a zip archive). Its text is in the stored part with
	// Attrs[AttrStored] = StoredAfterEnd. The message says how many bytes
	// follow the end.
	WarnInputTrailingData = "input.trailing_data"

	// WarnTableRegionSplit: an area of a sheet whose cells are spread so
	// thinly that the rectangle around them is mostly empty was not
	// written as one table: each run of cells side by side in a row is a
	// table of one row. The message gives the sheet and the count.
	// Severity info: no cell is lost.
	WarnTableRegionSplit = "table.region_split"
	// WarnTableFormulaNoValue: cells hold formulas with no stored result
	// (a program wrote the file without calculating it), so they are
	// empty; formulas are not evaluated, and are in Table.Cells when
	// Options.Cells is set. One warning per part with the count.
	// Severity info.
	WarnTableFormulaNoValue = "table.formula_no_value"
	// WarnLimitExceeded: a limit was reached during extraction and the
	// output is cut or left out at that point. The limit is one of
	// Options.Limits or a fixed bound of the extractor (the longest field
	// instruction it keeps, the most columns of a table it reads); the
	// message says which, and what was left out. It covers anything that
	// would appear in the output: text, cells, notes, metadata values,
	// names, labels and addresses; a bound on data used only inside the
	// extractor needs no warning. Severity error, for every such cut.
	WarnLimitExceeded = "limit.exceeded"

	// WarnWarningsOmitted: more warnings were raised on the part or the
	// document than MaxWarnings, and the rest are counted here instead of
	// listed. Part.Warn and Document.Warn add it at the end of the list
	// (warnings appended to the list directly can follow it); extractors
	// do not emit it themselves. Its message begins with the
	// number of warnings it counts, and its severity is the highest of
	// theirs.
	WarnWarningsOmitted = "warnings.omitted"

	// WarnChildNotExtracted: an embedded document is listed in Children
	// with its name and size but was not extracted, because no child
	// extractor was supplied, the depth limit was reached, or the
	// extractor had nothing to hand over (a limit stopped it first).
	WarnChildNotExtracted = "child.not_extracted"

	// WarnChildUnsupported: no extractor handles the embedded document's
	// format, or it uses a feature that is not implemented. It is listed
	// in Children without content.
	WarnChildUnsupported = "child.unsupported"

	// WarnChildEncrypted: the embedded document is encrypted and was not
	// opened: no password was supplied, the one supplied does not open
	// it, or no key in Options.Keys matches a recipient it names.
	// Severity error.
	WarnChildEncrypted = "child.encrypted"

	// WarnChildFailed: extraction of an embedded document was attempted
	// and failed for any other reason. Severity error.
	WarnChildFailed = "child.failed"
)
