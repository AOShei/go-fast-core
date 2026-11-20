package gofastcore

import (
	"strings"
	"testing"
)

// A block marked stored in an ordinary part (a PDF page's script) is
// left out like the stored part, unless RenderOptions.Stored is set.
func TestRenderStoredBlockInPart(t *testing.T) {
	d := New("pdf")
	d.Parts = []Part{
		{Kind: PartPage, Number: 1, Blocks: []Block{
			{Kind: KindParagraph, Text: "Before the script."},
			{Kind: KindCode, Text: "app.alert(1)", Attrs: map[string]string{AttrStored: StoredMacro, AttrLanguage: "javascript"}},
			{Kind: KindParagraph, Text: "After the script."},
		}},
		{Kind: PartStored, Number: 2, Blocks: []Block{
			{Kind: KindParagraph, Text: "var=1", Attrs: map[string]string{AttrStored: StoredDocumentVariable}},
		}},
	}
	for _, md := range []bool{false, true} {
		render := func(opts *RenderOptions) string {
			var sb strings.Builder
			if md {
				d.WriteMarkdown(&sb, opts)
			} else {
				d.WriteText(&sb, opts)
			}
			return sb.String()
		}
		got := render(nil)
		if strings.Contains(got, "app.alert") || strings.Contains(got, "var=1") || !strings.Contains(got, "Before the script.\n\nAfter the script.") {
			t.Errorf("md=%v default: %q", md, got)
		}
		got = render(&RenderOptions{Stored: true})
		i, j, k := strings.Index(got, "Before"), strings.Index(got, "app.alert(1)"), strings.Index(got, "After")
		if i < 0 || j < i || k < j || !strings.Contains(got, "var=1") {
			t.Errorf("md=%v Stored: %q", md, got)
		}
	}
}
