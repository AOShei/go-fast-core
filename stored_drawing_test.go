package gofastcore

import (
	"slices"
	"strings"
	"testing"
)

// A drawing's shape data stays in its page, after the shape's text; the
// data it links to is in the stored part. Both validate, and both are left
// out of rendering unless asked for by kind or with Stored.
func TestStoredShapeAndLinkedData(t *testing.T) {
	for _, k := range []string{StoredShapeData, StoredLinkedData} {
		if !slices.Contains(StoredKindNames(), k) {
			t.Fatalf("StoredKindNames lacks %s", k)
		}
	}
	d := New("vsdx")
	box := Location{BBox: Rect{X0: 72, Y0: 72, X1: 216, Y1: 144}}
	d.Parts = []Part{
		{Kind: PartPage, Number: 1, Name: "Network", Width: 792, Height: 612, Blocks: []Block{
			{Kind: KindParagraph, Text: "Router", Loc: box},
			{Kind: KindTable, Loc: box, Display: DisplayHidden,
				Attrs: map[string]string{AttrStored: StoredShapeData, AttrName: "Router.12"},
				Table: &Table{
					Rows:    [][]string{{"IP address", "10.0.0.1"}, {"Asset tag", "A-77"}},
					Display: []DisplayCells{{Row: 1, Col: 0, RowSpan: 1, ColSpan: 2, Display: DisplayHidden}},
				}},
		}},
		{Kind: PartStored, Number: 2, Blocks: []Block{
			{Kind: KindTable, Attrs: map[string]string{AttrStored: StoredLinkedData, AttrName: "Inventory"},
				Table: &Table{HeaderRows: 1, Rows: [][]string{{"Host", "Site"}, {"Router", "Paris"}}}},
		}},
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	text := func(opts *RenderOptions) string {
		var sb strings.Builder
		d.WriteText(&sb, opts)
		return sb.String()
	}
	md := func(opts *RenderOptions) string {
		var sb strings.Builder
		d.WriteMarkdown(&sb, opts)
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
	for name, render := range map[string]func(*RenderOptions) string{"text": text, "markdown": md} {
		check(name+" default", render(nil), []string{"Router"}, []string{"10.0.0.1", "Paris"})
		check(name+" shape data", render(&RenderOptions{StoredKinds: []string{StoredShapeData}}), []string{"Router", "10.0.0.1"}, []string{"Paris"})
		check(name+" linked data", render(&RenderOptions{StoredKinds: []string{StoredLinkedData}}), []string{"Paris"}, []string{"10.0.0.1"})
		check(name+" all", render(&RenderOptions{Stored: true}), []string{"10.0.0.1", "Paris"}, nil)
	}
}
