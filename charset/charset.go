// Package charset decodes text in the character encodings documents use
// to UTF-8. It is the one decoder of the go-fast libraries: HTML and XML
// documents, mail, RTF and the binary and XML Office formats all decode
// through it, so that the same bytes give the same text in every library.
//
// Decoded are UTF-8, UTF-16LE, UTF-16BE, windows-1252 (which the WHATWG
// Encoding Standard also uses for every ISO-8859-1 and US-ASCII label),
// the single-byte windows-874, windows-1250 to windows-1258 and macintosh,
// and the double-byte Shift_JIS, GBK, EUC-KR and Big5, read with
// Microsoft's code pages 932, 936, 949 and 950 (tables.go, dbcs.go), Big5
// with the HKSCS pairs code page 950 leaves undefined (big5-hkscs.txt);
// the Encoding Standard's other single-byte encodings, IBM866, ISO-8859-2
// to 16, KOI8-R, KOI8-U and x-mac-cyrillic (singlebyte.go); and GB18030,
// EUC-JP and ISO-2022-JP (cjk.go, iso2022.go). Where a code page leaves a
// byte or a pair undefined, the decoding is the Encoding Standard's.
//
// An encoding is found by its label (Lookup), by its Windows code page
// number (CodePage) or by a font's character set (ForCharset). Text is
// decoded whole (Decode, with a map back to source offsets), as a stream
// (Decoder, NewReader) or a byte at a time (Decoder.Byte).
//
// What decides the encoding of a document is in SniffText (a byte order
// mark, a label from outside, then the bytes themselves, detect.go) and
// SniffHTML, which adds the HTML standard's prescan for a <meta>
// declaration (prescan.go).
package charset

import (
	"bytes"
	"sort"
	"unicode/utf8"
)

// Encoding is one of the encodings this package decodes.
type Encoding uint8

const (
	UTF8 Encoding = iota
	UTF16LE
	UTF16BE
	Windows1252
	Windows874
	Windows1250
	Windows1251
	Windows1253
	Windows1254
	Windows1255
	Windows1256
	Windows1257
	Windows1258
	Macintosh
	ShiftJIS // code page 932
	GBK      // code page 936
	EUCKR    // code page 949
	Big5     // code page 950
	IBM866
	ISO8859_2
	ISO8859_3
	ISO8859_4
	ISO8859_5
	ISO8859_6
	ISO8859_7
	ISO8859_8
	ISO8859_10
	ISO8859_13
	ISO8859_14
	ISO8859_15
	ISO8859_16
	KOI8R
	KOI8U
	MacCyrillic
	EUCJP
	GB18030
	ISO2022JP
)

var names = [...]string{
	UTF8: "utf-8", UTF16LE: "utf-16le", UTF16BE: "utf-16be", Windows1252: "windows-1252",
	Windows874: "windows-874", Windows1250: "windows-1250", Windows1251: "windows-1251",
	Windows1253: "windows-1253", Windows1254: "windows-1254", Windows1255: "windows-1255",
	Windows1256: "windows-1256", Windows1257: "windows-1257", Windows1258: "windows-1258",
	Macintosh: "macintosh", ShiftJIS: "shift_jis", GBK: "gbk", EUCKR: "euc-kr", Big5: "big5",
	IBM866: "ibm866", ISO8859_2: "iso-8859-2", ISO8859_3: "iso-8859-3", ISO8859_4: "iso-8859-4",
	ISO8859_5: "iso-8859-5", ISO8859_6: "iso-8859-6", ISO8859_7: "iso-8859-7", ISO8859_8: "iso-8859-8",
	ISO8859_10: "iso-8859-10", ISO8859_13: "iso-8859-13", ISO8859_14: "iso-8859-14", ISO8859_15: "iso-8859-15",
	ISO8859_16: "iso-8859-16", KOI8R: "koi8-r", KOI8U: "koi8-u", MacCyrillic: "x-mac-cyrillic",
	EUCJP: "euc-jp", GB18030: "gb18030", ISO2022JP: "iso-2022-jp",
}

