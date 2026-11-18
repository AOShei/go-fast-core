package gofastcore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sample() *Document {
	d := New("pdf")
	d.HiddenChecked = HiddenChars
	d.Source.Name = "report.pdf"
	d.Metadata = Metadata{
		Title:   "Q3 Report",
		Authors: []string{"A. Writer"},
		Created: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		Extra:   map[string]string{"producer": "TeX"},
	}
	d.Outline = []OutlineItem{{Title: "Q3 Report", Level: 1, Part: 40, Y: 72}}
	d.Parts = []Part{
		{Kind: PartPage, Number: 40, Width: 612, Height: 792, Blocks: []Block{
			{Kind: KindHeader, Text: "ACME Confidential", Origin: OriginInferred},
			{Kind: KindHeading, Level: 1, Text: "Q3 Report",
				Loc: Location{BBox: Rect{72, 72, 300, 96}}},
			{Kind: KindParagraph, Text: "Revenue grew in every région.", Origin: OriginInferred,
				Spans: []Span{{Start: 0, End: 7, Style: StyleBold | StyleItalic}}},
			{Kind: KindListItem, Level: 1, Text: "EMEA up"},
			{Kind: KindListItem, Level: 2, Ordered: true, Marker: "a.", Text: "UK flat\nsecond line"},
			{Kind: KindTable, Table: &Table{
				HeaderRows: 1,
				Rows:       [][]string{{"Region", "Rev"}, {"EMEA", "1|2"}, {"APAC", "3\n4"}},
			}},
		}},
		{Kind: PartPage, Number: 42, Width: 612, Height: 792, Blocks: []Block{
			{Kind: KindImage, Text: "Revenue chart", Image: &Image{ID: "obj-12", Width: 800, Height: 600, MIME: "image/jpeg"}},
			{Kind: KindCode, Text: "x := 1", Attrs: map[string]string{"language": "go"}},
			{Kind: KindQuote, Level: 1, Text: "Strong quarter."},
			{Kind: KindFooter, Text: "2"},
		}},
	}
	d.Parts[1].Warn(SeverityWarning, WarnImageUnsupportedEncoding, "JBIG2 not decoded")
	child := New("xlsx")
	child.HiddenChecked = HiddenChars
	child.Source.Name = "data.xlsx"
	child.Parts = []Part{{Kind: PartSheet, Number: 1, Name: "Sheet1", Blocks: []Block{
		{Kind: KindTable, Loc: Location{Range: "A1:B2"}, Table: &Table{
			Rows:   [][]string{{"merged", ""}, {"a", "b"}},
			Merges: []Merge{{Row: 0, Col: 0, RowSpan: 1, ColSpan: 2}},
		}},
	}}}
	d.Children = []*Document{child}
	d.Warn(SeverityInfo, WarnInputRepaired, "xref rebuilt")
	return d
}

func TestSampleIsValid(t *testing.T) {
	if err := sample().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	want := sample()
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`"kind":"page"`, `"kind":"list_item"`, `"style":"bold+italic"`, `"schema_version":"0.1"`,
		`"origin":"inferred"`, `"severity":"warning"`, `"number":42`, `"outline":[`,
	} {
		if !strings.Contains(string(data), s) {
			t.Errorf("JSON lacks %s", s)
		}
	}
	// Zero-valued optional fields must not appear.
	for _, s := range []string{`"bbox":{"x0":0`, `"modified"`, `"origin":"native"`, `"continued"`} {
		if strings.Contains(string(data), s) {
			t.Errorf("zero value %s was serialized", s)
		}
	}
	got := new(Document)
	if err := json.Unmarshal(data, got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the document\n got: %+v\nwant: %+v", got, want)
	}
}

