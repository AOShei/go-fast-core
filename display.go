package gofastcore

import (
	"slices"
	"strings"
)

// What the file declares about display
//
// The go-fast libraries extract everything a file contains, including
// content the file says is not to be shown, and report what the file
// itself declares about it. They do not judge whether text is visible or
// harmful, and the absence of a reason says nothing about either: text in
// the colour of its background, text too small to read or text placed
// off the page carries no reason. Threat detection is outside these
// libraries.

// Display is a set of reasons, each declared by the file, why content is
// not shown or not inked. The zero value means the file declares neither.
// In JSON it is a "+"-joined list of names, e.g. "hidden+no_ink".
//
// A reason is declared when the file states it in one switch or one value
// of the content or of something containing it, including through the
// file's own inheritance (a style, an ancestor element). Anything that
// needs two values compared, or a threshold, is not a declaration and is
// not reported: colour against background, a size against a minimum, a
// box against the page or another box, the coverage of an image, an
// evaluated condition.
type Display uint16

const (
	// DisplayHidden: the file switches the content off. Hidden formatting
	// or the hidden attribute; a hidden sheet, row, column, slide, shape,
	// layer or annotation; display:none.
	DisplayHidden Display = 1 << iota
	// DisplayNoInk: the file declares the text drawn with no paint: no
	// fill, full transparency, PDF text render mode 3 or 7. The searchable
	// text layer of a scanned PDF page is drawn this way, so excluding
	// DisplayNoInk leaves out the text of scanned pages.
	DisplayNoInk
	displayEnd

	// DisplayUnknown stands for reasons this version of the package does
	// not know. Unlike an unknown style, an unknown reason is kept on
	// decode, so that an older consumer never loses a declaration.
	DisplayUnknown Display = 1 << 15

	// DisplayAny is every reason. It is meant for
	// RenderOptions.ExcludeDisplay.
	DisplayAny Display = 0xFFFF
)

var displayNames = [...]string{"hidden", "no_ink"}

// Valid reports whether d holds only reasons an extractor may emit.
func (d Display) Valid() bool { return d < displayEnd }

func (d Display) String() string {
	var parts []string
	for i, n := range displayNames {
		if d&(1<<i) != 0 {
			parts = append(parts, n)
		}
	}
	if d >= displayEnd {
		parts = append(parts, "unknown")
	}
	return strings.Join(parts, "+")
}

func (d Display) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Display) UnmarshalText(b []byte) error {
	*d = 0
	if len(b) == 0 {
		return nil
	}
next:
	for name := range strings.SplitSeq(string(b), "+") {
		for i, n := range displayNames {
			if n == name {
				*d |= 1 << i
				continue next
			}
		}
		*d |= DisplayUnknown
	}
	return nil
}

// DisplayCells marks a rectangular region of a Table whose cells the file
// declares not shown or not inked. Row and Col are 0-based; RowSpan and
// ColSpan are at least 1. A hidden column is one region the height of the
// table.
type DisplayCells struct {
	Row     int     `json:"row"`
	Col     int     `json:"col"`
	RowSpan int     `json:"row_span"`
	ColSpan int     `json:"col_span"`
	Display Display `json:"display"`
}

// DisplayWhole returns the reasons that apply to the whole block.
//
// Block.Display lists every reason declared anywhere in the block. A
// reason that also appears on a span (or, for a table, on a DisplayCells
// region) applies only to those ranges; a reason that appears on none
// applies to the whole block.
func (b *Block) DisplayWhole() Display {
	whole := b.Display
	for i := range b.Spans {
		whole &^= b.Spans[i].Display
	}
	if b.Table != nil {
		for i := range b.Table.Display {
			whole &^= b.Table.Display[i].Display
		}
	}
	return whole
}

// DisplayedText returns the block's text without the content declared
// with one of the reasons in exclude: the empty string if such a reason
// applies to the whole block, otherwise Text with those ranges cut out.
func (b *Block) DisplayedText(exclude Display) string {
	if b.Display&exclude == 0 {
		return b.Text
	}
	if b.DisplayWhole()&exclude != 0 {
		return ""
	}
	var cuts [][2]int
	for _, s := range b.Spans {
		if s.Display&exclude != 0 && s.Start >= 0 && s.Start < s.End && s.End <= len(b.Text) {
			cuts = append(cuts, [2]int{s.Start, s.End})
		}
	}
	if len(cuts) == 0 {
		return b.Text
	}
	slices.SortFunc(cuts, func(a, c [2]int) int { return a[0] - c[0] })

	var sb strings.Builder
	keep := func(piece string) {
		// Do not leave a double space where a passage was removed.
		if strings.HasPrefix(piece, " ") && strings.HasSuffix(sb.String(), " ") {
			piece = piece[1:]
		}
		sb.WriteString(piece)
	}
	at := 0
	for _, c := range cuts {
		if c[0] > at {
			keep(b.Text[at:c[0]])
		}
		at = max(at, c[1])
	}
	keep(b.Text[at:])
	if b.Kind == KindCode {
		return sb.String()
	}
	return strings.TrimSpace(sb.String())
}

// displayed returns the table with the cells declared with a reason in
// exclude blanked. It returns t itself when nothing changes.
func (t *Table) displayed(exclude Display) *Table {
	var out *Table
	for _, d := range t.Display {
		if d.Display&exclude == 0 {
			continue
		}
		if out == nil {
			c := *t
			c.Rows = make([][]string, len(t.Rows))
			for i, row := range t.Rows {
				c.Rows[i] = slices.Clone(row)
			}
			out = &c
		}
		for r := max(d.Row, 0); r < d.Row+d.RowSpan && r < len(out.Rows); r++ {
			for c := max(d.Col, 0); c < d.Col+d.ColSpan && c < len(out.Rows[r]); c++ {
				out.Rows[r][c] = ""
			}
		}
	}
	if out == nil {
		return t
	}
	return out
}

// Finish completes a part. An extractor calls it once per part, after the
// part's blocks are final. It:
//
//   - copies Part.Display onto every block, so that "b.Display != 0" is the
//     only test a consumer needs;
//   - makes Blocks an empty list instead of nil when the part has no blocks
//     (a failed or blank page), so that JSON has [] and not null.
//
// It touches only the part, so page workers can call it concurrently. It
// is idempotent.
func (p *Part) Finish() {
	if p.Blocks == nil {
		p.Blocks = []Block{}
	}
	if p.Display == 0 {
		return
	}
	for i := range p.Blocks {
		p.Blocks[i].Display |= p.Display
	}
}
