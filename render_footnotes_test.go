package gofastcore

import (
	"strings"
	"testing"
)

// footnotePages is a paragraph cut by a page break, with the first
// page's footnotes and footer between its two pieces, as go-fast-pdf
// gives it. mid is put between the footnotes and the footer of page 1;
// page 2 starts with a header and the continued block cont.
func footnotePages(t *testing.T, first, cont Block, mid ...Block) *Document {
	t.Helper()
	page1 := []Block{
		first,
		{Kind: KindFootnote, Marker: "1", Text: "A note."},
		{Kind: KindFootnote, Marker: "2", Text: "Another note."},
	}
	page1 = append(page1, mid...)
	page1 = append(page1, Block{Kind: KindFooter, Text: "12"})
	d := New("pdf")
	d.Parts = []Part{
		{Kind: PartPage, Number: 1, Blocks: page1},
		{Kind: PartPage, Number: 2, Blocks: []Block{
			{Kind: KindHeader, Text: "Running head"},
			cont,
		}},
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	return d
}

func renderBoth(d *Document, opts *RenderOptions) (text, md string) {
	var tb, mb strings.Builder
	d.WriteText(&tb, opts)
	d.WriteMarkdown(&mb, opts)
	return tb.String(), mb.String()
}

func TestContinuedPastFootnotes(t *testing.T) {
	para := Block{Kind: KindParagraph, Text: "A sentence that goes on to the"}
	cont := Block{Kind: KindParagraph, Text: "next page, where it ends.", Continued: true}
	item := Block{Kind: KindListItem, Level: 1, Text: "An item that goes on to the"}
	itemCont := Block{Kind: KindListItem, Level: 1, Text: "next page.", Continued: true}

	cases := []struct {
		name     string
		d        *Document
		opts     *RenderOptions
		text, md string
	}{
		{
			name: "paragraph joined, notes after it",
			d:    footnotePages(t, para, cont),
			text: "A sentence that goes on to the next page, where it ends.\n\n1 A note.\n\n2 Another note.\n",
			md:   "A sentence that goes on to the\nnext page, where it ends.\n\n[^1]: A note.\n\n[^2]: Another note.\n",
		},
		{
			name: "list item joined, notes after it",
			d:    footnotePages(t, item, itemCont),
			text: "- An item that goes on to the next page.\n\n1 A note.\n\n2 Another note.\n",
			md:   "- An item that goes on to the\n  next page.\n\n[^1]: A note.\n\n[^2]: Another note.\n",
		},
		{
			name: "not continued: as it stands",
			d:    footnotePages(t, para, Block{Kind: KindParagraph, Text: "A new paragraph."}),
			text: "A sentence that goes on to the\n\n1 A note.\n\n2 Another note.\n\nA new paragraph.\n",
			md:   "A sentence that goes on to the\n\n[^1]: A note.\n\n[^2]: Another note.\n\nA new paragraph.\n",
		},
		{
			name: "a heading between: not joined",
			d:    footnotePages(t, para, cont, Block{Kind: KindHeading, Level: 2, Text: "Heading"}),
			text: "A sentence that goes on to the\n\n1 A note.\n\n2 Another note.\n\nHeading\n\nnext page, where it ends.\n",
			md:   "A sentence that goes on to the\n\n[^1]: A note.\n\n[^2]: Another note.\n\n## Heading\n\nnext page, where it ends.\n",
		},
		{
			name: "a note between: only footnotes are passed",
			d:    footnotePages(t, para, cont, Block{Kind: KindNote, Text: "A comment."}),
			text: "A sentence that goes on to the\n\n1 A note.\n\n2 Another note.\n\nA comment.\n\nnext page, where it ends.\n",
		},
		{
			name: "furniture written between: not joined",
			d:    footnotePages(t, para, cont),
			opts: &RenderOptions{Furniture: true},
			text: "A sentence that goes on to the\n\n1 A note.\n\n2 Another note.\n\n12\n\nRunning head\n\nnext page, where it ends.\n",
		},
		{
			name: "part markers between: not joined",
			d:    footnotePages(t, para, cont),
			opts: &RenderOptions{PartMarkers: true},
			text: "[page 1]\n\nA sentence that goes on to the\n\n1 A note.\n\n2 Another note.\n\n[page 2]\n\nnext page, where it ends.\n",
		},
		{
			name: "a heading continued: not joinable",
			d: footnotePages(t, Block{Kind: KindHeading, Level: 1, Text: "Title"},
				Block{Kind: KindHeading, Level: 1, Text: "goes on", Continued: true}),
			text: "Title\n\n1 A note.\n\n2 Another note.\n\ngoes on\n",
		},
	}
	for _, c := range cases {
		text, md := renderBoth(c.d, c.opts)
		if text != c.text {
			t.Errorf("%s: text = %q, want %q", c.name, text, c.text)
		}
		if c.md != "" && md != c.md {
			t.Errorf("%s: Markdown = %q, want %q", c.name, md, c.md)
		}
	}
}

// A paragraph over three pages, each with a footnote, is one paragraph,
// and the notes follow it in order; a footnote that continues the one
// before it is joined to it as before.
func TestContinuedPastFootnotesOverPages(t *testing.T) {
	d := New("pdf")
	d.Parts = []Part{
		{Kind: PartPage, Number: 1, Blocks: []Block{
			{Kind: KindParagraph, Text: "One"},
			{Kind: KindFootnote, Marker: "1", Text: "First note"},
		}},
		{Kind: PartPage, Number: 2, Blocks: []Block{
			{Kind: KindParagraph, Text: "two", Continued: true},
			{Kind: KindFootnote, Text: "goes on.", Continued: true},
			{Kind: KindFootnote, Marker: "2", Text: "Second note."},
		}},
		{Kind: PartPage, Number: 3, Blocks: []Block{
			{Kind: KindParagraph, Text: "three.", Continued: true},
			{Kind: KindParagraph, Text: "Next."},
		}},
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	text, md := renderBoth(d, nil)
	if want := "One two three.\n\n1 First note goes on.\n\n2 Second note.\n\nNext.\n"; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	if want := "One\ntwo\nthree.\n\n[^1]: First note\ngoes on.\n\n[^2]: Second note.\n\nNext.\n"; md != want {
		t.Errorf("Markdown = %q, want %q", md, want)
	}
}

// Footnotes at the end of the document are written; a part rendered by
// itself is as before, having no block before it.
func TestHeldFootnotesAreWritten(t *testing.T) {
	d := footnotePages(t, Block{Kind: KindParagraph, Text: "Start"},
		Block{Kind: KindParagraph, Text: "end.", Continued: true})
	if got, want := d.Parts[0].Text(), "Start\n\n1 A note.\n\n2 Another note.\n"; got != want {
		t.Errorf("part 1 Text() = %q, want %q", got, want)
	}
	if got, want := d.Parts[1].Text(), "end.\n"; got != want {
		t.Errorf("part 2 Text() = %q, want %q", got, want)
	}
	// Children: the marker of a child writes what is held first.
	d.Parts = d.Parts[:1]
	d.Children = []*Document{New("txt")}
	var sb strings.Builder
	d.WriteText(&sb, &RenderOptions{Children: true})
	if got, want := sb.String(), "Start\n\n1 A note.\n\n2 Another note.\n\n[child 1: txt]\n"; got != want {
		t.Errorf("with a child = %q, want %q", got, want)
	}
}

// Footnotes held at the end of the last page are written when the stored
// part follows, also when only some of its kinds are wanted.
func TestHeldFootnotesBeforeStoredPart(t *testing.T) {
	d := New("pdf")
	d.Parts = []Part{
		{Kind: PartPage, Number: 1, Blocks: []Block{
			{Kind: KindParagraph, Text: "Body"},
			{Kind: KindFootnote, Marker: "1", Text: "A note."},
		}},
		{Kind: PartStored, Number: 2, Blocks: []Block{
			{Kind: KindCode, Text: "x=1", Attrs: map[string]string{AttrStored: StoredMacro}},
		}},
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []*RenderOptions{nil, {StoredKinds: []string{StoredMacro}}, {Stored: true}} {
		var sb strings.Builder
		d.WriteText(&sb, opts)
		want := "Body\n\n1 A note.\n"
		if opts != nil {
			want += "\nx=1\n"
		}
		if got := sb.String(); got != want {
			t.Errorf("%+v: %q, want %q", opts, got, want)
		}
	}
}
