package gofastcore

import (
	"bufio"
	"bytes"
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
	// Stored includes stored data: the stored part (macro code, defined
	// names, document variables and other data the file shows nowhere),
	// and blocks of other parts whose Attrs[AttrStored] is set, such as
	// the scripts of a PDF page. Both are left out by default.
	Stored bool
	// StoredKinds includes only the stored data of the kinds named (values
	// of Attrs[AttrStored], such as StoredMarkupComment or
	// StoredFieldCode), wherever it is: in the stored part or in any other
	// part. A block of the stored part with no kind is included only by
	// Stored. Stored includes every kind, whatever StoredKinds says.
	StoredKinds []string
	// PartMarkers emits a line before each part so that positions survive
	// in the rendered text: "<!-- page 3 -->" in Markdown, "[page 3]" in
	// plain text.
	PartMarkers bool
	// ExcludeDisplay leaves out content the file declares with any of the
	// given reasons. The default is to include everything. Excluding
	// DisplayNoInk also leaves out the searchable text layer of scanned
	// PDF pages, which would then render empty.
	ExcludeDisplay Display
	// Children renders the documents in Document.Children after the
	// document's own parts, depth first, each after a line that names it:
	// "<!-- child 2: report.pdf -->" in Markdown, "[child 2: report.pdf]"
	// in plain text. A nested child is numbered by its path ("child 2.1").
	// A child that was listed but not extracted gives its line only. The
	// same options apply to every child. Children nested more than 64
	// deep are not rendered: no extractor nests them so deep, and the
	// bound stops a tree built by hand with a cycle in it.
	Children bool
}

// Markdown renders the document (not its children) as Markdown with
// default options.
func (d *Document) Markdown() string {
	var sb strings.Builder
	sb.Grow(d.sizeHint(true))
	d.WriteMarkdown(&sb, nil)
	return sb.String()
}

