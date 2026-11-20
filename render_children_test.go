package gofastcore

import (
	"strings"
	"testing"
)

func childDoc(format, name, text string) *Document {
	d := New(format)
	d.Source.Name = name
	if text != "" {
		d.Parts = []Part{{Kind: PartPage, Number: 1, Blocks: []Block{{Kind: KindParagraph, Text: text}}}}
	}
	return d
}

func TestRenderChildren(t *testing.T) {
	d := childDoc("eml", "", "Message body")
	html := childDoc("html", "body.html", "Rich body")
	html.Children = []*Document{childDoc("png", "", "")}
	listed := &Document{Source: Source{Name: "tool.exe"}}
	d.Children = []*Document{html, nil, listed, childDoc("txt", "", "Plain\nattachment")}

	// Off by default: only the document's own parts.
	if got := d.Text(); got != "Message body\n" {
		t.Errorf("Text() = %q", got)
	}

	var sb strings.Builder
	d.WriteText(&sb, &RenderOptions{Children: true})
	want := "Message body\n\n[child 1: body.html]\n\nRich body\n\n[child 1.1: png]\n\n[child 3: tool.exe]\n\n[child 4: txt]\n\nPlain\nattachment\n"
	if sb.String() != want {
		t.Errorf("text:\n got %q\nwant %q", sb.String(), want)
	}

	sb.Reset()
	d.WriteMarkdown(&sb, &RenderOptions{Children: true})
	if !strings.Contains(sb.String(), "Message body\n\n<!-- child 1: body.html -->\n\nRich body\n\n<!-- child 1.1: png -->") {
		t.Errorf("markdown: %q", sb.String())
	}

	// The options reach the children.
	html.Parts[0].Blocks[0].Display = DisplayHidden
	sb.Reset()
	d.WriteText(&sb, &RenderOptions{Children: true, ExcludeDisplay: DisplayHidden})
	if strings.Contains(sb.String(), "Rich body") || !strings.Contains(sb.String(), "[child 1: body.html]") {
		t.Errorf("exclude in a child: %q", sb.String())
	}
}

func TestRenderChildrenMarkerText(t *testing.T) {
	d := childDoc("eml", "", "")
	d.Children = []*Document{childDoc("txt", "a --> b\nc", "x")}
	var sb strings.Builder
	d.WriteMarkdown(&sb, &RenderOptions{Children: true})
	if got := sb.String(); got != "<!-- child 1: a - -> b c -->\n\nx\n" {
		t.Errorf("markdown = %q", got)
	}
}

func TestRenderChildrenCycle(t *testing.T) {
	d := childDoc("eml", "", "loop")
	d.Children = []*Document{d}
	var sb strings.Builder
	if err := d.WriteText(&sb, &RenderOptions{Children: true}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(sb.String(), "loop"); n != maxRenderDepth+1 {
		t.Errorf("rendered %d times", n)
	}
}
