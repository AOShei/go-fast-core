// How a message's text in a declared character set becomes UTF-8: mail's
// policy over go-fast-core's charset, which does the decoding. Moved here
// from go-fast-email's internal/charset.
//
//   - A label is matched without quotes, spaces or an RFC 2231 language
//     suffix (NormalizeCharset).
//   - Text labelled US-ASCII, or not labelled, that is valid UTF-8 is taken
//     as UTF-8, as mail programs write it. Otherwise it is read in the set
//     its bytes are recognised as where one is certain (DetectCharset),
//     and else has its bytes outside ASCII as U+FFFD.
//   - "utf-16" and "ucs-2" without a byte order mark are big-endian (RFC
//     2781), where the Encoding Standard reads them as little-endian.
//   - Any other character set is not guessed at: bytes outside ASCII become
//     U+FFFD, and the caller says so in a warning that names the set.
//     Decoding a multi-byte set as a single-byte one would produce text
//     that looks like text and is not.

package rfc822

import (
	"bytes"
	"strings"
	"unicode/utf8"

	core "github.com/AOShei/go-fast-core/charset"
)

// CharsetResult says how a decoding went.
type CharsetResult struct {
	// Known is false when the character set is not one this package
	// decodes: bytes outside ASCII were replaced with U+FFFD.
	Known bool
	// Replaced is true when some bytes could not be decoded and were
	// replaced with U+FFFD.
	Replaced bool
	// Detected is the label of the character set the text was read in,
	// recognised from its bytes, when it declared none (or US-ASCII) and
	// is not UTF-8; "" otherwise.
	Detected string
	// Undetermined is true when the text declared no character set (or
	// US-ASCII), is not UTF-8, and no set is certain: its bytes outside
	// ASCII are U+FFFD.
	Undetermined bool
}

// Normalize returns the lowercase name of a charset label, without
// quotes, spaces or an RFC 2231 language suffix.
func NormalizeCharset(label string) string {
	label = strings.ToLower(strings.Trim(strings.TrimSpace(label), `"'`))
	if i := strings.IndexByte(label, '*'); i >= 0 { // RFC 2231 "charset*language"
		label = label[:i]
	}
	return label
}

// ascii are the labels of US-ASCII, and no label at all: valid UTF-8 is
// taken as UTF-8.
var ascii = map[string]bool{
	"": true, "us-ascii": true, "ascii": true, "ansi_x3.4-1968": true,
	"iso646-us": true, "us": true,
}

// lookup returns the encoding a label names, with mail's reading of
// "utf-16" and "ucs-2" (big-endian without a mark), and false for US-ASCII
// and for a set not decoded.
func lookup(l string) (core.Encoding, bool) {
	switch l {
	case "utf-16", "ucs-2":
		return core.UTF16BE, true
	}
	if ascii[l] {
		return 0, false
	}
	return core.Lookup(l)
}

// Known reports whether label names a character set this package decodes.
func KnownCharset(label string) bool {
	l := NormalizeCharset(label)
	_, ok := lookup(l)
	return ok || ascii[l]
}

// Decode returns b, in the character set label names, as UTF-8. It never
// fails: what cannot be decoded is U+FFFD, and CharsetResult says so.
func DecodeCharset(label string, b []byte) (string, CharsetResult) {
	l := NormalizeCharset(label)
	if ascii[l] {
		// Text labelled US-ASCII, or not labelled, that is valid UTF-8 is
		// taken as UTF-8: mail programs write it so. Otherwise the set is
		// recognised from the bytes where one is certain.
		if !hasZero(b) && utf8.Valid(b) {
			return string(b), CharsetResult{Known: true}
		}
		if set, ok := DetectCharset(b); ok {
			s, res := DecodeCharset(set, b)
			res.Detected = set
			return s, res
		}
		if utf8.Valid(b) {
			return string(b), CharsetResult{Known: true} // zero bytes, not UTF-16
		}
		return strings.ToValidUTF8(string(b), "\uFFFD"), CharsetResult{Known: true, Replaced: true, Undetermined: true}
	}
	enc, ok := lookup(l)
	if !ok {
		var sb strings.Builder
		sb.Grow(len(b))
		replaced := false
		for _, c := range b {
			if c < 0x80 {
				sb.WriteByte(c)
			} else {
				sb.WriteString("\uFFFD")
				replaced = true
			}
		}
		return sb.String(), CharsetResult{Known: false, Replaced: replaced}
	}
	if enc == core.UTF16LE || enc == core.UTF16BE {
		return DecodeUTF16(b, enc == core.UTF16LE)
	}
	if enc == core.UTF8 && utf8.Valid(b) {
		return string(b), CharsetResult{Known: true}
	}
	out, _, invalid, _ := core.Decode(b, core.Result{Enc: enc}, int64(len(b))*4+16)
	return string(out), CharsetResult{Known: true, Replaced: invalid > 0}
}

// DecodeDefault is Decode, but text that declares no character set or
// US-ASCII (label) and is not UTF-8 is read in def, when def is not empty,
// instead of in the set recognised from its bytes. Valid UTF-8 stays
// UTF-8, as in Decode.
func DecodeCharsetDefault(label, def string, b []byte) (string, CharsetResult) {
	if def != "" && ascii[NormalizeCharset(label)] && (hasZero(b) || !utf8.Valid(b)) {
		return DecodeCharset(def, b)
	}
	return DecodeCharset(label, b)
}

// DecodeUTF16 decodes UTF-16 in the byte order a byte order mark gives, or
// in little-endian order if le and there is no mark, else big-endian.
// Unpaired surrogates and an odd last byte become U+FFFD.
func DecodeUTF16(b []byte, le bool) (string, CharsetResult) {
	r := core.Result{Enc: core.UTF16BE}
	if le {
		r.Enc = core.UTF16LE
	}
	if len(b) >= 2 {
		switch {
		case b[0] == 0xFF && b[1] == 0xFE:
			r.Enc, r.BOM = core.UTF16LE, 2
		case b[0] == 0xFE && b[1] == 0xFF:
			r.Enc, r.BOM = core.UTF16BE, 2
		}
	}
	out, _, invalid, _ := core.Decode(b, r, int64(len(b))*2+16)
	return string(out), CharsetResult{Known: true, Replaced: invalid > 0}
}

// Detect recognises the character set of text that declares none and is
// not valid UTF-8 (or has zero bytes) from its bytes, where one is
// certain, as go-fast-core's charset recognises it. It returns the label
// to decode it with, or false.
func DetectCharset(b []byte) (string, bool) {
	r := core.SniffText(b, "")
	if r.Unknown {
		return "", false
	}
	return r.Enc.String(), true
}

// hasZero reports whether the start of b holds a zero byte, which text in
// a byte-oriented set does not: it may be UTF-16 without a mark.
func hasZero(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), 32<<10)], 0) >= 0
}