// Text renders the document (not its children) as plain text with default
// options, for consumers such as embedding pipelines that do not want
// Markdown syntax. Blocks are separated by blank lines, tables are
// tab-separated, and images contribute their alt text.
func (d *Document) Text() string {
	var sb strings.Builder
	sb.Grow(d.sizeHint(false))
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

// Markdown renders a single part as Markdown with default options. Those
// leave out stored data and running headers and footers, so a stored part
// renders as "". To render one with other options, render a Document
// holding it.
func (p *Part) Markdown() string { return p.render(true) }

// Text renders a single part as plain text with default options, as
// Markdown does.
func (p *Part) Text() string { return p.render(false) }

func (d *Document) render(w io.Writer, opts *RenderOptions, md bool) error {
	r := newRenderer(w, md)
	if opts != nil {
		r.opts = *opts
	}
	r.doc(d, "", 0)
	return r.finish()
}

// maxRenderDepth bounds the recursion into Children, for a tree built by
// hand with a cycle in it; extractors' own depth limits are far lower.
const maxRenderDepth = 64

func (r *renderer) doc(d *Document, path string, depth int) {
	for i := range d.Parts {
		r.part(&d.Parts[i])
	}
	if !r.opts.Children || depth >= maxRenderDepth {
		return
	}
	for i, c := range d.Children {
		if c == nil {
			continue
		}
		n := path + strconv.Itoa(i+1)
		label := "child " + n
		switch {
		case c.Source.Name != "":
			label += ": " + oneLine(c.Source.Name)
		case c.Source.Format != "":
			label += ": " + c.Source.Format
		}
		r.marker(label)
		r.doc(c, n+".", depth+1)
	}
}

// marker writes a line that says where the following text comes from.
func (r *renderer) marker(label string) {
	if r.any {
		r.w.WriteString("\n\n")
	}
	if r.md {
		r.w.WriteString("<!-- " + strings.ReplaceAll(label, "--", "- -") + " -->")
	} else {
		r.w.WriteString("[" + label + "]")
	}
	r.any, r.prev = true, KindInvalid
}

func (p *Part) render(md bool) string {
	var sb strings.Builder
	sb.Grow(p.sizeHint(md))
	r := newRenderer(&sb, md)
	r.part(p)
	r.finish()
	return sb.String()
}

// sink is what the renderer writes to.
type sink interface {
	io.Writer
	io.StringWriter
	io.ByteWriter
}

// newRenderer returns a renderer that writes to w. A strings.Builder or a
// bytes.Buffer is written to directly, since a buffer in front of it would
// only copy the text once more; any other writer through a bufio.Writer.
func newRenderer(w io.Writer, md bool) renderer {
	switch s := w.(type) {
	case *strings.Builder:
		return renderer{w: s, md: md}
	case *bytes.Buffer:
		return renderer{w: s, md: md}
	}
	bw := bufio.NewWriter(w)
	return renderer{w: bw, buf: bw, md: md}
}

// renderer writes blocks without a trailing newline; the separator that
// belongs between two blocks is chosen when the second one is written.
type renderer struct {
	w    sink
	buf  *bufio.Writer // the buffer in front of the writer, if any
	opts RenderOptions
	md   bool
	any  bool // something has been written
	prev Kind // kind of the last block written; KindInvalid after a marker
	cols int  // columns of the last table written
	// linked is the text of the block being written with its links as
	// Markdown links, or "" if it has none.
	linked string
	// inStored is set while the blocks of the stored part are written.
	inStored bool
}

// storedWanted reports whether the options include stored data of the
// given kind ("" for a block of the stored part that names none).
func (r *renderer) storedWanted(kind string) bool {
	return r.opts.Stored || kind != "" && slices.Contains(r.opts.StoredKinds, kind)
}

func (r *renderer) finish() error {
	if r.any {
		r.w.WriteByte('\n')
	}
	if r.buf == nil {
		return nil
	}
	return r.buf.Flush()
}

// sizeHint is about the length of the document's own parts rendered with
// default options, so that Text and Markdown can allocate their result
// once. Furniture and stored data, which the defaults leave out, are not
// counted; Markdown's syntax is allowed for roughly. A hint too small
// only costs the builder a reallocation.
func (d *Document) sizeHint(md bool) int {
	n := 0
	for i := range d.Parts {
		n += d.Parts[i].sizeHint(md)
	}
	return n
}

func (p *Part) sizeHint(md bool) int {
	if p.Kind == PartStored {
		return 0
	}
	n := 0
	for i := range p.Blocks {
		b := &p.Blocks[i]
		if b.Kind.Furniture() || b.Attrs[AttrStored] != "" {
			continue
		}
		n += len(b.Text) + len(b.Marker) + 2 // 2: the separator before it
		if md {
			n += 8 // a marker, a prefix or a fence, about
		}
		if b.Table != nil {
			for _, row := range b.Table.Rows {
				n++
				for _, c := range row {
					n += len(c) + 1
					if md {
						n += 2
					}
				}
			}
		}
	}
	return n
}

func (r *renderer) part(p *Part) {
	if p.Display&r.opts.ExcludeDisplay != 0 {
		return
	}
	if p.Kind == PartStored && !r.opts.Stored {
		// Only some kinds are wanted: the part is written, marker and
		// all, only if one of its blocks is.
		if !slices.ContainsFunc(p.Blocks, func(b Block) bool { return r.storedWanted(b.Attrs[AttrStored]) }) {
			return
		}
		r.inStored = true
		defer func() { r.inStored = false }()
	}
	if r.opts.PartMarkers {
		label := p.Kind.String() + " " + strconv.Itoa(p.Number)
		if p.Name != "" {
			label += ": " + oneLine(p.Name)
		}
		r.marker(label)
	}
	for i := range p.Blocks {
		r.block(&p.Blocks[i])
	}
}

func (r *renderer) skip(b *Block) bool {
	switch {
	case (b.Attrs[AttrStored] != "" || r.inStored) && !r.storedWanted(b.Attrs[AttrStored]):
		return true
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
		// Links are written into the text before excluded passages are
		// taken out of it, since their places are in the text as it is.
		r.linked, _ = linkedText(b, r.opts.ExcludeDisplay)
	}
	if exclude := r.opts.ExcludeDisplay; b.Display&exclude != 0 {
		if b.DisplayWhole()&exclude != 0 {
			return
		}
		shown := *b
		if b.Table != nil {
			shown.Table = b.Table.displayed(exclude)
		} else if shown.Text = b.DisplayedText(exclude); shown.Text == "" && b.Kind != KindImage && b.Kind != KindThematicBreak {
			return
		}
		b = &shown
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
// links and the passages declared with a reason in exclude left out, as
// DisplayedText leaves them out. It returns "" and false if b has no link.
func linkedText(b *Block, exclude Display) (string, bool) {
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
	if b.Display&exclude != 0 {
		if b.DisplayWhole()&exclude != 0 {
			return "", false
		}
		for _, s := range b.Spans {
			if s.Display&exclude != 0 && s.Start >= 0 && s.Start < s.End && s.End <= len(b.Text) {
				cuts = append(cuts, [2]int{s.Start, s.End})
			}
		}
	}
	excluded := func(at int) bool {
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
		// Do not leave a double space where a passage was removed.
		if strings.HasPrefix(piece, " ") && strings.HasSuffix(sb.String(), " ") {
			piece = piece[1:]
		}
		sb.WriteString(piece)
	}
	// The text is walked from one boundary of a link or an excluded passage
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
		if excluded(from) {
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