func (e Encoding) String() string {
	if int(e) < len(names) {
		return names[e]
	}
	return "utf-8"
}

// single returns the upper half of a single-byte table encoding, or nil.
func (e Encoding) single() *[128]uint16 {
	switch e {
	case Windows874:
		return &cp874
	case Windows1250:
		return &cp1250
	case Windows1251:
		return &cp1251
	case Windows1253:
		return &cp1253
	case Windows1254:
		return &cp1254
	case Windows1255:
		return &cp1255
	case Windows1256:
		return &cp1256
	case Windows1257:
		return &cp1257
	case Windows1258:
		return &cp1258
	case Macintosh:
		return &macRoman
	case IBM866:
		return &ibm866
	case ISO8859_2:
		return &iso8859_2
	case ISO8859_3:
		return &iso8859_3
	case ISO8859_4:
		return &iso8859_4
	case ISO8859_5:
		return &iso8859_5
	case ISO8859_6:
		return &iso8859_6
	case ISO8859_7:
		return &iso8859_7
	case ISO8859_8:
		return &iso8859_8
	case ISO8859_10:
		return &iso8859_10
	case ISO8859_13:
		return &iso8859_13
	case ISO8859_14:
		return &iso8859_14
	case ISO8859_15:
		return &iso8859_15
	case ISO8859_16:
		return &iso8859_16
	case KOI8R:
		return &koi8r
	case KOI8U:
		return &koi8u
	case MacCyrillic:
		return &macCyrillic
	}
	return nil
}

// codePage returns the double-byte code page of e, or 0.
func (e Encoding) codePage() int {
	switch e {
	case ShiftJIS:
		return 932
	case GBK:
		return 936
	case EUCKR:
		return 949
	case Big5:
		return 950
	}
	return 0
}

// How the encoding was decided.
const (
	FromBOM       = "bom"
	FromTransport = "transport"
	FromMeta      = "meta"
	FromDetected  = "detected"
)

// Result is the outcome of SniffText or SniffHTML.
type Result struct {
	Enc Encoding
	// From says how the encoding was decided: FromBOM, FromTransport,
	// FromMeta or FromDetected.
	From string
	// BOM is the length of the byte order mark, 0 if there is none.
	BOM int
	// Declared is the label a <meta> declaration gives, lowercased, held
	// to 64 bytes; empty if there was none or it was not looked for.
	Declared string
	// Unsupported reports that Declared names an encoding this package
	// does not decode; Enc is then Windows1252.
	Unsupported bool
	// Transport is the label the caller gave, trimmed, lowercased and
	// held to 64 bytes, when it names an encoding this package does not
	// decode; it was ignored.
	Transport string
	// Guessed reports that nothing declared the encoding and the input is
	// not valid UTF-8: Enc was recognised from the bytes (detect.go), or
	// is windows-1252 for text whose bytes read as it.
	Guessed bool
	// valid reports that the whole input was found to be valid UTF-8 (and
	// has no byte order mark), so that Decode need not look again.
	valid bool
	// Unknown reports that nothing declared the encoding, the input is not
	// valid UTF-8, and no encoding is certain: read as windows-1252 it
	// would be garbage. Enc is Windows1252.
	Unknown bool
}

// transport looks up the caller's label. It returns the encoding and
// true if it is decoded here, and otherwise the cleaned label to report
// (empty when no label was given).
func transport(label string) (Encoding, bool, string) {
	if label == "" {
		return 0, false, ""
	}
	// A MIME parameter may come with its quotes.
	l := lower(bytes.Trim([]byte(label), "\t\n\f\r \"'"))
	if e, ok := labels[l]; ok {
		return e, true, ""
	}
	if len(l) > 64 {
		l = l[:64]
	}
	if l == "" {
		return 0, false, ""
	}
	return 0, false, l
}

func bom(b []byte) (Result, bool) {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return Result{Enc: UTF8, From: FromBOM, BOM: 3}, true
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return Result{Enc: UTF16BE, From: FromBOM, BOM: 2}, true
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return Result{Enc: UTF16LE, From: FromBOM, BOM: 2}, true
	}
	return Result{}, false
}

