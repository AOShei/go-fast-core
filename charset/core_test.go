package charset

import (
	"bytes"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

const ffd = "\uFFFD"

func decodeAll(t *testing.T, src []byte, enc Encoding) (string, int) {
	t.Helper()
	out, _, invalid, cut := Decode(src, Result{Enc: enc}, 1<<40)
	if cut {
		t.Fatal("cut")
	}
	return string(out), invalid
}

// The cases of go-fast-email's ISO-2022-JP test, where the decoder came
// from.
func TestISO2022JP(t *testing.T) {
	for _, l := range []string{"iso-2022-jp", "csiso2022jp", "ISO-2022-JP"} {
		if e, ok := Lookup(l); !ok || e != ISO2022JP {
			t.Errorf("Lookup(%q) = %v %v", l, e, ok)
		}
	}
	for _, c := range []struct {
		in, want string
		invalid  bool
	}{
		{"a\x1b$B$\"$$\x1b(Bb", "aあいb", false},
		{"\x1b$@$\"\x1b(B", "あ", false},
		{"\x1b(J\\~a\x1b(B\\~", "¥‾a\\~", false}, // JIS-Roman, then ASCII
		{"\x1b(I1\x1b(B", "ｱ", false},
		{"\x1b$B$\"\nx", "あ" + ffd + ffd, true}, // not a pair: both errors
		{"\x1b$B$", ffd, true},                  // unpaired byte at the end
		{"a\x1b", "a" + ffd, true},              // truncated escape
		{"a\x1b$", "a" + ffd + "$", true},       // truncated escape, $ read again
		{"a\x1b(Zx", "a" + ffd + "(Zx", true},   // unknown escape
		{"\x1b$B\x1b(Ba", ffd + "a", true},      // two escapes with nothing between
		{"a\x80b\x0ec", "a" + ffd + "b" + ffd + "c", true},
		{"\x1b(I\x60", ffd, true},
	} {
		got, invalid := decodeAll(t, []byte(c.in), ISO2022JP)
		if got != c.want || (invalid > 0) != c.invalid {
			t.Errorf("%q: %q (%d invalid), want %q", c.in, got, invalid, c.want)
		}
	}
}

func TestGBKEuro(t *testing.T) {
	if got, invalid := decodeAll(t, []byte("a\x80b"), GBK); got != "a€b" || invalid != 0 {
		t.Errorf("GBK 0x80: %q %d", got, invalid)
	}
}

func TestCodePage(t *testing.T) {
	for _, c := range []struct {
		cp  int
		enc Encoding
	}{
		{874, Windows874}, {932, ShiftJIS}, {936, GBK}, {949, EUCKR}, {950, Big5},
		{1250, Windows1250}, {1252, Windows1252}, {1258, Windows1258},
		{10000, Macintosh}, {65001, UTF8}, {1200, UTF16LE}, {54936, GB18030},
		{50220, ISO2022JP}, {28605, ISO8859_15},
	} {
		if e, ok := CodePage(c.cp); !ok || e != c.enc {
			t.Errorf("CodePage(%d) = %v %v", c.cp, e, ok)
		}
		if cp := c.enc.CodePage(); cp != c.cp {
			t.Errorf("%v.CodePage() = %d, want %d", c.enc, cp, c.cp)
		}
	}
	if _, ok := CodePage(437); ok {
		t.Error("437 decoded")
	}
	if ForCharset(128) != 932 || ForCharset(2) != Symbol || ForCharset(1) != 0 {
		t.Error("ForCharset")
	}
}

// samples are inputs in every encoding: the texts of testdata/detect,
// read in each encoding (most of them not what they were written in, so
// that undefined bytes and broken pairs are covered too), and a few made
// for the multi-byte encodings.
func samples(t *testing.T) [][]byte {
	files, err := filepath.Glob("testdata/detect/*")
	if err != nil || len(files) == 0 {
		t.Fatal("no testdata/detect", err)
	}
	var out [][]byte
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return append(out,
		[]byte("a\x1b$B$\"$$\x1b(Bb\x1b(J\\~\x1b(I1\x1b$B\x1b(B\x1b"),
		[]byte("\x95\x32\x82\x36\x81\x30\x84\x31\xa5\x30\x81"),
		[]byte("\x8f\xb0\xa1\x8e\xb1\xa4\x8f"),
		[]byte("\xf0\x9f\x98\x80\xe2\x82\xac\xc3\xa9\xe2\x82"),
		[]byte("\x3d\xd8\x00\xde\x41\x00\x00\xd8"),
		[]byte("\x88\x62\x88\x64\xa4\x40\x80\xfe"),
	)
}

// TestPiecesAgree: Decoder.Decode over any split, Decoder.Byte, Reader and
// Map.Source all agree with Decode.
func TestPiecesAgree(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	encs := make([]Encoding, 0, len(names))
	for e := range Encoding(len(names)) {
		encs = append(encs, e)
	}
	for _, src := range samples(t) {
		for _, enc := range encs {
			want, invalid := decodeAll(t, src, enc)
			if !utf8.ValidString(want) {
				t.Fatalf("%v: invalid UTF-8", enc)
			}

			// Pieces of random sizes.
			var d Decoder
			d.Reset(enc)
			var got []byte
			bad, s := 0, 0
			for s < len(src) {
				end := min(len(src), s+1+rng.IntN(7))
				var used, n int
				got, used, n = d.Decode(got, src[s:end], end == len(src))
				bad += n
				if used == 0 && end < len(src) {
					// Nothing complete yet: offer more next time.
					for end < len(src) && used == 0 {
						end++
						got, used, n = d.Decode(got, src[s:end], end == len(src))
						bad += n
					}
				}
				s += used
			}
			if string(got) != want || bad != invalid {
				t.Fatalf("%v pieces: %q (%d), want %q (%d)", enc, got, bad, want, invalid)
			}

			// A byte at a time.
			d.Reset(enc)
			var sb strings.Builder
			bad = 0
			emit := func(r rune, ok bool) {
				sb.WriteRune(r)
				if !ok {
					bad++
				}
			}
			for _, b := range src {
				d.Byte(b, emit)
			}
			d.Flush(emit)
			if sb.String() != want || bad != invalid {
				t.Fatalf("%v bytes: %q (%d), want %q (%d)", enc, sb.String(), bad, want, invalid)
			}

			// A Reader over a reader that gives a byte or two at a time.
			r := NewReader(&trickle{src: src}, enc)
			all, err := io.ReadAll(r)
			if err != nil || string(all) != want || r.Invalid() != invalid {
				t.Fatalf("%v reader: %q %v (%d), want %q", enc, all, err, r.Invalid(), want)
			}

			// Each character start maps back to where it is in the source,
			// or to the start of escape sequences (no characters) just
			// before it.
			_, m, _, _ := Decode(src, Result{Enc: enc}, 1<<40)
			var st state
			dec, run := 0, 0
			for s := 0; s < len(src); {
				if got := m.Source(dec); got < int64(run) || got > int64(s) {
					t.Fatalf("%v: Source(%d) = %d, want %d to %d", enc, dec, got, run, s)
				}
				dn, sn := st.step(src, s, enc)
				if dn > 0 {
					run = s + sn
				}
				dec, s = dec+dn, s+sn
			}
		}
	}
}

type trickle struct {
	src []byte
	n   int
}

func (r *trickle) Read(p []byte) (int, error) {
	if len(r.src) == 0 {
		return 0, io.EOF
	}
	r.n++
	k := min(len(p), len(r.src), 1+r.n%2)
	copy(p, r.src[:k])
	r.src = r.src[k:]
	return k, nil
}

func FuzzPiecesAgree(f *testing.F) {
	for _, s := range []string{"\x1b$B$\"\x1b(J\\\x1b(I1\x1b", "\x95\x32\x82\x36\x81", "\x88\x62\x88\x64", "\xe2\x82"} {
		f.Add([]byte(s), uint8(ISO2022JP))
	}
	f.Fuzz(func(t *testing.T, src []byte, e uint8) {
		enc := Encoding(int(e) % len(names))
		want, _ := decodeAll(t, src, enc)
		var d Decoder
		d.Reset(enc)
		var sb strings.Builder
		for _, b := range src {
			d.Byte(b, func(r rune, _ bool) { sb.WriteRune(r) })
		}
		d.Flush(func(r rune, _ bool) { sb.WriteRune(r) })
		if sb.String() != want {
			t.Fatalf("%v: %q, want %q", enc, sb.String(), want)
		}
		all, _ := io.ReadAll(NewReader(bytes.NewReader(src), enc))
		if string(all) != want {
			t.Fatalf("%v reader: %q, want %q", enc, all, want)
		}
	})
}

func TestDecodeCodePage(t *testing.T) {
	for _, c := range []struct {
		cp        int
		in, want  string
		undecoded int
	}{
		{1252, "caf\xe9 \x80", "café €", 0},
		{1252, "\x81", "\u0081", 0}, // undefined: the C1 control, as WHATWG and Windows give
		{932, "\x82\xa0x", "あx", 0},
		{932, "\x82", "\uFFFD", 1},   // a lead byte at the end
		{437, "a\xb0", "a\uFFFD", 1}, // not decoded here
		{Symbol, "a\xb7\xb8", "a•\uFFFD", 1},
		{65001, "\xe2\x82\xac", "€", 0},
	} {
		if got, n := DecodeCodePage(c.cp, []byte(c.in)); got != c.want || n != c.undecoded {
			t.Errorf("DecodeCodePage(%d, %q) = %q %d, want %q %d", c.cp, c.in, got, n, c.want, c.undecoded)
		}
	}
	// A character half read when the code page changes is not one.
	var p PageDecoder
	var got []rune
	emit := func(r rune, _ bool) { got = append(got, r) }
	p.Byte(932, 0x82, emit)
	p.Byte(1252, 'a', emit)
	p.Flush(emit)
	if string(got) != "\uFFFDa" {
		t.Errorf("code page change: %q", string(got))
	}
}

// The table PageDecoder reads windows-1252 by gives what win1252 gives.
func TestCP1252TableIsWin1252(t *testing.T) {
	for b := 0x80; b <= 0xFF; b++ {
		r := rune(cp1252[b-0x80])
		if r == 0 && b < 0xA0 {
			r = rune(b)
		}
		if r != win1252(byte(b)) {
			t.Errorf("0x%X: %U, win1252 %U", b, r, win1252(byte(b)))
		}
	}
}