func TestUnknownNamesDecodeToUnknown(t *testing.T) {
	const in = `{"schema_version":"0.1","source":{"format":"pdf"},"parts":[
		{"kind":"hologram","number":1,
		 "warnings":[{"code":"x.y","severity":"catastrophic"}],
		 "blocks":[{"kind":"sidebar","text":"hi","origin":"divined",
		            "spans":[{"start":0,"end":2,"style":"bold+sparkly"}]}]}]}`
	var d Document
	if err := json.Unmarshal([]byte(in), &d); err != nil {
		t.Fatalf("newer document failed to decode: %v", err)
	}
	p := d.Parts[0]
	b := p.Blocks[0]
	if p.Kind != PartUnknown || b.Kind != KindUnknown || b.Origin != OriginUnknown ||
		p.Warnings[0].Severity != SeverityUnknown || b.Spans[0].Style != StyleBold {
		t.Errorf("got part=%v block=%v origin=%v severity=%v style=%v",
			p.Kind, b.Kind, b.Origin, p.Warnings[0].Severity, b.Spans[0].Style)
	}
	if p.Warnings[0].Severity < SeverityError {
		t.Error("unknown severity should sort at least as high as error")
	}
	// Still usable: it renders as a paragraph and re-encodes.
	if got := d.Text(); got != "hi\n" {
		t.Errorf("Text() = %q", got)
	}
	if _, err := json.Marshal(&d); err != nil {
		t.Errorf("re-encoding failed: %v", err)
	}
	// But a producer may not emit it.
	if d.Validate() == nil {
		t.Error("Validate accepted unknown enum values")
	}
}

func TestUnsetEnumsDoNotMarshal(t *testing.T) {
	if _, err := json.Marshal(Block{}); err == nil {
		t.Error("block with unset kind marshaled")
	}
	if _, err := json.Marshal(Warning{Code: "x"}); err == nil {
		t.Error("warning with unset severity marshaled")
	}
}

