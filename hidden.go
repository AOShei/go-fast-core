package gofastcore

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

// Hidden text
//
// Text that a reader of the source cannot see still reaches whatever
// consumes the extracted text. That is a known way to poison the context
// of an AI system, and it also has ordinary uses (the text layer of a
// scanned page, screen-reader text, a collapsed menu). The go-fast
// libraries therefore never drop hidden text and never decide what it
// means: they extract it and flag it with the reason it is not visible.
//
// A flag is evidence that text is hidden. The absence of a flag is not
// evidence that a document is safe: an extractor cannot see text inside
// images, a font whose glyphs display one thing and map to another, or
// visibility controlled by external stylesheets and scripts.

// Hidden is a set of reasons why text is not visible to a reader of the
// source. The zero value means no reason was found. In JSON it is a
// "+"-joined list of names, e.g. "invisible+tiny".
type Hidden uint16

const (
	// HiddenInvisible: the source marks the text as not displayed. PDF
	// invisible render mode or a layer that is off by default; Word
	// hidden-text formatting; a hidden slide, sheet, row or column; a cell
	// format that blanks the value; HTML display:none, visibility:hidden
	// or the hidden attribute.
	HiddenInvisible Hidden = 1 << iota
	// HiddenTextLayer: the text is invisible and lies over an image, which
	// is how the searchable text of a scanned page is stored. It is used
	// instead of HiddenInvisible, not with it. Nothing verifies that the
	// text matches what the image shows.
	HiddenTextLayer
	// HiddenTiny: the text is displayed smaller than HiddenTinySize.
	HiddenTiny
	// HiddenLowContrast: the text color is indistinguishable from what is
	// behind it, as defined by LowContrast.
	HiddenLowContrast
	// HiddenOffPage: the text is positioned outside the visible area of
	// its page, slide or viewport.
	HiddenOffPage
	// HiddenClipped: the text lies outside a clipping region, including a
	// box too small to show it with its overflow cut off.
	HiddenClipped
	// HiddenOccluded: the text is covered by an image or a filled shape.
	HiddenOccluded
	// HiddenTransparent: the text is drawn with zero opacity.
	HiddenTransparent
	// HiddenChars: the text contains characters that display as nothing
	// yet can carry a payload, as found by InvisibleChars.
	HiddenChars
	hiddenEnd

	// HiddenUnknown stands for reasons this version of the package does
	// not know. Unlike an unknown style, an unknown reason is kept on
	// decode, so that hidden text never looks visible to an older consumer.
	HiddenUnknown Hidden = 1 << 15

	// HiddenAny is every reason. It is meant for
	// RenderOptions.ExcludeHidden.
	HiddenAny Hidden = 0xFFFF
)

var hiddenNames = [...]string{
	"invisible", "text_layer", "tiny", "low_contrast", "off_page",
	"clipped", "occluded", "transparent", "invisible_chars",
}

// Valid reports whether h holds only reasons an extractor may emit.
func (h Hidden) Valid() bool { return h < hiddenEnd }

func (h Hidden) String() string {
	var parts []string
	for i, n := range hiddenNames {
		if h&(1<<i) != 0 {
			parts = append(parts, n)
		}
	}
	if h >= hiddenEnd {
		parts = append(parts, "unknown")
	}
	return strings.Join(parts, "+")
}

func (h Hidden) MarshalText() ([]byte, error) { return []byte(h.String()), nil }

func (h *Hidden) UnmarshalText(b []byte) error {
	*h = 0
	if len(b) == 0 {
		return nil
	}
next:
	for name := range strings.SplitSeq(string(b), "+") {
		for i, n := range hiddenNames {
			if n == name {
				*h |= 1 << i
				continue next
			}
		}
		*h |= HiddenUnknown
	}
	return nil
}

// HiddenCells marks a rectangular region of a Table whose cells are
// hidden. Row and Col are 0-based; RowSpan and ColSpan are at least 1. A
// hidden column is one region the height of the table.
type HiddenCells struct {
	Row     int    `json:"row"`
	Col     int    `json:"col"`
	RowSpan int    `json:"row_span"`
	ColSpan int    `json:"col_span"`
	Hidden  Hidden `json:"hidden"`
}