// SniffText decides the encoding of text that has no way to declare one
// (Markdown, plain text): a byte order mark, else the transport-layer
// label tr if it names an encoding decoded here, else UTF-8 if b is valid
// UTF-8, else windows-1252.
func SniffText(b []byte, tr string) Result {
	if r, ok := bom(b); ok {
		return r
	}
	enc, ok, bad := transport(tr)
	if ok {
		return Result{Enc: enc, From: FromTransport}
	}
	if unmarkedUTF16(b) {
		if enc, ok := utf16Unmarked(b[:min(len(b), detectSample)]); ok {
			return Result{Enc: enc, From: FromDetected, Guessed: true, Transport: bad}
		}
	}
	if utf8.Valid(b) {
		return Result{Enc: UTF8, From: FromDetected, Transport: bad, valid: true}
	}
	r := detect(b)
	r.Transport = bad
	return r
}

// Sniff decides the encoding of b, given the transport-layer label tr
// (empty for none).
func SniffHTML(b []byte, tr string) Result {
	if r, ok := bom(b); ok {
		return r
	}
	enc, ok, bad := transport(tr)
	if ok {
		return Result{Enc: enc, From: FromTransport}
	}
	r := sniffMeta(b)
	r.Transport = bad
	return r
}

// latePrescan is how far a <meta> declaration is looked for when the
// first 1024 bytes have none and the input is not UTF-8: the HTML
// standard's parser changes the encoding when it meets one later ("change
// the encoding"), and a declaration is taken over a set recognised from
// the bytes.
const latePrescan = 1 << 20

func sniffMeta(b []byte) Result {
	label, ok := prescan(b[:min(len(b), 1024)])
	valid := -1 // whether b is valid UTF-8, once known
	if !ok && len(b) > 1024 {
		valid = btoi(utf8.Valid(b))
		if valid == 0 {
			label, ok = prescan(b[:min(len(b), latePrescan)])
		}
	}
	if ok {
		r := Result{From: FromMeta, Declared: label}
		enc, known := Lookup(label)
		switch {
		case !known:
			r.Enc, r.Unsupported = Windows1252, true
		case enc == UTF16LE || enc == UTF16BE:
			// A document that can declare its encoding in ASCII is not
			// UTF-16; the standard reads the declaration as UTF-8.
			r.Enc = UTF8
		default:
			r.Enc = enc
		}
		return r
	}
	if unmarkedUTF16(b) {
		if enc, ok := utf16Unmarked(b[:min(len(b), detectSample)]); ok {
			return Result{Enc: enc, From: FromDetected, Guessed: true}
		}
	}
	if valid == 1 || valid == -1 && utf8.Valid(b) {
		return Result{Enc: UTF8, From: FromDetected, valid: true}
	}
	return detect(b)
}

func btoi(v bool) int {
	if v {
		return 1
	}
	return 0
}

