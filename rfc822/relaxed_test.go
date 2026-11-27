package rfc822

import (
	"fmt"
	"testing"
)

func TestParamsRelaxed(t *testing.T) {
	for _, c := range []struct {
		in, value string
		params    map[string]string
	}{
		{`Multipart/Related; type="text/html"; boundary="----=_Next \"q\""`, "multipart/related", map[string]string{"type": "text/html", "boundary": `----=_Next "q"`}},
		{"text/html;charset=UTF-8;;charset=x; bad; =v; name", "text/html", map[string]string{"charset": "UTF-8"}},
		{`text/html; a="unclosed`, "text/html", map[string]string{"a": "unclosed"}},
		{"text/plain", "text/plain", map[string]string{}},
	} {
		v, p := ParamsRelaxed([]byte(c.in))
		if v != c.value || fmt.Sprint(p) != fmt.Sprint(c.params) {
			t.Errorf("ParamsRelaxed(%q) = %q %v, want %q %v", c.in, v, p, c.value, c.params)
		}
	}
}

func TestSplitAnywhere(t *testing.T) {
	m := SplitAnywhere([]byte("pre--b\r\nA\r\n\r\none--b\r\nB\r\n\r\ntwo\r\n--b--\r\nepi"), "b", 10)
	if len(m.Parts) != 2 || !m.Closed || string(m.Parts[0]) != "A\r\n\r\none" || string(m.Epilogue) != "epi" {
		t.Fatalf("%q", m.Parts)
	}
}

// FuzzMIME runs the header and parameter readers, the splitters and the
// transfer decoders on arbitrary bytes: they must not panic or grow what
// they are given.
func FuzzMIME(f *testing.F) {
	f.Add([]byte("A: 1\n  2\nContent-Type: multipart/related; boundary=b\n\npre\n--b\nx\n--b--\npost=3D=\n"), "b")
	f.Fuzz(func(t *testing.T, data []byte, boundary string) {
		h, at, _ := ParseHeader(data, 4096)
		if at < 0 || at > len(data) {
			t.Fatalf("body at %d of %d", at, len(data))
		}
		for _, field := range h {
			ParamsRelaxed(field.Value)
		}
		if len(Base64(data)) > len(data) || len(QuotedPrintable(data)) > len(data)+1 {
			t.Fatal("decoding grew the input")
		}
		if boundary == "" {
			return
		}
		for _, m := range []Multipart{Split(data, boundary, 100), SplitAnywhere(data, boundary, 100)} {
			total := len(m.Preamble) + len(m.Epilogue)
			for _, p := range m.Parts {
				total += len(p)
			}
			if total > len(data) || len(m.Parts) > 100 {
				t.Fatalf("split gave %d bytes in %d parts from %d", total, len(m.Parts), len(data))
			}
		}
	})
}
