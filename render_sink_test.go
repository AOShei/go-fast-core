package gofastcore

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// opaque hides the type of the writer under it, so that the renderer
// writes to it through a bufio.Writer.
type opaque struct{ w *bytes.Buffer }

func (o opaque) Write(p []byte) (int, error) { return o.w.Write(p) }

// renderEverySink renders d through every kind of writer the renderer
// treats differently and fails if they differ, or if Text and Markdown
// differ from WriteText and WriteMarkdown with default options.
func renderEverySink(t *testing.T, d *Document, opts *RenderOptions) {
	t.Helper()
	for _, md := range []bool{false, true} {
		write := d.WriteText
		if md {
			write = d.WriteMarkdown
		}
		var through bytes.Buffer
		if err := write(opaque{&through}, opts); err != nil {
			t.Fatal(err)
		}
		var sb strings.Builder
		var bb bytes.Buffer
		write(&sb, opts)
		write(&bb, opts)
		if sb.String() != through.String() || bb.String() != through.String() {
			t.Fatalf("md=%v opts=%+v: through a buffer %q, to a builder %q, to a bytes.Buffer %q", md, opts, through.String(), sb.String(), bb.String())
		}
		if opts == nil {
			s := d.Text()
			if md {
				s = d.Markdown()
			}
			if s != through.String() {
				t.Fatalf("md=%v: Text or Markdown %q, written %q", md, s, through.String())
			}
		}
	}
}

func TestEverySinkRendersTheSame(t *testing.T) {
	docs := []*Document{sample(), {}}
	for _, seed := range []string{
		`{"parts":[{"kind":"page","blocks":[{"kind":"table","table":{"header_rows":1,"rows":[["a|b","c\nd"],["e\tf"]]}},{"kind":"table","continued":true,"table":{"rows":[["g","h"]]}}]}]}`,
		`{"parts":[{"kind":"body","blocks":[{"kind":"list_item","level":2,"text":"a\nb"},{"kind":"list_item","text":"c","continued":true},{"kind":"heading","level":9,"marker":"2.3","text":"h"},{"kind":"code","text":"x\n` + "```" + `"},{"kind":"header","text":"running"},{"kind":"paragraph","text":"s","attrs":{"stored":"field_code"}}]},{"kind":"stored","blocks":[{"kind":"paragraph","text":"macro"}]}]}`,
		`{"parts":[{"kind":"sheet","blocks":[{"kind":"paragraph","text":"see here","spans":[{"start":4,"end":8,"href":"https://x/(y)"}]},{"kind":"image","text":"alt [1]","image":{"id":"i 1"}}]}],"children":[{"source":{"name":"c.pdf"},"parts":[{"kind":"page","blocks":[{"kind":"paragraph","text":"child"}]}]}]}`,
	} {
		var d Document
		if err := json.Unmarshal([]byte(seed), &d); err != nil {
			t.Fatal(err)
		}
		docs = append(docs, &d)
	}
	every := &RenderOptions{Children: true, Stored: true, Furniture: true, PartMarkers: true}
	for _, d := range docs {
		renderEverySink(t, d, nil)
		renderEverySink(t, d, every)
		for i := range d.Parts {
			p := &d.Parts[i]
			one := &Document{Parts: []Part{*p}}
			var through bytes.Buffer
			one.WriteText(opaque{&through}, nil)
			if p.Text() != through.String() {
				t.Fatalf("Part.Text %q, written %q", p.Text(), through.String())
			}
			through.Reset()
			one.WriteMarkdown(opaque{&through}, nil)
			if p.Markdown() != through.String() {
				t.Fatalf("Part.Markdown %q, written %q", p.Markdown(), through.String())
			}
		}
	}
}

// failing fails every write.
type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteErrorIsReturned(t *testing.T) {
	if err := sample().WriteText(failing{}, nil); err == nil {
		t.Fatal("WriteText to a failing writer returned no error")
	}
	if err := sample().WriteMarkdown(failing{}, nil); err == nil {
		t.Fatal("WriteMarkdown to a failing writer returned no error")
	}
}

// TestTextAllocatesOnce: Text and Markdown allocate their result once, the
// hint being large enough for sample().
func TestTextAllocatesOnce(t *testing.T) {
	d := sample()
	for _, md := range []bool{false, true} {
		render := d.Text
		if md {
			render = d.Markdown
		}
		s := render()
		if h := d.sizeHint(md); h < len(s) {
			t.Errorf("md=%v: hint %d, rendered %d bytes", md, h, len(s))
		}
	}
}

func BenchmarkText(b *testing.B) {
	d := &Document{}
	for p := range 20 {
		part := Part{Kind: PartPage, Number: p + 1}
		for i := range 40 {
			part.Blocks = append(part.Blocks, Block{Kind: KindParagraph, Text: strings.Repeat("word ", 10+i%30)})
		}
		d.Parts = append(d.Parts, part)
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = d.Text()
	}
}
