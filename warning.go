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
	// under a format prefix, e.g. "pdf.xref_rebuilt".
	Code     string   `json:"code"`
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

	// WarnTextHidden: the part contains text that a reader of the source
	// cannot see; the blocks concerned carry the reasons in Block.Hidden.
	// One per part, added by Part.MarkHidden. Severity info when the only
	// reason is the text layer of a scanned page, warning otherwise. On a
	// document it means a metadata string contains invisible characters.
	WarnTextHidden = "text.hidden"

	// WarnTextUnshownOutlier: the part holds routine unshown data (a
	// comment, an attribute, a field instruction, a document variable, a
	// mail header) that is out of proportion for its kind (see
	// UnshownOutlier). The blocks concerned are flagged HiddenInvisible.
	// The message says how many items and of what kind.
	WarnTextUnshownOutlier = "text.unshown_outlier"

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

	// WarnLimitExceeded: a limit from Options.Limits was reached during
	// extraction and the output is truncated at that point. Severity
	// error.
	WarnLimitExceeded = "limit.exceeded"

	// WarnChildNotExtracted: an embedded document is listed in Children
	// with its name and size but was not extracted, because no child
	// extractor was supplied or the depth limit was reached.
	WarnChildNotExtracted = "child.not_extracted"

	// WarnChildUnsupported: no extractor handles the embedded document's
	// format, or it uses a feature that is not implemented. It is listed
	// in Children without content.
	WarnChildUnsupported = "child.unsupported"

	// WarnChildEncrypted: the embedded document is encrypted and was not
	// opened, because no password was supplied or the one supplied does
	// not open it. Severity error.
	WarnChildEncrypted = "child.encrypted"

	// WarnChildFailed: extraction of an embedded document was attempted
	// and failed for any other reason. Severity error.
	WarnChildFailed = "child.failed"
)
