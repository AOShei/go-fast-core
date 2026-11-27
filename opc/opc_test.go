package opc

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core/xmlscan"
)

var noLimits = Limits{MaxDecodedBytes: math.MaxInt64}

type zm struct {
	name, body string
	encrypted  bool
}

func zipOf(t *testing.T, members ...zm) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, m := range members {
		h := &zip.FileHeader{Name: m.name, Method: zip.Deflate}
		if m.encrypted {
			h.Flags |= 1
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, m.body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func open(t *testing.T, data []byte, lim Limits) *Package {
	t.Helper()
	p, err := Open(bytes.NewReader(data), int64(len(data)), lim)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func readPart(t *testing.T, p *Package, name string) string {
	t.Helper()
	rc, err := p.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const types = `<Types xmlns="` + ContentTypesNS + `">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="XML" ContentType="application/xml"/>` +
	`<Override PartName="/Documents/1/Pages/1.fpage" ContentType="application/vnd.ms-package.xps-fixedpage+xml"/></Types>`

const rels = `<Relationships xmlns="` + RelationshipsNS + `">` +
	`<Relationship Id="r1" Type="http://schemas.microsoft.com/xps/2005/06/fixedrepresentation" Target="/FixedDocSeq.fdseq"/>` +
	`<Relationship Id="r2" Type="hyperlink" Target=" https://example.org/a b " TargetMode="External"/></Relationships>`

func TestCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"word/document.xml":     "word/document.xml",
		"/word/document.xml":    "word/document.xml",
		"Word/Document.XML":     "word/document.xml",
		`word\media\image1.png`: "word/media/image1.png",
		"word/my%20file.xml":    "word/my file.xml",
		"word/100%.xml":         "word/100%.xml",
		"word/caf%C3%A9.xml":    "word/café.xml",
		"[Content_Types].xml":   "[content_types].xml",
		"":                      "",
		"//word/x.xml":          "word/x.xml",
		"word/été.xml":          "word/été.xml",
	} {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	for _, c := range []struct{ source, target, want string }{
		{"", "word/document.xml", "word/document.xml"},
		{"", "/word/document.xml", "word/document.xml"},
		{"word/document.xml", "styles.xml", "word/styles.xml"},
		{"word/document.xml", "media/image1.png", "word/media/image1.png"},
		{"word/document.xml", "../customXml/item1.xml", "customxml/item1.xml"},
		{"word/document.xml", "/word/numbering.xml", "word/numbering.xml"},
		{"ppt/slides/slide1.xml", "../slideLayouts/slideLayout1.xml", "ppt/slidelayouts/slidelayout1.xml"},
		{"ppt/slides/slide1.xml", "../media/image1.png#frag", "ppt/media/image1.png"},
		{"xl/workbook.xml", `worksheets\sheet1.xml`, "xl/worksheets/sheet1.xml"},
		{"xl/workbook.xml", "./worksheets/./sheet1.xml", "xl/worksheets/sheet1.xml"},
		// A target cannot leave the package.
		{"word/document.xml", "../../../etc/passwd", "etc/passwd"},
		{"", "../x.xml", "x.xml"},
		{"word/document.xml", "..", ""},
	} {
		if got := Resolve(c.source, c.target); got != c.want {
			t.Errorf("Resolve(%q, %q) = %q, want %q", c.source, c.target, got, c.want)
		}
	}
	if RelsName("") != "_rels/.rels" || RelsName("word/document.xml") != "word/_rels/document.xml.rels" {
		t.Error("RelsName is wrong")
	}
}

func TestContentTypesAndRels(t *testing.T) {
	p := open(t, zipOf(t,
		zm{name: "[Content_Types].xml", body: types},
		zm{name: "_rels/.rels", body: rels},
		zm{name: "FixedDocSeq.fdseq", body: "<x/>"},
		zm{name: "Documents/1/Pages/1.fpage", body: "<FixedPage/>"},
	), noLimits)
	if !p.Typed() {
		t.Error("not typed")
	}
	if got := p.ContentType("documents/1/pages/1.FPAGE"); got != "application/vnd.ms-package.xps-fixedpage+xml" {
		t.Errorf("override: %q", got)
	}
	if got := p.ContentType("FixedDocSeq.xml"); got != "application/xml" {
		t.Errorf("default by extension, any case: %q", got)
	}
	if ct, ok := p.Override("/Documents/1/Pages/1.fpage"); !ok || ct == "" {
		t.Error("Override")
	}
	got, err := p.Rels("")
	want := []Rel{
		{ID: "r1", Type: "http://schemas.microsoft.com/xps/2005/06/fixedrepresentation", Target: "fixeddocseq.fdseq"},
		{ID: "r2", Type: "hyperlink", Target: "https://example.org/a b", External: true},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Rels = %+v, %v", got, err)
	}
	if !p.HasContentType("application/xml") || p.HasContentType("image/png") {
		t.Error("HasContentType")
	}
	if p.Written("documents/1/pages/1.fpage") != "Documents/1/Pages/1.fpage" {
		t.Errorf("Written: %q", p.Written("documents/1/pages/1.fpage"))
	}
}

func TestUntypedAndMimetype(t *testing.T) {
	p := open(t, zipOf(t,
		zm{name: "mimetype", body: "application/vnd.oasis.opendocument.text\n"},
		zm{name: "content.xml", body: "<x/>"},
	), noLimits)
	if p.Typed() || p.Mimetype() != "application/vnd.oasis.opendocument.text" {
		t.Errorf("typed %v, mimetype %q", p.Typed(), p.Mimetype())
	}
	if _, err := Open(bytes.NewReader([]byte("not a zip")), 9, noLimits); !errors.Is(err, ErrNotZip) {
		t.Errorf("not a zip: %v", err)
	}
}

// Interleaved parts (ECMA-376 Part 2, 10.2.4): the pieces, in any order
// in the archive, make one part under the folder's name.
func TestInterleavedParts(t *testing.T) {
	p := open(t, zipOf(t,
		zm{name: "[Content_Types].xml", body: types},
		zm{name: "Documents/1/Pages/1.fpage/[1].piece", body: "two "},
		zm{name: "a.xml", body: "<a/>"},
		zm{name: "Documents/1/Pages/1.fpage/[0].piece", body: "one "},
		zm{name: "Documents/1/Pages/1.fpage/[2].last.piece", body: "three"},
		// No last piece: left as it is.
		zm{name: "b.xml/[0].piece", body: "b0"},
		zm{name: "b.xml/[1].piece", body: "b1"},
		// A gap.
		zm{name: "c.xml/[0].piece", body: "c0"},
		zm{name: "c.xml/[2].last.piece", body: "c2"},
		// One piece, the last.
		zm{name: "d.xml/[0].last.piece", body: "d"},
	), noLimits)
	if got := readPart(t, p, "documents/1/pages/1.fpage"); got != "one two three" {
		t.Errorf("part: %q", got)
	}
	if p.Size("Documents/1/Pages/1.fpage") != int64(len("one two three")) {
		t.Errorf("size %d", p.Size("Documents/1/Pages/1.fpage"))
	}
	if ct := p.ContentType("Documents/1/Pages/1.fpage"); ct != "application/vnd.ms-package.xps-fixedpage+xml" {
		t.Errorf("content type %q", ct)
	}
	if p.Written("documents/1/pages/1.fpage") != "Documents/1/Pages/1.fpage" {
		t.Errorf("Written %q", p.Written("documents/1/pages/1.fpage"))
	}
	if readPart(t, p, "d.xml") != "d" {
		t.Error("single last piece")
	}
	if p.BrokenPieces() != 2 || p.Has("b.xml") || !p.Has("b.xml/[0].piece") || p.Has("c.xml") {
		t.Errorf("broken %d", p.BrokenPieces())
	}
	wantNames := []string{"[content_types].xml", "documents/1/pages/1.fpage", "a.xml",
		"b.xml/[0].piece", "b.xml/[1].piece", "c.xml/[0].piece", "c.xml/[2].last.piece", "d.xml"}
	if !reflect.DeepEqual(p.Names(), wantNames) {
		t.Errorf("names %q", p.Names())
	}
	// The budget is charged across pieces.
	p = open(t, zipOf(t,
		zm{name: "x/[0].piece", body: strings.Repeat("a", 100)},
		zm{name: "x/[1].last.piece", body: strings.Repeat("b", 100)},
	), Limits{MaxDecodedBytes: 150})
	rc, _ := p.Open("x")
	b, err := io.ReadAll(rc)
	if len(b) != 150 || !errors.Is(err, ErrBudget) {
		t.Errorf("budget: %d %v", len(b), err)
	}
}

func TestPieceNames(t *testing.T) {
	for name, want := range map[string]string{
		"a/[0].piece":           "a",
		"a/b/[12].last.piece":   "a/b",
		"[0].piece":             "",
		"a/[x].piece":           "",
		"a/[-1].piece":          "",
		"a/[01].piece":          "",
		"a/[].piece":            "",
		"a/[0].last.last.piece": "",
	} {
		part, _, ok := pieceOf(name)
		if ok != (want != "") || part != want {
			t.Errorf("pieceOf(%q) = %q %v", name, part, ok)
		}
	}
}

func TestBudgetAndEncrypted(t *testing.T) {
	p := open(t, zipOf(t,
		zm{name: "a.xml", body: strings.Repeat("x", 1000)},
		zm{name: "e.xml", body: "secret", encrypted: true},
	), Limits{MaxDecodedBytes: 600})
	rc, _ := p.Open("a.xml")
	b, err := io.ReadAll(rc)
	if len(b) != 600 || !errors.Is(err, ErrBudget) {
		t.Errorf("budget: %d %v", len(b), err)
	}
	if _, err := p.Open("e.xml"); !errors.Is(err, ErrEncryptedMember) {
		t.Errorf("encrypted: %v", err)
	}
	if _, err := p.Open("nothing.xml"); !errors.Is(err, ErrNotFound) {
		t.Errorf("not found: %v", err)
	}
}

func TestFromParts(t *testing.T) {
	body := `<w:document><w:body/></w:document>`
	p := FromParts([]Part{
		{Name: "/word/document.xml", ContentType: "application/x-doc", Size: int64(len(body)),
			Open:     func() io.ReadCloser { return io.NopCloser(strings.NewReader(body)) },
			Bindings: []xmlscan.Binding{{Prefix: "w", URI: "urn:w"}}},
		{Name: "word/Document.xml", ContentType: "dup"},
	}, noLimits)
	if p.Ambiguous() != 1 || p.ContentType("word/document.xml") != "application/x-doc" {
		t.Errorf("ambiguous %d, type %q", p.Ambiguous(), p.ContentType("word/document.xml"))
	}
	// Written gives the name as the part gives it, not its canonical form;
	// the second part of the name is not a part.
	if w := p.Written("WORD/DOCUMENT.XML"); w != "word/document.xml" {
		t.Errorf("Written %q", w)
	}
	q := FromParts([]Part{{Name: "\\Media\\Image1.PNG"}, {Name: "plain.xml"}}, noLimits)
	if w := q.Written("media/image1.png"); w != "Media/Image1.PNG" {
		t.Errorf("Written %q", w)
	}
	if w := q.Written("plain.xml"); w != "plain.xml" {
		t.Errorf("Written %q", w)
	}
	var s xmlscan.Scanner
	c, err := p.Scan("word/document.xml", &s, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if s.Next() != xmlscan.Start || string(s.URI()) != "urn:w" {
		t.Errorf("binding not applied: %q %q %v", s.Name(), s.URI(), s.Err())
	}
}

func TestWindowHoldsThePart(t *testing.T) {
	p := open(t, zipOf(t, zm{name: "a.xml", body: "<a/>"}), noLimits)
	if o := p.Window("a.xml", nil); o == nil || o.BufferSize != 4096 {
		t.Errorf("window %+v", o)
	}
	if o := p.Window("nothing", nil); o != nil {
		t.Errorf("window for nothing %+v", o)
	}
}
