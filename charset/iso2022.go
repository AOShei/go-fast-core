// ISO-2022-JP, decoded as the WHATWG Encoding Standard's decoder does,
// from go-fast-email's decoder (internal/charset/cjk.go at v0.3.0), a
// character at a time so that the map back to the source works as for
// the other encodings.

package charset

import "unicode/utf8"

// The character sets ISO-2022-JP's escape sequences select.
const (
	isoASCII    = iota
	isoRoman    // JIS X 0201 Roman: ASCII with a yen sign and an overline
	isoKatakana // JIS X 0201 Katakana
	isoJIS      // JIS X 0208, two bytes a character
)

// iso2022 decodes the character at src[s:] in ISO-2022-JP. An escape
// sequence is no character (noRune), unless it follows another, which is
// an error (U+FFFD).
func (st *state) iso2022(src []byte, s int) (rune, int, bool) {
	c := src[s]
	if c == 0x1B {
		if s+1 >= len(src) || src[s+1] != 0x24 && src[s+1] != 0x28 {
			// What follows is read again in the current set.
			st.output = false
			return utf8.RuneError, 1, true
		}
		if s+2 >= len(src) {
			return utf8.RuneError, 1, true // the "$" or "(" is read again
		}
		next := -1
		switch l, e := src[s+1], src[s+2]; {
		case l == 0x28 && e == 0x42:
			next = isoASCII
		case l == 0x28 && e == 0x4A:
			next = isoRoman
		case l == 0x28 && e == 0x49:
			next = isoKatakana
		case l == 0x24 && (e == 0x40 || e == 0x42):
			next = isoJIS
		}
		if next < 0 {
			return utf8.RuneError, 1, true // the two bytes after ESC are read again
		}
		st.iso = uint8(next)
		if st.output {
			return utf8.RuneError, 3, true
		}
		st.output = true
		return noRune, 3, false
	}
	st.output = false
	switch st.iso {
	case isoASCII, isoRoman:
		switch {
		case c >= 0x80 || c == 0x0E || c == 0x0F:
			return utf8.RuneError, 1, true
		case st.iso == isoRoman && c == 0x5C:
			return 0xA5, 1, false
		case st.iso == isoRoman && c == 0x7E:
			return 0x203E, 1, false
		}
		return rune(c), 1, false
	case isoKatakana:
		if c >= 0x21 && c <= 0x5F {
			return 0xFF61 + rune(c-0x21), 1, false
		}
		return utf8.RuneError, 1, true
	}
	// JIS X 0208.
	if c < 0x21 || c > 0x7E || s+1 >= len(src) {
		return utf8.RuneError, 1, true
	}
	c2 := src[s+1]
	if c2 == 0x1B {
		// An escape in place of the trail byte: the lead is an error and
		// the escape is read.
		return utf8.RuneError, 1, true
	}
	if c2 >= 0x21 && c2 <= 0x7E {
		if r := tableJIS0208.get().pair(c+0x80, c2+0x80); r != 0 {
			return r, 2, false
		}
	}
	return utf8.RuneError, 2, true
}