// Shared thresholds, so that a reason means the same in every format.
const (
	// HiddenTinySize is the displayed text size, in points, below which
	// text is HiddenTiny. Printed text stops being legible well above it.
	HiddenTinySize = 2.0

	// HiddenContrastRatio is the WCAG contrast ratio below which text is
	// HiddenLowContrast. Identical colors have ratio 1; #EEEEEE on white
	// is 1.16 and is flagged, #DDDDDD on white is 1.36 and is not.
	HiddenContrastRatio = 1.2
)

// LowContrast reports whether text of one 8-bit sRGB color is
// indistinguishable from a background of another, i.e. whether their
// WCAG contrast ratio is below HiddenContrastRatio.
func LowContrast(textR, textG, textB, backR, backG, backB uint8) bool {
	return ContrastRatio(textR, textG, textB, backR, backG, backB) < HiddenContrastRatio
}

// ContrastRatio returns the WCAG 2 contrast ratio of two 8-bit sRGB
// colors, from 1 (identical) to 21 (black on white).
func ContrastRatio(r1, g1, b1, r2, g2, b2 uint8) float64 {
	l1, l2 := luminance(r1, g1, b1), luminance(r2, g2, b2)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

func luminance(r, g, b uint8) float64 {
	lin := func(c uint8) float64 {
		v := float64(c) / 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// InvisibleChars finds the characters in s that display as nothing yet can
// carry a payload, and returns their byte ranges as spans with Hidden set
// to HiddenChars. It returns nil for the usual case of none. It flags:
//
//   - Unicode tag characters (U+E0000 to U+E007F), which mirror ASCII
//     invisibly and which language models can read, except inside a
//     well-formed emoji flag sequence.
//   - The bidirectional overrides U+202D and U+202E, which make text
//     display in a different order from the one it is stored and read in.
//   - Runs of four or more zero-width characters (U+200B, U+200C, U+200D,
//     U+2060, U+FEFF), or of two or more variation selectors. Single ones
//     are ordinary: emoji sequences, Persian and Indic text, Thai line
//     breaking.
//
// The list covers the known carriers and will grow; it is not a proof of
// absence.
func InvisibleChars(s string) []Span {
	// Every candidate is at or above U+2000, so it starts with a UTF-8
	// lead byte of 0xE2 or more. Most text has none.
	i := 0
	for i < len(s) && s[i] < 0xE2 {
		i++
	}
	if i == len(s) {
		return nil
	}

	var out []Span
	flag := func(start, end int) {
		if n := len(out); n > 0 && out[n-1].End == start {
			out[n-1].End = end
			return
		}
		out = append(out, Span{Start: start, End: end, Hidden: HiddenChars})
	}
	// A run of zero-width characters and variation selectors.
	runStart, runLen, runVS := -1, 0, 0
	endRun := func(end int) {
		if runStart >= 0 && (runLen >= 4 || runVS >= 2) {
			flag(runStart, end)
		}
		runStart, runLen, runVS = -1, 0, 0
	}

	for pos := i; pos < len(s); {
		r, size := utf8.DecodeRuneInString(s[pos:])
		switch {
		case isZeroWidth(r), isVariationSelector(r):
			if runStart < 0 {
				runStart = pos
			}
			runLen++
			if isVariationSelector(r) {
				runVS++
			}
		case r == 0x1F3F4: // black flag: the base of an emoji tag sequence
			endRun(pos)
			size += flagSequenceLen(s[pos+size:])
		case isTag(r), r == 0x202D, r == 0x202E:
			endRun(pos)
			flag(pos, pos+size)
		default:
			endRun(pos)
		}
		pos += size
	}
	endRun(len(s))
	return out
}

func isZeroWidth(r rune) bool {
	return r == 0x200B || r == 0x200C || r == 0x200D || r == 0x2060 || r == 0xFEFF
}

func isVariationSelector(r rune) bool {
	return (r >= 0xFE00 && r <= 0xFE0F) || (r >= 0xE0100 && r <= 0xE01EF)
}

func isTag(r rune) bool { return r >= 0xE0000 && r <= 0xE007F }

// flagSequenceLen returns the length in bytes of the well-formed emoji
// tag sequence that s starts with, the part that follows a black flag:
// one to seven tag letters or digits closed by the cancel tag. It returns
// 0 if there is none. The real sequences (England, Scotland, Wales) use
// five letters; the bound stops a "flag" from carrying a message.
func flagSequenceLen(s string) int {
	n, pos := 0, 0
	for pos < len(s) {
		r, size := utf8.DecodeRuneInString(s[pos:])
		if r == 0xE007F && n > 0 {
			return pos + size
		}
		if n == 7 || r < 0xE0030 || r > 0xE007A {
			return 0
		}
		n++
		pos += size
	}
	return 0
}

// HiddenWhole returns the reasons that apply to the whole block.
//
// Block.Hidden lists every reason found anywhere in the block. A reason
// that also appears on a span (or, for a table, on a HiddenCells region)
// applies only to those ranges; a reason that appears on none applies to
// the whole block.
func (b *Block) HiddenWhole() Hidden {
	whole := b.Hidden
	for i := range b.Spans {
		whole &^= b.Spans[i].Hidden
	}
	if b.Table != nil {
		for i := range b.Table.Hidden {
			whole &^= b.Table.Hidden[i].Hidden
		}
	}
	return whole
}

// VisibleText returns the block's text without anything hidden for one of
// the reasons in exclude: the empty string if such a reason applies to the
// whole block, otherwise Text with the flagged ranges cut out.
func (b *Block) VisibleText(exclude Hidden) string {
	if b.Hidden&exclude == 0 {
		return b.Text
	}
	if b.HiddenWhole()&exclude != 0 {
		return ""
	}
	var cuts [][2]int
	for _, s := range b.Spans {
		if s.Hidden&exclude != 0 && s.Start >= 0 && s.Start < s.End && s.End <= len(b.Text) {
			cuts = append(cuts, [2]int{s.Start, s.End})
		}
	}
	if len(cuts) == 0 {
		return b.Text
	}
	slices.SortFunc(cuts, func(a, c [2]int) int { return a[0] - c[0] })

	var sb strings.Builder
	keep := func(piece string) {
		// Do not leave a double space where a hidden passage was removed.
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

// visible returns the table with the cells hidden for a reason in exclude
// blanked. It returns t itself when nothing changes.
func (t *Table) visible(exclude Hidden) *Table {
	var out *Table
	for _, h := range t.Hidden {
		if h.Hidden&exclude == 0 {
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
		for r := max(h.Row, 0); r < h.Row+h.RowSpan && r < len(out.Rows); r++ {
			for c := max(h.Col, 0); c < h.Col+h.ColSpan && c < len(out.Rows[r]); c++ {
				out.Rows[r][c] = ""
			}
		}
	}
	if out == nil {
		return t
	}
	return out
}

// MarkHidden completes the hidden-text bookkeeping for a finished part.
// An extractor sets the reasons only its format can know (on Block.Hidden,
// Span.Hidden, Table.Hidden, Part.Hidden) and then calls MarkHidden once
// per part, after the part's blocks are final. It:
//
//   - scans all block text and table cells with InvisibleChars and flags
//     what it finds, so that every format detects the same characters;
//   - copies Part.Hidden onto every block, so that "b.Hidden != 0" is the
//     only test a consumer needs;
//   - adds one WarnTextHidden warning to the part if any block is flagged.
//
// It touches only the part, so page workers can call it concurrently. It
// is idempotent.
func (p *Part) MarkHidden() {
	var all Hidden
	n := 0
	for i := range p.Blocks {
		b := &p.Blocks[i]
		if b.Hidden&HiddenChars == 0 {
			if found := InvisibleChars(b.Text); found != nil {
				b.Spans = append(b.Spans, found...)
				b.Hidden |= HiddenChars
			}
			if t := b.Table; t != nil {
				for r, row := range t.Rows {
					for c, cell := range row {
						if InvisibleChars(cell) != nil {
							t.Hidden = append(t.Hidden, HiddenCells{Row: r, Col: c, RowSpan: 1, ColSpan: 1, Hidden: HiddenChars})
							b.Hidden |= HiddenChars
						}
					}
				}
			}
		}
		b.Hidden |= p.Hidden
		if b.Hidden != 0 {
			all |= b.Hidden
			n++
		}
	}
	if n == 0 || p.Has(WarnTextHidden) {
		return
	}
	// A text layer over a scan is the ordinary case; anything else is not.
	severity := SeverityWarning
	if all == HiddenTextLayer {
		severity = SeverityInfo
	}
	noun := "blocks"
	if n == 1 {
		noun = "block"
	}
	p.Warn(severity, WarnTextHidden, fmt.Sprintf("%d %s: %s", n, noun, all))
}
