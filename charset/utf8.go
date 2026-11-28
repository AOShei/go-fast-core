package charset

import (
	"encoding/binary"
	"math/bits"
)

// UTF8Len returns the length of the UTF-8 character b begins with: 1 for
// ASCII, 2 to 4 beyond it, and 0 when b does not begin with a whole and
// valid one (RFC 3629: no overlong form, no surrogate, nothing past
// U+10FFFF). It decides by the ranges the bytes may take, without
// decoding the character: where utf8.DecodeRune returns RuneError and 1,
// it returns 0.
func UTF8Len(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	c := b[0]
	switch {
	case c < 0x80:
		return 1
	case c >= 0xC2 && c <= 0xDF:
		if len(b) < 2 || b[1]&0xC0 != 0x80 {
			return 0
		}
		return 2
	case c >= 0xE0 && c <= 0xEF:
		if len(b) < 3 {
			return 0
		}
		lo, hi := byte(0x80), byte(0xBF)
		if c == 0xE0 {
			lo = 0xA0 // no overlong form
		} else if c == 0xED {
			hi = 0x9F // no surrogate
		}
		if b[1] < lo || b[1] > hi || b[2]&0xC0 != 0x80 {
			return 0
		}
		return 3
	case c >= 0xF0 && c <= 0xF4:
		if len(b) < 4 {
			return 0
		}
		lo, hi := byte(0x80), byte(0xBF)
		if c == 0xF0 {
			lo = 0x90 // no overlong form
		} else if c == 0xF4 {
			hi = 0x8F // nothing past U+10FFFF
		}
		if b[1] < lo || b[1] > hi || b[2]&0xC0 != 0x80 || b[3]&0xC0 != 0x80 {
			return 0
		}
		return 4
	}
	return 0
}

// ValidUTF8 reports whether b is valid UTF-8, as utf8.Valid does. Runs of
// ASCII are passed over eight bytes at a time, and each character beyond
// ASCII is checked by UTF8Len. utf8.Valid reads the rest of its input a
// byte at a time once it meets a character beyond ASCII, and decodes each:
// on text that mixes the two (French, German) or is all beyond ASCII
// (Greek, Russian), this costs a fraction of it.
func ValidUTF8(b []byte) bool {
	const highs = 0x8080808080808080
	n := len(b)
	for i := 0; i < n; {
		if b[i] < 0x80 {
			for i+8 <= n {
				w := binary.LittleEndian.Uint64(b[i:]) & highs
				if w != 0 {
					i += bits.TrailingZeros64(w) >> 3
					break
				}
				i += 8
			}
			for i < n && b[i] < 0x80 {
				i++
			}
			continue
		}
		// Two bytes, the usual case beyond ASCII (Latin letters with
		// accents, Greek, Cyrillic): checked here, without a call.
		if c := b[i]; c >= 0xC2 && c <= 0xDF && i+1 < n && b[i+1]&0xC0 == 0x80 {
			i += 2
			continue
		}
		m := UTF8Len(b[i:])
		if m == 0 {
			return false
		}
		i += m
	}
	return true
}

// CutUTF8 returns s cut to at most n bytes without splitting a UTF-8
// character: when the cut falls inside one, it moves back to where the
// character starts, at most three bytes. It neither trims nor mends
// anything else, and bytes that are not part of a character may be cut
// anywhere. n below 0 is taken as 0.
func CutUTF8[T string | []byte](s T, n int) T {
	if n >= len(s) {
		return s
	}
	if n <= 0 {
		return s[:0]
	}
	if s[n]&0xC0 != 0x80 {
		return s[:n] // the cut is where a character starts
	}
	for i := n - 1; i >= 0 && i >= n-3; i-- {
		c := s[i]
		if c < 0x80 {
			break
		}
		if c >= 0xC0 {
			size := 2
			if c >= 0xF0 {
				size = 4
			} else if c >= 0xE0 {
				size = 3
			}
			if i+size > n {
				return s[:i]
			}
			break
		}
	}
	return s[:n]
}
