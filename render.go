package gofastcore

import (
	"bufio"
	"io"
	"slices"
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
// Inline characters are not escaped. Of Spans, links are rendered, as
// [text](<address>), and so are the links of table cells; formatting is
// not.
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
	cols int  // columns of the last table written
	// linked is the text of the block being written with its links as
	// Markdown links, or "" if it has none.
	linked string
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
	case b.Kind == KindThematicBreak && !r.md:
		// Plain text already separates blocks with a blank line.
		return true
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
	r.linked = ""
	if r.md && b.Kind != KindCode && b.Kind != KindFormula && b.Kind != KindImage && b.Kind != KindThematicBreak {
		// Links are written into the text before hidden passages are taken
		// out of it, since their places are in the text as it is.
		r.linked, _ = linkedText(b, r.opts.ExcludeHidden)
	}
	if exclude := r.opts.ExcludeHidden; b.Hidden&exclude != 0 {
		if b.HiddenWhole()&exclude != 0 {
			return
		}
		visible := *b
		if b.Table != nil {
			visible.Table = b.Table.visible(exclude)
		} else if visible.Text = b.VisibleText(exclude); visible.Text == "" && b.Kind != KindImage && b.Kind != KindThematicBreak {
			return
		}
		b = &visible
	}
	if r.skip(b) {
		return
	}
	// A continued table is run on to the one before it if it has the
	// same columns: its rows follow as rows of that table.
	table := b.Kind == KindTable
	joined := b.Continued && b.Kind == r.prev && (joinable(b.Kind) || table && len(b.Table.Rows[0]) == r.cols)
	switch {
	case !r.any:
	case joined && !r.md && !table:
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
	if table {
		r.cols = len(b.Table.Rows[0])
	}
}

func (r *renderer) markdown(b *Block, joined bool) {
	w := r.w
	if r.linked != "" {
		// Its text with its links written as links.
		c := *b
		c.Text = r.linked
		b = &c
	}
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
		r.markdownTable(b.Table, joined)

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

	case KindThematicBreak:
		w.WriteString("---")

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

func (r *renderer) markdownTable(t *Table, joined bool) {
	w := r.w
	cols := len(t.Rows[0])
	var links map[[2]int]string
	for _, c := range t.Cells {
		if c.Href != "" {
			if links == nil {
				links = map[[2]int]string{}
			}
			links[[2]int{c.Row, c.Col}] = c.Href
		}
	}
	// row is the place of the row in t.Rows; -1 for an empty header.
	writeRow := func(row int) {
		w.WriteByte('|')
		for c := range cols {
			w.WriteByte(' ')
			if row >= 0 && c < len(t.Rows[row]) {
				text := t.Rows[row][c]
				if href, ok := links[[2]int{row, c}]; ok && text != "" {
					w.WriteByte('[')
					w.WriteString(markdownCell(linkTextEscaper.Replace(text)))
					w.WriteString("](")
					w.WriteString(linkDestination(href, true))
					w.WriteByte(')')
				} else {
					w.WriteString(markdownCell(text))
				}
			}
			w.WriteString(" |")
		}
	}
	// The rows of a continued table carry on the table before it.
	if joined {
		for i := range t.Rows {
			if i > 0 {
				w.WriteByte('\n')
			}
			writeRow(i)
		}
		return
	}
	// Markdown tables need exactly one header row. With no declared
	// header an empty one is emitted so that data is not mislabeled; with
	// several, the extra header rows follow as ordinary rows.
	first := 0
	if t.HeaderRows > 0 {
		writeRow(0)
		first = 1
	} else {
		writeRow(-1)
	}
	w.WriteString("\n|")
	for range cols {
		w.WriteString(" --- |")
	}
	for i := first; i < len(t.Rows); i++ {
		w.WriteByte('\n')
		writeRow(i)
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

// linkTextEscaper escapes what would end the text of a Markdown link early.
var linkTextEscaper = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`)

// linkDestination writes an address as the destination of a Markdown
// link, in angle brackets so that spaces and parentheses in it survive.
// In a table, a bar would end the cell, and is written as %7C.
func linkDestination(href string, inTable bool) string {
	var sb strings.Builder
	sb.WriteByte('<')
	for _, c := range href {
		switch {
		case c == '<':
			sb.WriteString("%3C")
		case c == '>':
			sb.WriteString("%3E")
		case c == '\\':
			sb.WriteString(`\\`)
		case c == '|' && inTable:
			sb.WriteString("%7C")
		case c == '\n' || c == '\r':
			sb.WriteString("%0A")
		default:
			sb.WriteRune(c)
		}
	}
	sb.WriteByte('>')
	return sb.String()
}

// linkedText returns the text of b with its links written as Markdown
// links and the passages hidden for a reason in exclude left out, as
// VisibleText leaves them out. It returns "" and false if b has no link.
func linkedText(b *Block, exclude Hidden) (string, bool) {
	type link struct {
		from, to int
		href     string
	}
	var links []link
	for _, s := range b.Spans {
		if s.Href == "" || s.Start < 0 || s.Start >= s.End || s.End > len(b.Text) {
			continue
		}
		if n := len(links); n > 0 && links[n-1].href == s.Href && s.Start <= links[n-1].to {
			links[n-1].to = max(links[n-1].to, s.End) // one link, in several spans
			continue
		}
		links = append(links, link{s.Start, s.End, s.Href})
	}
	if len(links) == 0 {
		return "", false
	}
	slices.SortStableFunc(links, func(a, c link) int { return a.from - c.from })
	var cuts [][2]int
	if b.Hidden&exclude != 0 {
		if b.HiddenWhole()&exclude != 0 {
			return "", false
		}
		for _, s := range b.Spans {
			if s.Hidden&exclude != 0 && s.Start >= 0 && s.Start < s.End && s.End <= len(b.Text) {
				cuts = append(cuts, [2]int{s.Start, s.End})
			}
		}
	}
	hidden := func(at int) bool {
		for _, c := range cuts {
			if at >= c[0] && at < c[1] {
				return true
			}
		}
		return false
	}
	var sb strings.Builder
	open, li := false, 0
	keep := func(piece string, inLink bool) {
		if inLink {
			piece = linkTextEscaper.Replace(strings.ReplaceAll(piece, "\n", " "))
		}
		// Do not leave a double space where a hidden passage was removed.
		if strings.HasPrefix(piece, " ") && strings.HasSuffix(sb.String(), " ") {
			piece = piece[1:]
		}
		sb.WriteString(piece)
	}
	// The text is walked from one boundary of a link or a hidden passage
	// to the next.
	bounds := []int{0, len(b.Text)}
	for _, l := range links {
		bounds = append(bounds, l.from, l.to)
	}
	for _, c := range cuts {
		bounds = append(bounds, c[0], c[1])
	}
	slices.Sort(bounds)
	bounds = slices.Compact(bounds)
	for i := 0; i+1 < len(bounds); i++ {
		from, to := bounds[i], bounds[i+1]
		for li < len(links) && links[li].to <= from {
			if open {
				sb.WriteString("](" + linkDestination(links[li].href, false) + ")")
				open = false
			}
			li++
		}
		inLink := li < len(links) && links[li].from <= from && from < links[li].to
		if hidden(from) {
			continue
		}
		if inLink && !open {
			sb.WriteByte('[')
			open = true
		}
		keep(b.Text[from:to], inLink)
	}
	if open {
		sb.WriteString("](" + linkDestination(links[li].href, false) + ")")
	}
	return strings.TrimSpace(sb.String()), true
}
