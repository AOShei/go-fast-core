package gofastcore

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// RenderOptions controls Markdown and plain-text output. The zero value
// is the default.
type RenderOptions struct {
	// Furniture includes running headers and footers, which are dropped
	// by default.
	Furniture bool
	// PartMarkers emits a line before each part so that positions survive
	// in the rendered text: "<!-- page 3 -->" in Markdown, "[page 3]" in
	// plain text.
	PartMarkers bool
	// ExcludeHidden leaves out text hidden for any of the given reasons.
	// The default is to include everything. Use HiddenAny to exclude all
	// hidden text, or HiddenAny &^ HiddenTextLayer to keep the text layer
	// of scanned pages, which would otherwise render empty.
	ExcludeHidden Hidden
}

// Markdown renders the document (not its children) as Markdown with
// default options.
func (d *Document) Markdown() string {
	var sb strings.Builder
	d.WriteMarkdown(&sb, nil)
	return sb.String()
}

// Text renders the document (not its children) as plain text with default
// options, for consumers such as embedding pipelines that do not want
// Markdown syntax. Blocks are separated by blank lines, tables are
// tab-separated, and images contribute their alt text.
func (d *Document) Text() string {
	var sb strings.Builder
	d.WriteText(&sb, nil)
	return sb.String()
}

// WriteMarkdown renders the document's parts to w as Markdown.
//
// Text that would be read as Markdown structure at the start of a line
// ("# ", "> ", "- ", "1. ") is escaped, so a paragraph stays a paragraph.
// Inline characters are not escaped, and Spans are not rendered.
func (d *Document) WriteMarkdown(w io.Writer, opts *RenderOptions) error {
	return d.render(w, opts, true)
}

// WriteText renders the document's parts to w as plain text.
func (d *Document) WriteText(w io.Writer, opts *RenderOptions) error {
	return d.render(w, opts, false)
}

// Markdown renders a single part as Markdown with default options.
func (p *Part) Markdown() string { return p.render(true) }

// Text renders a single part as plain text with default options.
func (p *Part) Text() string { return p.render(false) }

func (d *Document) render(w io.Writer, opts *RenderOptions, md bool) error {
	r := renderer{w: bufio.NewWriter(w), md: md}
	if opts != nil {
		r.opts = *opts
	}
	for i := range d.Parts {
		r.part(&d.Parts[i])
	}
	return r.finish()
}

func (p *Part) render(md bool) string {
	var sb strings.Builder
	r := renderer{w: bufio.NewWriter(&sb), md: md}
	r.part(p)
	r.finish()
	return sb.String()
}

// renderer writes blocks without a trailing newline; the separator that
// belongs between two blocks is chosen when the second one is written.
type renderer struct {
	w    *bufio.Writer
	opts RenderOptions
	md   bool
	any  bool // something has been written
	prev Kind // kind of the last block written; KindInvalid after a marker
}

func (r *renderer) finish() error {
	if r.any {
		r.w.WriteByte('\n')
	}
	return r.w.Flush()
}

func (r *renderer) part(p *Part) {
	if p.Hidden&r.opts.ExcludeHidden != 0 {
		return
	}
	if r.opts.PartMarkers {
		if r.any {
			r.w.WriteString("\n\n")
		}
		label := p.Kind.String() + " " + strconv.Itoa(p.Number)
		if p.Name != "" {
			label += ": " + oneLine(p.Name)
		}
		if r.md {
			r.w.WriteString("<!-- " + strings.ReplaceAll(label, "--", "- -") + " -->")
		} else {
			r.w.WriteString("[" + label + "]")
		}
		r.any, r.prev = true, KindInvalid
	}
	for i := range p.Blocks {
		r.block(&p.Blocks[i])
	}
}

func (r *renderer) skip(b *Block) bool {
	switch {
	case b.Kind.Furniture() && !r.opts.Furniture:
		return true
	case b.Kind == KindTable:
		return b.Table == nil || len(b.Table.Rows) == 0 || len(b.Table.Rows[0]) == 0
	case b.Kind == KindImage && !r.md:
		return b.Text == ""
	}
	return false
}

