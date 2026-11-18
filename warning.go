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

	// WarnImageUnsupportedEncoding: image data was requested
	// (ImagesData) and an image uses an encoding the extractor cannot
	// deliver; its block is present without data.
	WarnImageUnsupportedEncoding = "image.unsupported_encoding"

	// WarnInputRepaired: the input was structurally damaged and was read
	// by a recovery path. Severity info if nothing is known to be lost.
	WarnInputRepaired = "input.repaired"

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