// labels maps the WHATWG Encoding standard's labels of the encodings
// decoded here. x-user-defined is read as windows-1252, as the prescan
// prescribes.
var labels = map[string]Encoding{
	"unicode-1-1-utf-8": UTF8, "unicode11utf8": UTF8, "unicode20utf8": UTF8,
	"utf-8": UTF8, "utf8": UTF8, "x-unicode20utf8": UTF8,

	"csunicode": UTF16LE, "iso-10646-ucs-2": UTF16LE, "ucs-2": UTF16LE,
	"unicode": UTF16LE, "unicodefeff": UTF16LE, "utf-16": UTF16LE, "utf-16le": UTF16LE,
	"unicodefffe": UTF16BE, "utf-16be": UTF16BE,

	"ansi_x3.4-1968": Windows1252, "ascii": Windows1252, "cp1252": Windows1252,
	"cp819": Windows1252, "csisolatin1": Windows1252, "ibm819": Windows1252,
	"iso-8859-1": Windows1252, "iso-ir-100": Windows1252, "iso8859-1": Windows1252,
	"iso88591": Windows1252, "iso_8859-1": Windows1252, "iso_8859-1:1987": Windows1252,
	"l1": Windows1252, "latin1": Windows1252, "us-ascii": Windows1252,
	"windows-1252": Windows1252, "x-cp1252": Windows1252, "x-user-defined": Windows1252,

	"dos-874": Windows874, "iso-8859-11": Windows874, "iso8859-11": Windows874,
	"iso885911": Windows874, "tis-620": Windows874, "windows-874": Windows874,

	"cp1250": Windows1250, "windows-1250": Windows1250, "x-cp1250": Windows1250,
	"cp1251": Windows1251, "windows-1251": Windows1251, "x-cp1251": Windows1251,
	"cp1253": Windows1253, "windows-1253": Windows1253, "x-cp1253": Windows1253,
	"cp1254": Windows1254, "csisolatin5": Windows1254, "iso-8859-9": Windows1254,
	"iso-ir-148": Windows1254, "iso8859-9": Windows1254, "iso88599": Windows1254,
	"iso_8859-9": Windows1254, "iso_8859-9:1989": Windows1254, "l5": Windows1254,
	"latin5": Windows1254, "windows-1254": Windows1254, "x-cp1254": Windows1254,
	"cp1255": Windows1255, "windows-1255": Windows1255, "x-cp1255": Windows1255,
	"cp1256": Windows1256, "windows-1256": Windows1256, "x-cp1256": Windows1256,
	"cp1257": Windows1257, "windows-1257": Windows1257, "x-cp1257": Windows1257,
	"cp1258": Windows1258, "windows-1258": Windows1258, "x-cp1258": Windows1258,

	"csmacintosh": Macintosh, "mac": Macintosh, "macintosh": Macintosh, "x-mac-roman": Macintosh,

	"csshiftjis": ShiftJIS, "ms932": ShiftJIS, "ms_kanji": ShiftJIS, "shift-jis": ShiftJIS,
	"shift_jis": ShiftJIS, "sjis": ShiftJIS, "windows-31j": ShiftJIS, "x-sjis": ShiftJIS,

	"chinese": GBK, "csgb2312": GBK, "csiso58gb231280": GBK, "gb2312": GBK, "gb_2312": GBK,
	"gb_2312-80": GBK, "gbk": GBK, "iso-ir-58": GBK, "x-gbk": GBK,

	"cseuckr": EUCKR, "csksc56011987": EUCKR, "euc-kr": EUCKR, "iso-ir-149": EUCKR,
	"korean": EUCKR, "ks_c_5601-1987": EUCKR, "ks_c_5601-1989": EUCKR, "ksc5601": EUCKR,
	"ksc_5601": EUCKR, "windows-949": EUCKR,

	"big5": Big5, "big5-hkscs": Big5, "cn-big5": Big5, "csbig5": Big5, "x-x-big5": Big5,
}

// Lookup returns the encoding a label names, if it is one decoded here.
// The label is matched after trimming ASCII whitespace and lowercasing.
func Lookup(label string) (Encoding, bool) {
	e, ok := labels[lower(bytes.Trim([]byte(label), "\t\n\f\r "))]
	return e, ok
}

// extraLabels are names in use that the Encoding Standard does not list:
// those of Windows code pages that mail programs and XML declarations
// write, and ISO-2022-JP's, whose decoder the standard defines.
var extraLabels = map[string]Encoding{
	"cp874": Windows874, "x-cp874": Windows874,
	"cp932": ShiftJIS, "cp936": GBK, "windows-936": GBK,
	"cp949": EUCKR, "cp950": Big5, "windows-950": Big5,
	"cp10000": Macintosh,
	"latin9":  ISO8859_15, "latin-9": ISO8859_15,
	"iso-2022-jp": ISO2022JP, "csiso2022jp": ISO2022JP,
}

// The labels of singlebyte.go and the extra ones are looked up with the
// others.
func init() {
	for l, e := range singleLabels {
		labels[l] = e
	}
	for l, e := range extraLabels {
		labels[l] = e
	}
}

