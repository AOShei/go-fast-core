package gofastcore

import (
	"strings"
	"testing"
)

// StoredKinds picks stored data by kind, in ordinary parts and in the
// stored part; Stored still takes everything.
func TestRenderStoredKinds(t *testing.T) {
	d := New("html")
	d.Parts = []Part{
		{Kind: PartBody, Number: 1, Blocks: []Block{
			{Kind: KindParagraph, Text: "Visible text."},
			{Kind: KindNote, Text: "a markup comment", Attrs: map[string]string{AttrStored: StoredMarkupComment}},
			{Kind: KindNote, Text: "PAGE", Attrs: map[string]string{AttrNoteType: NoteField, AttrStored: StoredFieldCode}},
			{Kind: KindNote, Text: "a reviewer's comment", Attrs: map[string]string{AttrNoteType: NoteComment}},
		}},
		{Kind: PartStored, Number: 2, Blocks: []Block{
			{Kind: KindCode, Text: "alert(1)", Attrs: map[string]string{AttrStored: StoredMacro}},
			{Kind: KindNote, Text: "a comment in the head", Attrs: map[string]string{AttrStored: StoredMarkupComment}},
			{Kind: KindParagraph, Text: "kindless stored text"},
		}},
	}
	render := func(opts *RenderOptions) string {
		var sb strings.Builder
		d.WriteText(&sb, opts)
		return sb.String()
	}
	check := func(name, got string, want, not []string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q missing in %q", name, w, got)
			}
		}
		for _, w := range not {
			if strings.Contains(got, w) {
				t.Errorf("%s: %q present in %q", name, w, got)
			}
		}
	}
	all := []string{"Visible text.", "a markup comment", "PAGE", "a reviewer's comment", "alert(1)", "a comment in the head", "kindless stored text"}

	check("default", render(nil), []string{"Visible text.", "a reviewer's comment"},
		[]string{"a markup comment", "PAGE", "alert(1)", "a comment in the head", "kindless stored text"})
	check("comments", render(&RenderOptions{StoredKinds: []string{StoredMarkupComment}, PartMarkers: true}),
		[]string{"Visible text.", "a reviewer's comment", "a markup comment", "a comment in the head", "[stored 2]"},
		[]string{"PAGE", "alert(1)", "kindless stored text"})
	check("fields", render(&RenderOptions{StoredKinds: []string{StoredFieldCode}, PartMarkers: true}),
		[]string{"PAGE"}, []string{"a markup comment", "alert(1)", "[stored 2]"})
	check("all", render(&RenderOptions{Stored: true, StoredKinds: []string{StoredFieldCode}}), all, nil)
}

func TestValidateFieldNoteStored(t *testing.T) {
	d := New("rtf")
	d.Parts = []Part{{Kind: PartBody, Number: 1, Blocks: []Block{
		{Kind: KindNote, Text: "PAGE", Attrs: map[string]string{AttrNoteType: NoteField}},
	}}}
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "field note") {
		t.Errorf("unmarked field note: %v", err)
	}
	d.Parts[0].Blocks[0].Attrs[AttrStored] = StoredFieldCode
	if err := d.Validate(); err != nil {
		t.Errorf("marked field note: %v", err)
	}
}

func TestValidateThematicBreakNative(t *testing.T) {
	d := New("rtf")
	d.Parts = []Part{{Kind: PartBody, Number: 1, Blocks: []Block{{Kind: KindThematicBreak, Origin: OriginInferred}}}}
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "never inferred") {
		t.Errorf("inferred break: %v", err)
	}
	d.Parts[0].Blocks[0].Origin = OriginNative
	if err := d.Validate(); err != nil {
		t.Errorf("native break: %v", err)
	}
}