// joinable reports whether a continued block of kind k can be run on to
// the block before it.
func joinable(k Kind) bool {
	switch k {
	case KindParagraph, KindListItem, KindQuote, KindCaption, KindFootnote, KindNote:
		return true
	}
	return false
}

func (r *renderer) block(b *Block) {
	if exclude := r.opts.ExcludeHidden; b.Hidden&exclude != 0 {
		if b.HiddenWhole()&exclude != 0 {
			return
		}
		visible := *b
		if b.Table != nil {
			visible.Table = b.Table.visible(exclude)
		} else if visible.Text = b.VisibleText(exclude); visible.Text == "" && b.Kind != KindImage {
			return
		}
		b = &visible
	}
	if r.skip(b) {
		return
	}
	joined := b.Continued && b.Kind == r.prev && joinable(b.Kind)
	switch {
	case !r.any:
	case joined && !r.md:
		r.w.WriteByte(' ')
	case joined, r.prev == KindListItem && b.Kind == KindListItem:
		// A single newline keeps a Markdown paragraph, quote or list going.
		r.w.WriteByte('\n')
	default:
		r.w.WriteString("\n\n")
	}
	if r.md {
		r.markdown(b, joined)
	} else {
		r.plain(b, joined)
	}
	r.any, r.prev = true, b.Kind
}

func (r *renderer) markdown(b *Block, joined bool) {
	w := r.w
	switch b.Kind {
	case KindHeading:
		w.WriteString(strings.Repeat("#", min(max(int(b.Level), 1), 6)))
		w.WriteByte(' ')
		if b.Marker != "" {
			// A numbered heading: "## 2.3 Scope".
			w.WriteString(oneLine(b.Marker))
			w.WriteByte(' ')
		}
		w.WriteString(oneLine(b.Text))

	case KindListItem:
		indent := strings.Repeat("    ", max(int(b.Level), 1)-1)
		marker := "-"
		if b.Ordered {
			marker = "1."
			if b.Marker != "" {
				marker = b.Marker
			}
		}
		rest := indent + strings.Repeat(" ", len(marker)+1)
		first := indent + marker + " "
		if joined {
			first = rest
		}
		r.lines(b.Text, first, rest)

	case KindQuote:
		prefix := strings.Repeat("> ", max(int(b.Level), 1))
		r.lines(b.Text, prefix, prefix)

	case KindCode:
		fence := "```"
		for strings.Contains(b.Text, fence) {
			fence += "`"
		}
		w.WriteString(fence)
		w.WriteString(b.Attrs["language"])
		w.WriteByte('\n')
		w.WriteString(b.Text)
		if !strings.HasSuffix(b.Text, "\n") {
			w.WriteByte('\n')
		}
		w.WriteString(fence)

	case KindTable:
		r.markdownTable(b.Table)

	case KindImage:
		w.WriteString("![")
		w.WriteString(strings.NewReplacer("[", "\\[", "]", "\\]").Replace(oneLine(b.Text)))
		w.WriteString("](")
		if b.Image != nil {
			w.WriteString(strings.NewReplacer(" ", "%20", ")", "%29").Replace(b.Image.ID))
		}
		w.WriteByte(')')

	case KindFootnote:
		first := ""
		if b.Marker != "" && !joined {
			first = "[^" + b.Marker + "]: "
		}
		r.lines(b.Text, first, "")

	case KindFormula:
		w.WriteString("$$\n")
		w.WriteString(b.Text)
		w.WriteString("\n$$")

	default: // paragraph, caption, note, header, footer, unknown
		r.lines(b.Text, "", "")
	}
}

// lines writes text with first before its first line and rest before
// every following line, escaping each line's leading Markdown marker.
func (r *renderer) lines(text, first, rest string) {
	prefix := first
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			r.w.WriteByte('\n')
		}
		r.w.WriteString(prefix)
		r.w.WriteString(escapeLine(line))
		prefix = rest
	}
}