// Map takes offsets in the decoded text back to offsets in the source.
// It keeps a checkpoint every few kilobytes of decoded text and decodes
// forward from the nearest one, so it costs little memory whatever the
// input. It also remembers where the last call stopped: offsets asked for
// in order, as a document's blocks are, cost in all one pass over the
// source. A Map is not safe for use by more than one goroutine at once.
type Map struct {
	src      []byte
	enc      Encoding
	identity bool  // decoded text is the source after the BOM
	base     int64 // the BOM length
	cps      []checkpoint
	last     checkpoint // where the last call stopped: a character start
}

type checkpoint struct {
	dec, src int
	st       state // the decoder's state at src
}

const checkpointEvery = 4096

// Source returns the offset in the source of the character that starts
// at or covers offset dec of the decoded text. An offset at the end of the
// decoded text gives the end of the source.
func (m *Map) Source(dec int) int64 {
	if m == nil {
		return int64(dec)
	}
	if m.identity {
		return m.base + int64(dec)
	}
	i := sort.Search(len(m.cps), func(i int) bool { return m.cps[i].dec > dec }) - 1
	if i < 0 {
		i = 0
	}
	c := m.cps[i]
	if m.last.dec > c.dec && m.last.dec <= dec {
		c = m.last
	}
	for c.dec < dec && c.src < len(m.src) {
		st := c.st
		dn, sn := st.step(m.src, c.src, m.enc)
		if c.dec+dn > dec {
			break
		}
		c = checkpoint{c.dec + dn, c.src + sn, st}
	}
	m.last = c
	return int64(c.src)
}

// state is what decoding carries from one character to the next. Only
// ISO-2022-JP has any: the character set its escape sequences select, and
// whether the last thing read was an escape sequence.
type state struct {
	iso    uint8
	output bool
}

// noRune is the character of bytes that make none: an escape sequence.
const noRune = -1

// next decodes the character at src[s:]. It returns the character (noRune
// for none), a second character the same bytes stand for or 0, the number
// of source bytes taken, and whether the character is a U+FFFD for bytes
// that are not one.
func (st *state) next(src []byte, s int, enc Encoding) (r, mark rune, n int, bad bool) {
	if enc == ISO2022JP {
		r, n, bad = st.iso2022(src, s)
		return r, 0, n, bad
	}
	r, n, bad = decodeAt(src, s, enc)
	return r, markAt(src, s, enc), n, bad
}

// step decodes the character at src[s:] and returns how many bytes it
// becomes in UTF-8 and how many it takes in the source. It must agree with
// Decode.
func (st *state) step(src []byte, s int, enc Encoding) (dec, n int) {
	r, mark, n, _ := st.next(src, s, enc)
	return runeLen(r) + markLen(mark), n
}

// runeLen is the length utf8.AppendRune gives r: none for noRune, U+FFFD's
// for any other rune it cannot encode.
func runeLen(r rune) int {
	if r == noRune {
		return 0
	}
	if n := utf8.RuneLen(r); n > 0 {
		return n
	}
	return 3
}

// markLen is the length of a second character, 0 for none.
func markLen(mark rune) int {
	if mark == 0 {
		return 0
	}
	return runeLen(mark)
}

// appendRune appends r and mark, which may be noRune or 0 for none.
func appendRune(dst []byte, r, mark rune) []byte {
	if r != noRune {
		dst = utf8.AppendRune(dst, r)
	}
	if mark != 0 {
		dst = utf8.AppendRune(dst, mark)
	}
	return dst
}

