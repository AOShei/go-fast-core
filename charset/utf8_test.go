package charset

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

// ValidUTF8 agrees with utf8.Valid, and UTF8Len with utf8.DecodeRune: on
// edge cases, at every offset of mixed text, and on random bytes.
func TestValidUTF8(t *testing.T) {
	cases := []string{"", "a", "abcdefgh", "abcdefghi", "\xe9", "caf\xc3\xa9 au lait", "\xc3", "abcdefg\xc3",
		"abcdefg\xc3\xa9", "\xed\xa0\x80", "\xed\x9f\xbf", "\xf4\x90\x80\x80", "\xf4\x8f\xbf\xbf", "\xef\xbf\xbd",
		"\xc0\x80", "\xc1\xbf", "\xe0\x9f\xbf", "\xe0\xa0\x80", "\xf0\x8f\xbf\xbf", "\xf0\x90\x80\x80", "\xe2\x82",
		"\xf5\x80\x80\x80", "\xff",
		strings.Repeat("d\u00e9j\u00e0 \u03bb\u03cc\u03b3\u03bf\u03c2 \u0442\u0435\u043a\u0441\u0442 ", 20) + "\xff"}
	check := func(b []byte) {
		t.Helper()
		if got, want := ValidUTF8(b), utf8.Valid(b); got != want {
			t.Fatalf("ValidUTF8(%q) = %v, want %v", b, got, want)
		}
		r, size := utf8.DecodeRune(b)
		want := size
		if r == utf8.RuneError && size <= 1 {
			want = 0
		}
		if got := UTF8Len(b); got != want {
			t.Fatalf("UTF8Len(%q) = %d, want %d", b, got, want)
		}
	}
	for _, c := range cases {
		for i := 0; i <= len(c); i++ {
			check([]byte(c[i:]))
		}
	}
	const pool = "ab\x80\xbf\xc3\xa9\xe2\x82\xac\xf0\x9f\x98\x80\xff\xed\xa0\xf4\x90\xe0\xc0"
	r := rand.New(rand.NewPCG(1, 2))
	for range 50000 {
		b := make([]byte, r.IntN(40))
		for i := range b {
			b[i] = pool[r.IntN(len(pool))]
		}
		check(b)
	}
}
