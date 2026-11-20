package gofastcore

import (
	"encoding/json"
	"strings"
	"testing"
)

func breakDoc() *Document {
	d := New("html")
	d.Parts = []Part{{Kind: PartBody, Number: 1, Blocks: []Block{
		{Kind: KindParagraph, Text: "The storm passed."},
		{Kind: KindThematicBreak},
		{Kind: KindParagraph, Text: "Morning came."},
	}}}
	d.Parts[0].Finish()
	return d
}

func TestThematicBreak(t *testing.T) {
	d := breakDoc()
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got, want := d.Markdown(), "The storm passed.\n\n---\n\nMorning came.\n"; got != want {
		t.Errorf("markdown: got %q, want %q", got, want)
	}
	if got, want := d.Text(), "The storm passed.\n\nMorning came.\n"; got != want {
		t.Errorf("text: got %q, want %q", got, want)
	}

	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `{"kind":"thematic_break"}`) {
		t.Errorf("JSON lacks a bare thematic_break block: %s", b)
	}
	var back Document
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if k := back.Parts[0].Blocks[1].Kind; k != KindThematicBreak {
		t.Errorf("round trip: kind %v", k)
	}
}

// A break after a list item ends the list in Markdown; it must not be
// read as a setext underline of the paragraph before it.
func TestThematicBreakAfterListAndHeadingSpacing(t *testing.T) {
	d := New("md")
	d.Parts = []Part{{Kind: PartBody, Number: 1, Blocks: []Block{
		{Kind: KindListItem, Level: 1, Text: "one"},
		{Kind: KindThematicBreak},
		{Kind: KindHeading, Level: 2, Text: "Next"},
	}}}
	d.Parts[0].Finish()
	if got, want := d.Markdown(), "- one\n\n---\n\n## Next\n"; got != want {
		t.Errorf("markdown: got %q, want %q", got, want)
	}
}

func TestThematicBreakHidden(t *testing.T) {
	d := breakDoc()
	d.Parts[0].Blocks[1].Display = DisplayHidden
	d.Parts[0].Warnings = nil
	d.Parts[0].Finish()
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	var md strings.Builder
	if err := d.WriteMarkdown(&md, &RenderOptions{ExcludeDisplay: DisplayHidden}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(md.String(), "---") {
		t.Errorf("hidden break rendered: %q", md.String())
	}
	if !strings.Contains(d.Markdown(), "---") {
		t.Error("hidden break left out without ExcludeDisplay")
	}
}

func TestValidateRejectsBadThematicBreak(t *testing.T) {
	cases := map[string]func(*Block){
		"text":      func(b *Block) { b.Text = "* * *" },
		"spans":     func(b *Block) { b.Spans = []Span{{Start: 0, End: 0, Style: StyleBold}} },
		"continued": func(b *Block) { b.Continued = true },
		"table":     func(b *Block) { b.Table = &Table{Rows: [][]string{{"x"}}} },
	}
	for name, mutate := range cases {
		d := breakDoc()
		mutate(&d.Parts[0].Blocks[1])
		if d.Validate() == nil {
			t.Errorf("%s: Validate accepted a thematic break with %s", name, name)
		}
	}
}