func TestMarkdown(t *testing.T) {
	got := sample().Markdown()
	want := `# Q3 Report

Revenue grew in every région.

- EMEA up
    a. UK flat
       second line

| Region | Rev |
| --- | --- |
| EMEA | 1\|2 |
| APAC | 3<br>4 |

![Revenue chart](obj-12)

` + "```go\nx := 1\n```" + `

> Strong quarter.
`
	if got != want {
		t.Errorf("markdown mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestText(t *testing.T) {
	got := sample().Text()
	want := "Q3 Report\n\nRevenue grew in every région.\n\n- EMEA up\n  a. UK flat\nsecond line\n\n" +
		"Region\tRev\nEMEA\t1|2\nAPAC\t3 4\n\nRevenue chart\n\nx := 1\n\nStrong quarter.\n"
	if got != want {
		t.Errorf("text mismatch\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

func TestRenderOptions(t *testing.T) {
	opts := &RenderOptions{Furniture: true, PartMarkers: true}
	var md, txt strings.Builder
	if err := sample().WriteMarkdown(&md, opts); err != nil {
		t.Fatal(err)
	}
	if err := sample().WriteText(&txt, opts); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(md.String(), "<!-- page 40 -->\n\nACME Confidential\n\n# Q3 Report") {
		t.Errorf("markdown starts %q", md.String()[:60])
	}
	if !strings.Contains(md.String(), "\n\n<!-- page 42 -->\n\n![Revenue chart]") {
		t.Error("markdown lacks second part marker with blank lines around it")
	}
	if !strings.HasPrefix(txt.String(), "[page 40]\n\nACME Confidential\n\n") || !strings.Contains(txt.String(), "\n\n[page 42]\n\n") {
		t.Errorf("text markers wrong: %q", txt.String())
	}
}

func TestEmptyDocumentRendersEmpty(t *testing.T) {
	d := New("txt")
	d.HiddenChecked = HiddenChars
	d.Parts = []Part{{Kind: PartBody, Number: 1, Blocks: []Block{}}}
	if d.Markdown() != "" || d.Text() != "" {
		t.Errorf("empty document rendered %q / %q", d.Markdown(), d.Text())
	}
}

func TestHeaderlessTableKeepsData(t *testing.T) {
	p := Part{Blocks: []Block{{Kind: KindTable, Table: &Table{Rows: [][]string{{"a", "b"}}}}}}
	want := "|  |  |\n| --- | --- |\n| a | b |\n"
	if got := p.Markdown(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarkdownEscapesLeadingMarkers(t *testing.T) {
	cases := map[string]string{
		"# not a heading":    `\# not a heading`,
		"#hashtag":           "#hashtag",
		"####### seven":      "####### seven",
		"> not a quote":      `\> not a quote`,
		"- not a list":       `\- not a list`,
		"-5 degrees":         "-5 degrees",
		"* star":             `\* star`,
		"*emphasis* stays":   "*emphasis* stays",
		"+ plus":             `\+ plus`,
		"1. not ordered":     `1\. not ordered`,
		"12) nor this":       `12\) nor this`,
		"3.14 is pi":         "3.14 is pi",
		"2026. A year.":      `2026\. A year.`,
		"---":                `\---`,
		"===":                `\===`,
		"* * *":              `\* * *`,
		"___":                `\___`,
		"snake_case_name":    "snake_case_name",
		"```":                "\\```",
		"    indented":       "indented",
		"line one\n# line 2": "line one\n\\# line 2",
		"a | b":              "a | b",
		"":                   "",
	}
	for in, want := range cases {
		p := Part{Blocks: []Block{{Kind: KindParagraph, Text: in}}}
		if got := strings.TrimSuffix(p.Markdown(), "\n"); got != want {
			t.Errorf("paragraph %q rendered %q, want %q", in, got, want)
		}
		// Plain text never escapes.
		if got := strings.TrimSuffix(p.Text(), "\n"); got != in {
			t.Errorf("text of %q rendered %q", in, got)
		}
	}
	// Inside a list item and a quote too.
	p := Part{Blocks: []Block{
		{Kind: KindListItem, Level: 1, Text: "# x"},
		{Kind: KindQuote, Level: 1, Text: "- y"},
	}}
	if got, want := p.Markdown(), "- \\# x\n\n> \\- y\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestContinuedBlocksAreJoined(t *testing.T) {
	d := New("pdf")
	d.HiddenChecked = HiddenChars
	d.Parts = []Part{
		{Kind: PartPage, Number: 1, Blocks: []Block{
			{Kind: KindParagraph, Text: "The sentence starts here and"},
			{Kind: KindFooter, Text: "1"},
		}},
		{Kind: PartPage, Number: 2, Blocks: []Block{
			{Kind: KindHeader, Text: "Title"},
			{Kind: KindParagraph, Text: "ends on the next page.", Continued: true},
			{Kind: KindListItem, Level: 1, Text: "item starts"},
		}},
		{Kind: PartPage, Number: 3, Blocks: []Block{
			{Kind: KindListItem, Level: 1, Text: "item ends", Continued: true},
			{Kind: KindHeading, Level: 2, Text: "Not joined", Continued: true},
		}},
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	wantText := "The sentence starts here and ends on the next page.\n\n- item starts item ends\n\nNot joined\n"
	if got := d.Text(); got != wantText {
		t.Errorf("Text() = %q, want %q", got, wantText)
	}
	wantMD := "The sentence starts here and\nends on the next page.\n\n- item starts\n  item ends\n\n## Not joined\n"
	if got := d.Markdown(); got != wantMD {
		t.Errorf("Markdown() = %q, want %q", got, wantMD)
	}
	// With part markers the pieces stay in their own parts.
	var sb strings.Builder
	d.WriteText(&sb, &RenderOptions{PartMarkers: true})
	if !strings.Contains(sb.String(), "starts here and\n\n[page 2]\n\nends on") {
		t.Errorf("part markers should separate continued blocks: %q", sb.String())
	}
}

func TestIterators(t *testing.T) {
	d := sample()
	n := 0
	for p, b := range d.AllBlocks() {
		if p == nil || b == nil {
			t.Fatal("nil from iterator")
		}
		n++
	}
	if n != 10 {
		t.Errorf("AllBlocks visited %d blocks, want 10", n)
	}
	var got []string
	for p, w := range d.AllWarnings() {
		where := "doc"
		if p != nil {
			where = fmt.Sprint(p.Number)
		}
		got = append(got, where+":"+w.Code)
	}
	want := []string{"doc:" + WarnInputRepaired, "42:" + WarnImageUnsupportedEncoding}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AllWarnings = %v, want %v", got, want)
	}
	if !d.Parts[1].Has(WarnImageUnsupportedEncoding) || d.Parts[0].Has(WarnPartFailed) {
		t.Error("Part.Has is wrong")
	}
	var formats []string
	d.Walk(func(doc *Document, depth int) bool {
		formats = append(formats, doc.Source.Format)
		return true
	})
	if !reflect.DeepEqual(formats, []string{"pdf", "xlsx"}) {
		t.Errorf("Walk visited %v", formats)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Document){
		"no parts":             func(d *Document) { d.Parts = nil },
		"no schema version":    func(d *Document) { d.SchemaVersion = "" },
		"no format":            func(d *Document) { d.Source.Format = "" },
		"numbers not rising":   func(d *Document) { d.Parts[1].Number = 40 },
		"number zero":          func(d *Document) { d.Parts[0].Number = 0 },
		"invalid part kind":    func(d *Document) { d.Parts[0].Kind = PartInvalid },
		"invalid block kind":   func(d *Document) { d.Parts[0].Blocks[0].Kind = KindInvalid },
		"unknown block kind":   func(d *Document) { d.Parts[0].Blocks[0].Kind = KindUnknown },
		"unknown origin":       func(d *Document) { d.Parts[0].Blocks[0].Origin = OriginUnknown },
		"heading level":        func(d *Document) { d.Parts[0].Blocks[1].Level = 0 },
		"invalid utf-8 text":   func(d *Document) { d.Parts[0].Blocks[2].Text = "bad \xff byte" },
		"invalid utf-8 author": func(d *Document) { d.Metadata.Authors[0] = "\xff" },
		"invalid utf-8 extra":  func(d *Document) { d.Metadata.Extra["producer"] = "\xff" },
		"invalid utf-8 attr":   func(d *Document) { d.Parts[1].Blocks[1].Attrs["language"] = "\xff" },
		"invalid utf-8 marker": func(d *Document) { d.Parts[0].Blocks[4].Marker = "\xff" },
		"invalid utf-8 href":   func(d *Document) { d.Parts[0].Blocks[2].Spans[0].Href = "\xff" },
		"invalid utf-8 cell":   func(d *Document) { d.Parts[0].Blocks[5].Table.Rows[1][0] = "\xff" },
		"span out of range":    func(d *Document) { d.Parts[0].Blocks[2].Spans[0].End = 999 },
		// "région": the é occupies bytes 23 and 24.
		"span splits a rune":  func(d *Document) { d.Parts[0].Blocks[2].Spans[0].End = 24 },
		"list level":          func(d *Document) { d.Parts[0].Blocks[3].Level = 0 },
		"table without data":  func(d *Document) { d.Parts[0].Blocks[5].Table = nil },
		"ragged table":        func(d *Document) { d.Parts[0].Blocks[5].Table.Rows[1] = []string{"x"} },
		"image on paragraph":  func(d *Document) { d.Parts[0].Blocks[2].Image = &Image{} },
		"flipped bbox":        func(d *Document) { d.Parts[0].Blocks[1].Loc.BBox = Rect{10, 10, 5, 5} },
		"warning no code":     func(d *Document) { d.Parts[0].Warn(SeverityError, "", "") },
		"warning no severity": func(d *Document) { d.Warnings[0].Severity = SeverityInvalid },
		"part warning utf-8":  func(d *Document) { d.Parts[1].Warnings[0].Message = "\xff" },
		"outline level":       func(d *Document) { d.Outline[0].Level = 0 },
		"nil child":           func(d *Document) { d.Children[0] = nil },
		"silent empty child":  func(d *Document) { d.Children[0].Parts = nil },
		"merge out of grid":   func(d *Document) { d.Children[0].Parts[0].Blocks[0].Table.Merges[0].ColSpan = 3 },
	}
	for name, mutate := range cases {
		d := sample()
		mutate(d)
		if d.Validate() == nil {
			t.Errorf("%s: Validate accepted an invalid document", name)
		}
	}
}

func TestEmptyListsMarshalAsArrays(t *testing.T) {
	d := New("pdf")
	d.HiddenChecked = HiddenChars
	d.Parts = append(d.Parts, Part{Kind: PartPage, Number: 1}) // a failed page: no blocks
	d.Parts[0].Warn(SeverityError, WarnPartFailed, "")
	d.Parts[0].MarkHidden()
	d.Children = []*Document{(*Options)(nil).ExtractChild(context.Background(), "a.bin", "", nil, 0)}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("JSON contains null: %s", data)
	}
	for _, s := range []string{`"blocks":[]`, `"parts":[]`} {
		if !strings.Contains(string(data), s) {
			t.Errorf("JSON lacks %s: %s", s, data)
		}
	}
	// Empty lists survive a round trip as empty lists.
	got := new(Document)
	if err := json.Unmarshal(data, got); err != nil {
		t.Fatal(err)
	}
	if again, _ := json.Marshal(got); string(again) != string(data) {
		t.Errorf("round trip changed the JSON:\n%s\n%s", data, again)
	}
	if empty, _ := json.Marshal(New("txt")); !strings.Contains(string(empty), `"parts":[]`) {
		t.Errorf("New() marshals as %s", empty)
	}

	// The model has no custom marshalers, so a nil list would be written
	// as null. Validate is what stops one from being produced.
	nilBlocks := New("pdf")
	nilBlocks.HiddenChecked = HiddenChars
	nilBlocks.Parts = []Part{{Kind: PartPage, Number: 1}}
	if nilBlocks.Validate() == nil {
		t.Error("nil blocks accepted")
	}
	nilParts := &Document{SchemaVersion: SchemaVersion, Source: Source{Format: "pdf"}}
	if err := nilParts.Validate(); err == nil || !strings.Contains(err.Error(), "parts is nil") {
		t.Errorf("nil parts: %v", err)
	}
	for name, table := range map[string]*Table{"no rows": {}, "no columns": {Rows: [][]string{{}}}} {
		e := New("pdf")
		e.HiddenChecked = HiddenChars
		e.Parts = []Part{{Kind: PartPage, Number: 1, Blocks: []Block{{Kind: KindTable, Table: table}}}}
		if e.Validate() == nil {
			t.Errorf("table with %s accepted", name)
		}
	}
}

func BenchmarkMarshal(b *testing.B) {
	d := New("pdf")
	d.HiddenChecked = HiddenChars
	for p := 1; p <= 100; p++ {
		part := Part{Kind: PartPage, Number: p, Width: 612, Height: 792}
		for i := range 12 {
			part.Blocks = append(part.Blocks, Block{
				Kind: KindParagraph, Origin: OriginInferred,
				Text: fmt.Sprintf("Paragraph %d on page %d. The quick brown fox jumps over the lazy dog, again and again, until the paragraph is a realistic length for body text.", i, p),
				Loc:  Location{BBox: Rect{72, float64(72 + i*50), 540, float64(110 + i*50)}},
			})
		}
		part.Blocks = append(part.Blocks, Block{Kind: KindTable, Origin: OriginInferred, Table: &Table{
			HeaderRows: 1, Rows: [][]string{{"Region", "Q1", "Q2"}, {"EMEA", "1.2", "1.4"}},
		}})
		part.MarkHidden()
		d.Parts = append(d.Parts, part)
	}
	out, err := json.Marshal(d)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for b.Loop() {
		json.Marshal(d)
	}
}

func TestValidateTextConventions(t *testing.T) {
	para := func(text string) *Document {
		d := New("pdf")
		d.HiddenChecked = HiddenChars
		d.Parts = []Part{{Kind: PartPage, Number: 1, Blocks: []Block{{Kind: KindParagraph, Text: text}}}}
		return d
	}
	for name, text := range map[string]string{
		"empty":            "",
		"leading space":    " x",
		"trailing space":   "x ",
		"trailing newline": "x\n",
		"only whitespace":  "  ",
		"nbsp at end":      "x\u00a0",
		"ligature":         "of\ufb01ce",
		"NUL":              "a\x00b",
		"form feed":        "a\fb",
		"carriage return":  "a\r\nb",
		"C1 control":       "a\u0085b",
	} {
		if para(text).Validate() == nil {
			t.Errorf("%s: Validate accepted paragraph text %q", name, text)
		}
	}
	for name, text := range map[string]string{
		"hard break": "line one\nline two",
		"inner tab":  "Name:\tValue",
		"non-ASCII":  "naïve café",
	} {
		if err := para(text).Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Code is verbatim: indentation, trailing newline and ligature
	// characters are allowed. Control characters and emptiness are not.
	code := func(text string) *Document {
		d := para("x")
		d.Parts[0].Blocks[0] = Block{Kind: KindCode, Text: text}
		return d
	}
	if err := code("\tif x {\n\t\ty := \"\ufb01\"\n\t}\n").Validate(); err != nil {
		t.Errorf("verbatim code rejected: %v", err)
	}
	if code("").Validate() == nil || code("a\x00").Validate() == nil {
		t.Error("empty code or code with NUL accepted")
	}

	// Images may have no alt text; cells may be empty or padded, but not
	// carry control characters or ligatures.
	d := para("x")
	d.Parts[0].Blocks = []Block{
		{Kind: KindImage, Image: &Image{ID: "obj-1"}},
		{Kind: KindTable, Table: &Table{Rows: [][]string{{"", " padded "}}}},
	}
	if err := d.Validate(); err != nil {
		t.Errorf("alt-less image or padded cell rejected: %v", err)
	}
	d.Parts[0].Blocks[1].Table.Rows[0][0] = "\ufb02at"
	if d.Validate() == nil {
		t.Error("ligature in a cell accepted")
	}

	// Control characters are rejected in every string, not only in blocks.
	d = para("x")
	d.Metadata.Title = "Report\x00"
	if d.Validate() == nil {
		t.Error("NUL in metadata accepted")
	}
}

func TestValidateAcceptsSpanAtRuneBoundary(t *testing.T) {
	d := sample()
	d.Parts[0].Blocks[2].Spans[0] = Span{Start: 22, End: 25, Style: StyleBold} // "ré"
	if err := d.Validate(); err != nil {
		t.Error(err)
	}
}

func TestPartRangeAndLimits(t *testing.T) {
	all := PartRange{}
	if !all.Contains(1) || !all.Contains(9999) {
		t.Error("zero PartRange should contain every part")
	}
	r := PartRange{First: 40, Last: 60}
	if r.Contains(39) || !r.Contains(40) || !r.Contains(60) || r.Contains(61) {
		t.Error("PartRange{40,60} bounds are wrong")
	}
	if open := (PartRange{First: 5}); open.Contains(4) || !open.Contains(1000) {
		t.Error("open-ended PartRange is wrong")
	}
	// An inverted range contains nothing; extractors report ErrNoPartsSelected.
	for n := range 10 {
		if (PartRange{First: 5, Last: 3}).Contains(n) {
			t.Errorf("inverted PartRange contains %d", n)
		}
	}
	if ResolveLimit(0, 7) != 7 || ResolveLimit(3, 7) != 3 || ResolveLimit(-1, 7) != math.MaxInt64 {
		t.Error("ResolveLimit is wrong")
	}
}

func TestExtractChild(t *testing.T) {
	ctx := context.Background()
	data := bytes.NewReader([]byte("hello"))

	// No options, no child extractor: listed, not extracted.
	var none *Options
	for _, o := range []*Options{none, {}} {
		d := o.ExtractChild(ctx, "a.docx", "application/x", data, 5)
		if len(d.Parts) != 0 || d.Source.Name != "a.docx" || d.Source.Size != 5 ||
			len(d.Warnings) != 1 || d.Warnings[0].Code != WarnChildNotExtracted {
			t.Errorf("stub is wrong: %+v", d)
		}
	}

	// A child extractor that recurses: depth is tracked and limited.
	var depths []int
	var opts *Options
	opts = &Options{
		Password: "pw",
		Parts:    PartRange{First: 3, Last: 4},
		Limits:   Limits{MaxDepth: 2},
		Child: func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *Options) (*Document, error) {
			depths = append(depths, o.Depth)
			if o.Password != "pw" || o.Name != name || o.Parts != (PartRange{}) {
				t.Errorf("child options not derived correctly: %+v", o)
			}
			d := New("txt")
			d.HiddenChecked = HiddenChars
			d.Parts = []Part{{Kind: PartBody, Number: 1, Blocks: []Block{}}}
			d.Children = append(d.Children, o.ExtractChild(ctx, "inner-of-"+name, "", r, size))
			return d, nil
		},
	}
	root := New("eml")
	root.HiddenChecked = HiddenChars
	root.Parts = []Part{{Kind: PartMessage, Number: 1, Blocks: []Block{}}}
	root.Children = append(root.Children, opts.ExtractChild(ctx, "a.txt", "text/plain", data, 5))
	if !reflect.DeepEqual(depths, []int{1, 2}) {
		t.Errorf("child depths = %v, want [1 2]", depths)
	}
	if opts.Depth != 0 {
		t.Error("ExtractChild modified the caller's options")
	}
	deepest := root.Children[0].Children[0].Children[0]
	if len(deepest.Parts) != 0 || deepest.Warnings[0].Code != WarnChildNotExtracted {
		t.Errorf("depth limit did not stop recursion: %+v", deepest)
	}
	if root.Children[0].Source.MIME != "text/plain" || root.Children[0].Source.Size != 5 {
		t.Errorf("child source not filled in: %+v", root.Children[0].Source)
	}
	if err := root.Validate(); err != nil {
		t.Errorf("tree with stub children should be valid: %v", err)
	}

	// A failing child extractor: listed, with a code that says why.
	for _, c := range []struct {
		err  error
		code string
	}{
		{ErrMalformed, WarnChildFailed},
		{errors.New("disk on fire"), WarnChildFailed},
		{ErrUnsupported, WarnChildUnsupported},
		{ErrPasswordRequired, WarnChildEncrypted},
		{ErrWrongPassword, WarnChildEncrypted},
	} {
		failing := &Options{Child: func(context.Context, string, string, io.ReaderAt, int64, *Options) (*Document, error) {
			return nil, fmt.Errorf("docx: %w", c.err)
		}}
		d := failing.ExtractChild(ctx, "bad.docx", "", data, 5)
		w := d.Warnings[0]
		if len(d.Parts) != 0 || w.Code != c.code || !strings.Contains(w.Message, c.err.Error()) {
			t.Errorf("error %v gave stub %+v, want code %s", c.err, w, c.code)
		}
		if err := d.Validate(); err == nil {
			t.Error("a stub is only valid as a child, not as a top-level document")
		}
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	all := []error{ErrPasswordRequired, ErrWrongPassword, ErrUnsupported, ErrMalformed, ErrLimitExceeded, ErrNoPartsSelected}
	for i, a := range all {
		wrapped := fmt.Errorf("pdf: %w", a)
		for j, b := range all {
			if errors.Is(wrapped, b) != (i == j) {
				t.Errorf("errors.Is(%v, %v) = %v", wrapped, b, i != j)
			}
		}
	}
}
