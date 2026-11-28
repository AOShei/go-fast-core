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

// CutUTF8 cuts valid UTF-8 where a character starts, as walking back to
// the start of the character does, and any input to a prefix of at most n
// bytes and at least n-3; a string and a byte slice alike.
func TestCutUTF8(t *testing.T) {
	ref := func(b []byte, n int) []byte {
		for n > 0 && !utf8.RuneStart(b[n]) {
			n--
		}
		return b[:n]
	}
	check := func(b []byte, n int) {
		t.Helper()
		got := CutUTF8(b, n)
		if s := CutUTF8(string(b), n); s != string(got) {
			t.Fatalf("CutUTF8(%q, %d): string %q, bytes %q", b, n, s, got)
		}
		want := max(n, 0)
		if want >= len(b) {
			if string(got) != string(b) {
				t.Fatalf("CutUTF8(%q, %d) = %q, want it whole", b, n, got)
			}
			return
		}
		if utf8.Valid(b) {
			if r := ref(b, want); string(got) != string(r) {
				t.Fatalf("CutUTF8(%q, %d) = %q, want %q", b, n, got, r)
			}
		}
		if len(got) > want || len(got) < want-3 || string(b[:len(got)]) != string(got) {
			t.Fatalf("CutUTF8(%q, %d) = %q", b, n, got)
		}
	}
	for _, c := range []string{"", "abc", "caf\u00e9", "\u6f22\u5b57", "\U0001F600x", "\xff\x80\x80\x80\x80", "a\xc3", "\xe2\x82"} {
		for n := -1; n <= len(c)+1; n++ {
			check([]byte(c), n)
		}
	}
	const pool = "ab\x80\xbf\xc3\xa9\xe2\x82\xac\xf0\x9f\x98\x80\xff"
	r := rand.New(rand.NewPCG(5, 6))
	for range 50000 {
		b := make([]byte, r.IntN(16))
		for i := range b {
			b[i] = pool[r.IntN(len(pool))]
		}
		check(b, r.IntN(18)-1)
	}
	if n := testing.AllocsPerRun(100, func() { _ = CutUTF8("caf\u00e9 au lait", 4) }); n != 0 {
		t.Errorf("%v allocations", n)
	}
}