// decodeAt decodes the character at src[s:] in enc, returning it, the
// number of source bytes it takes, and whether it was not valid (and is
// U+FFFD).
func decodeAt(src []byte, s int, enc Encoding) (rune, int, bool) {
	b := src[s]
	switch enc {
	case UTF8:
		r, n := utf8.DecodeRune(src[s:])
		if r == utf8.RuneError && n <= 1 {
			return utf8.RuneError, 1, true
		}
		return r, n, false
	case Windows1252:
		return win1252(b), 1, false
	case UTF16LE, UTF16BE:
		r, n := utf16At(src, s, enc == UTF16BE)
		return r, n, r == utf8.RuneError
	case EUCJP:
		if b < 0x80 {
			return rune(b), 1, false
		}
		return eucJPAt(src, s)
	case GB18030:
		if b < 0x80 {
			return rune(b), 1, false
		}
		return gb18030At(src, s)
	}
	if b < 0x80 {
		return rune(b), 1, false
	}
	if t := enc.single(); t != nil {
		if r := t[b-0x80]; r != 0 {
			return rune(r), 1, false
		}
		if b < 0xA0 {
			// The Encoding standard's indexes give the bytes 0x80 to
			// 0x9F a code page leaves undefined as the C1 control of the
			// same value.
			return rune(b), 1, false
		}
		return utf8.RuneError, 1, true
	}
	return dbcsAt(src, s, enc.codePage())
}

// dbcsAt decodes a character of double-byte code page cp at src[s:],
// whose first byte is not ASCII. A lead byte and a trail byte that do not
// make a character are U+FFFD; if the trail byte is ASCII it is read
// again as a character of its own, as the Encoding standard's decoders
// do.
func dbcsAt(src []byte, s int, cp int) (rune, int, bool) {
	t := dbcsTable(cp)
	b := src[s]
	if !isLead(cp, b) {
		if r := t.single[b-0x80]; r != 0 {
			return rune(r), 1, false
		}
		if cp == 932 && b == 0x80 {
			// The Encoding standard's Shift_JIS decoder reads 0x80 as
			// U+0080.
			return 0x80, 1, false
		}
		if cp == 936 && b == 0x80 {
			// Code page 936 and the Encoding standard's GBK decoder read
			// 0x80 as the euro sign; cp936.txt, made from Python's codec,
			// does not have it.
			return 0x20AC, 1, false
		}
		return utf8.RuneError, 1, true
	}
	if s+1 >= len(src) {
		return utf8.RuneError, 1, true
	}
	trail := src[s+1]
	if cp == 950 {
		// The pairs that decode to two code points; markAt gives the
		// second.
		if r, _ := big5Pair(b, trail); r != 0 {
			return r, 2, false
		}
	}
	if r := t.pair(b, trail); r != 0 {
		return r, 2, false
	}
	if r := userDefined(cp, b, trail); r != 0 {
		return r, 2, false
	}
	if trail < 0x80 {
		return utf8.RuneError, 1, true
	}
	return utf8.RuneError, 2, true
}

// userDefined returns the Private Use Area character the Encoding
// standard gives a lead and trail byte in a user-defined area that the
// code page tables leave out, or 0.
func userDefined(cp int, lead, trail byte) rune {
	switch cp {
	case 932:
		// Shift_JIS pointers 8836 to 10715: lead bytes 0xF0 to 0xF9,
		// trail bytes 0x40 to 0x7E and 0x80 to 0xFC, to U+E000 onwards.
		if lead < 0xF0 || lead > 0xF9 || trail < 0x40 || trail == 0x7F || trail > 0xFC {
			return 0
		}
		off := 0x40
		if trail > 0x7F {
			off = 0x41
		}
		return 0xE000 + rune(lead-0xF0)*188 + rune(int(trail)-off)
	case 936:
		// GBK's user-defined areas, as GB 18030 and the Encoding
		// standard's index-gb18030 map them.
		switch {
		case lead >= 0xAA && lead <= 0xAF && trail >= 0xA1 && trail <= 0xFE:
			return 0xE000 + rune(lead-0xAA)*94 + rune(trail-0xA1)
		case lead >= 0xF8 && lead <= 0xFE && trail >= 0xA1 && trail <= 0xFE:
			return 0xE234 + rune(lead-0xF8)*94 + rune(trail-0xA1)
		case lead >= 0xA1 && lead <= 0xA7 && trail >= 0x40 && trail <= 0xA0 && trail != 0x7F:
			off := rune(trail - 0x40)
			if trail > 0x7F {
				off--
			}
			return 0xE4C6 + rune(lead-0xA1)*96 + off
		}
	}
	return 0
}

