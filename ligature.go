package gofastcore

import "strings"

// The presentation-form ligatures U+FB00 to U+FB06 and the letters each
// stands for. The last two are both "st": the first is the long-s form.
const (
	ligatureFirst = 0xFB00
	ligatureLast  = 0xFB06
)

var ligatureLetters = [...]string{"ff", "fi", "fl", "ffi", "ffl", "st", "st"}

// Ligature returns the letters a presentation-form ligature (U+FB00 to
// U+FB06) stands for, and "" for any other character.
//
// Every go-fast library writes these seven characters as their letters in
// all text it extracts, because the output is searched and compared: a
// reader looking for "office" must find a document that spells it with
// U+FB01. Nothing wider is done; this is not compatibility normalization.
// An extractor that builds text a character at a time calls Ligature, and
// one that has whole strings calls ExpandLigatures or AppendLigatures, so
// that no library keeps its own table.
func Ligature(r rune) string {
	if r < ligatureFirst || r > ligatureLast {
		return ""
	}
	return ligatureLetters[r-ligatureFirst]
}

// ligatureAt reports whether s has one of the ligatures at i, and which.
// All seven are three bytes in UTF-8 and differ only in the last.
func ligatureAt[T string | []byte](s T, i int) (int, bool) {
	if i+2 < len(s) && s[i] == 0xEF && s[i+1] == 0xAC && s[i+2] >= 0x80 && s[i+2] <= 0x86 {
		return int(s[i+2] - 0x80), true
	}
	return 0, false
}

// ExpandLigatures returns s with every presentation-form ligature
// (U+FB00 to U+FB06) written as its letters. A string that has none is
// returned as it is, without allocating.
func ExpandLigatures(s string) string {
	first := strings.IndexByte(s, 0xEF)
	for first >= 0 {
		if _, ok := ligatureAt(s, first); ok {
			break
		}
		next := strings.IndexByte(s[first+1:], 0xEF)
		if next < 0 {
			return s
		}
		first += 1 + next
	}
	if first < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(s[:first])
	for i := first; i < len(s); {
		if k, ok := ligatureAt(s, i); ok {
			b.WriteString(ligatureLetters[k])
			i += 3
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// AppendLigatures appends src to dst with every presentation-form
// ligature written as its letters, and returns the extended slice. It is
// ExpandLigatures for an extractor that assembles text in a buffer.
func AppendLigatures(dst, src []byte) []byte {
	start := 0
	for i := 0; i+2 < len(src); i++ {
		if src[i] != 0xEF {
			continue
		}
		if k, ok := ligatureAt(src, i); ok {
			dst = append(dst, src[start:i]...)
			dst = append(dst, ligatureLetters[k]...)
			i += 2
			start = i + 1
		}
	}
	return append(dst, src[start:]...)
}
