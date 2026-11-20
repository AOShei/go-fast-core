package gofastcore

import (
	"strings"
	"unicode"
)

// Routine unshown data (comments, attributes, field instructions,
// document variables, mail headers, a MIME preamble) is not flagged
// hidden for being unshown: nearly every file has some. It is flagged
// only when one item is out of proportion for its kind; see
// UnshownOutlier.
const (
	// UnshownOutlierWords is the number of words above which one item of
	// routine unshown text is an outlier.
	UnshownOutlierWords = 30
	// UnshownOutlierChars is the number of characters, white space not
	// counted, above which one item is an outlier whatever its word
	// count: it catches text written without spaces (Chinese, Japanese,
	// Thai) and encoded payloads such as base64.
	UnshownOutlierChars = 300
)

// UnshownOutlier reports whether s, one item of routine unshown text, is
// out of proportion: more than UnshownOutlierWords words, or more than
// UnshownOutlierChars characters other than white space.
//
// A word is a run of characters between white space that, with
// punctuation trimmed from both ends, consists only of letters, marks,
// digits, apostrophes and hyphens. Tokens such as "dkim=pass",
// "header.i=@example.com" or a URL are not words, so machine-written
// key=value data counts by its length only.
//
// An extractor may exempt kinds of data that are long by nature, such as
// script code or the signatures in mail headers, and says so in its
// README. It flags an outlier HiddenInvisible and adds a
// WarnTextUnshownOutlier warning to the part.
func UnshownOutlier(s string) bool {
	words, chars := 0, 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			chars++
		}
	}
	if chars > UnshownOutlierChars {
		return true
	}
	for tok := range strings.FieldsSeq(s) {
		if isWord(tok) {
			words++
			if words > UnshownOutlierWords {
				return true
			}
		}
	}
	return false
}

func isWord(tok string) bool {
	tok = strings.TrimFunc(tok, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSymbol(r) })
	if tok == "" {
		return false
	}
	letter := false
	for _, r := range tok {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsMark(r), unicode.IsDigit(r), r == '\'', r == '’', r == '-':
		default:
			return false
		}
	}
	return letter
}
