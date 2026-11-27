package rfc822

import "testing"

func TestDecode(t *testing.T) {
	for _, c := range []struct {
		label    string
		in       string
		want     string
		known    bool
		replaced bool
	}{
		{"utf-8", "caf\xc3\xa9", "café", true, false},
		{"UTF-8", "bad\xff", "bad\uFFFD", true, true},
		{"", "plain", "plain", true, false},
		{"us-ascii", "caf\xc3\xa9", "café", true, false},       // valid UTF-8 mislabelled
		{"us-ascii", "caf\xe9", "café", true, false},           // recognised as windows-1252
		{"", "\xe9\xe8\xe9\xe8\xe9\xe8", "\uFFFD", true, true}, // no set is certain: one U+FFFD for the run
		{"ISO-8859-1", "caf\xe9 \x80 \x93x\x94", "café € “x”", true, false},
		{"\"windows-1252\"", "\x81", "\u0081", true, false}, // undefined: the C1 control (WHATWG)
		{"iso-8859-15", "\xa4\xbd\xe9", "€œé", true, false},
		{"utf-16le", "h\x00i\x00", "hi", true, false},
		{"utf-16", "\xfe\xff\x00h\x00i", "hi", true, false},
		{"utf-16be", "\xd8\x00", "\uFFFD", true, true},
		{"koi8-r", "a\xc1b", "a\u0430b", true, false},
		{"utf-7", "a\xc1b", "a\uFFFDb", false, true},
		{"utf-8*en", "x", "x", true, false},
	} {
		got, res := DecodeCharset(c.label, []byte(c.in))
		if got != c.want || res.Known != c.known || res.Replaced != c.replaced {
			t.Errorf("DecodeCharset(%q, %q) = %q %+v; want %q known=%v replaced=%v", c.label, c.in, got, res, c.want, c.known, c.replaced)
		}
	}
}

func TestCodePages(t *testing.T) {
	cases := []struct{ label, in, want string }{
		{"windows-1251", "\xcf\xf0\xe8\xe2\xe5\xf2", "\U0000041f\U00000440\U00000438\U00000432\U00000435\U00000442"},
		{"cp1250", "\xa3\xf3d\x9f", "\U00000141\U000000f3d\U0000017a"},
		{"ISO-8859-9", "\xdd\xfe", "\U00000130\U0000015f"},
		{"macintosh", "caf\x8e", "caf\U000000e9"},
		{"windows-1253", "\xe1", "\U000003b1"},
		{"tis-620", "\xa1", "\U00000e01"},
	}
	for _, c := range cases {
		got, res := DecodeCharset(c.label, []byte(c.in))
		if got != c.want || !res.Known || res.Replaced || !KnownCharset(c.label) {
			t.Errorf("%s: %q %+v, want %q", c.label, got, res, c.want)
		}
	}
	// A byte the mapping leaves undefined: in 0x80 to 0x9F the C1
	// control (WHATWG), above it U+FFFD.
	if got, res := DecodeCharset("windows-1250", []byte("a\x81")); got != "a\u0081" || res.Replaced {
		t.Errorf("undefined byte: %q %+v", got, res)
	}
	if got, res := DecodeCharset("windows-1253", []byte("a\xaa")); got != "a\uFFFD" || !res.Replaced {
		t.Errorf("undefined byte: %q %+v", got, res)
	}
	// Decoded by go-fast-core's charset, which has the WHATWG Encoding
	// Standard's tables; UTF-7 is not.
	for _, l := range []string{"koi8-r", "iso-8859-2", "iso-8859-5", "iso-8859-7", "big5-hkscs"} {
		if !KnownCharset(l) {
			t.Errorf("%s is not known", l)
		}
	}
	if KnownCharset("utf-7") {
		t.Error("utf-7 is known")
	}
}