// isLead reports whether b is a lead byte of double-byte code page cp.
func isLead(cp int, b byte) bool {
	switch cp {
	case 932:
		return b >= 0x81 && b <= 0x9F || b >= 0xE0 && b <= 0xFC
	case 936, 949, 950:
		return b >= 0x81 && b <= 0xFE
	}
	return false
}

// Decode decodes src, from which the BOM has not been removed, to UTF-8.
// It returns the text, the map back to src, and the number of byte
// sequences that could not be decoded and were replaced with U+FFFD.
//
// limit bounds the decoded size; Decode stops there and reports cut.
func Decode(src []byte, r Result, limit int64) (out []byte, m *Map, invalid int, cut bool) {
	m = &Map{src: src, enc: r.Enc, base: int64(r.BOM)}
	body := src[r.BOM:]
	if r.Enc == UTF8 && (r.valid || utf8.Valid(body)) {
		m.identity = true
		if int64(len(body)) > limit {
			// Cut at a character boundary.
			n := int(limit)
			for n > 0 && !utf8.RuneStart(body[n]) {
				n--
			}
			return body[:n], m, 0, true
		}
		return body, m, 0, false
	}
	m.cps = append(m.cps, checkpoint{src: r.BOM})
	grow := len(body)
	if r.Enc == UTF16LE || r.Enc == UTF16BE {
		grow = len(body) / 2 * 3 / 2
	}
	out = make([]byte, 0, int(min(int64(grow)+8, limit)))
	last := 0
	var st state
	for s := r.BOM; s < len(src); {
		if len(out)-last >= checkpointEvery {
			m.cps = append(m.cps, checkpoint{len(out), s, st})
			last = len(out)
		}
		ch, mark, n, bad := st.next(src, s, r.Enc)
		if bad {
			invalid++
		}
		if int64(len(out)+runeLen(ch)+markLen(mark)) > limit {
			return out, m, invalid, true
		}
		out = appendRune(out, ch, mark)
		s += n
	}
	return out, m, invalid, false
}

// utf16At decodes the UTF-16 code unit, or surrogate pair, at src[s:].
// An unpaired surrogate or an odd final byte is U+FFFD.
func utf16At(src []byte, s int, be bool) (rune, int) {
	unit := func(i int) (rune, bool) {
		if i+1 >= len(src) {
			return 0, false
		}
		if be {
			return rune(src[i])<<8 | rune(src[i+1]), true
		}
		return rune(src[i+1])<<8 | rune(src[i]), true
	}
	u, ok := unit(s)
	if !ok {
		return utf8.RuneError, len(src) - s
	}
	switch {
	case u >= 0xD800 && u < 0xDC00:
		if v, ok := unit(s + 2); ok && v >= 0xDC00 && v < 0xE000 {
			return 0x10000 + (u-0xD800)<<10 + (v - 0xDC00), 4
		}
		return utf8.RuneError, 2
	case u >= 0xDC00 && u < 0xE000:
		return utf8.RuneError, 2
	}
	return u, 2
}

// win1252High is windows-1252 for bytes 0x80 to 0x9F, as the WHATWG
// Encoding standard's index-windows-1252 gives it. The five bytes it
// leaves undefined map to the C1 control of the same value.
var win1252High = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

func win1252(b byte) rune {
	if b >= 0x80 && b < 0xA0 {
		return win1252High[b-0x80]
	}
	return rune(b)
}

// C1 returns the character a numeric character reference to a C1 control
// stands for, as the HTML standard's table gives it (the windows-1252
// character of that byte), and false for any other code point or for one
// of the five C1 values the table leaves alone.
func C1(r rune) (rune, bool) {
	if r < 0x80 || r >= 0xA0 {
		return 0, false
	}
	m := win1252High[r-0x80]
	return m, m != r
}