// escapeLine backslash-escapes whatever at the start of line would make
// Markdown read it as structure: a heading, quote, list item, rule, setext
// underline, code fence or indented code.
func escapeLine(line string) string {
	s := strings.TrimLeft(line, " \t")
	if s == "" {
		return s
	}
	follows := func(i int) bool { return i == len(s) || s[i] == ' ' || s[i] == '\t' }
	switch c := s[0]; {
	case c == '#':
		n := 0
		for n < len(s) && s[n] == '#' {
			n++
		}
		if n <= 6 && follows(n) {
			return `\` + s
		}
	case c == '>':
		return `\` + s
	case c == '-' || c == '+' || c == '*' || c == '_' || c == '=':
		// "- item", or a line made only of the marker: "---", "* * *", "===".
		if (c != '_' && c != '=' && follows(1)) || strings.Trim(s, string(c)+" \t") == "" {
			return `\` + s
		}
	case c == '`' || c == '~':
		if strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~") {
			return `\` + s
		}
	case c >= '0' && c <= '9':
		n := 0
		for n < len(s) && s[n] >= '0' && s[n] <= '9' {
			n++
		}
		if n <= 9 && n < len(s) && (s[n] == '.' || s[n] == ')') && follows(n+1) {
			return s[:n] + `\` + s[n:]
		}
	}
	return s
}

func (r *renderer) markdownTable(t *Table) {
	w := r.w
	cols := len(t.Rows[0])
	writeRow := func(row []string) {
		w.WriteByte('|')
		for c := range cols {
			w.WriteByte(' ')
			if c < len(row) {
				w.WriteString(markdownCell(row[c]))
			}
			w.WriteString(" |")
		}
	}
	// Markdown tables need exactly one header row. With no declared
	// header an empty one is emitted so that data is not mislabeled; with
	// several, the extra header rows follow as ordinary rows.
	body := t.Rows
	if t.HeaderRows > 0 {
		writeRow(t.Rows[0])
		body = t.Rows[1:]
	} else {
		writeRow(nil)
	}
	w.WriteString("\n|")
	for range cols {
		w.WriteString(" --- |")
	}
	for _, row := range body {
		w.WriteByte('\n')
		writeRow(row)
	}
}

var markdownCellEscaper = strings.NewReplacer("|", "\\|", "\r\n", "<br>", "\n", "<br>", "\r", "<br>")

func markdownCell(s string) string {
	if !strings.ContainsAny(s, "|\r\n") {
		return s
	}
	return markdownCellEscaper.Replace(s)
}

func (r *renderer) plain(b *Block, joined bool) {
	w := r.w
	switch b.Kind {
	case KindHeading:
		if b.Marker != "" {
			w.WriteString(oneLine(b.Marker))
			w.WriteByte(' ')
		}
		w.WriteString(oneLine(b.Text))

	case KindListItem:
		if !joined {
			w.WriteString(strings.Repeat("  ", max(int(b.Level), 1)-1))
			if b.Marker != "" {
				w.WriteString(b.Marker)
			} else {
				w.WriteByte('-')
			}
			w.WriteByte(' ')
		}
		w.WriteString(b.Text)

	case KindFootnote:
		if b.Marker != "" && !joined {
			w.WriteString(b.Marker)
			w.WriteByte(' ')
		}
		w.WriteString(b.Text)

	case KindCode:
		w.WriteString(strings.TrimRight(b.Text, "\n"))

	case KindTable:
		for i, row := range b.Table.Rows {
			if i > 0 {
				w.WriteByte('\n')
			}
			for c, cell := range row {
				if c > 0 {
					w.WriteByte('\t')
				}
				w.WriteString(plainCell(cell))
			}
		}

	default:
		w.WriteString(b.Text)
	}
}

var plainCellEscaper = strings.NewReplacer("\t", " ", "\r\n", " ", "\n", " ", "\r", " ")

func plainCell(s string) string {
	if !strings.ContainsAny(s, "\t\r\n") {
		return s
	}
	return plainCellEscaper.Replace(s)
}

func oneLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}
