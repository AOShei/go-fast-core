package gofastcore

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Validate checks that the document obeys the model's structural contract
// and the mechanical text conventions (valid UTF-8, no control characters,
// no unexpanded ligatures in text, trimmed non-empty block text) and
// returns every violation found, joined with errors.Join. It also checks
// that Part.Finish was called on every part, that the lists consumers
// iterate are not nil, and that every Attrs["stored"], note Attrs["type"]
// and Attrs["role"] is one of the package's values.
//
// It is a check on producers. It walks the whole document, so call it from
// tests rather than on the hot path, and it rejects the Unknown enum
// values that only arise when decoding output from a newer extractor.
func (d *Document) Validate() error {
	v := &validator{}
	v.document(d, "doc", true)
	return errors.Join(v.errs...)
}

type validator struct {
	errs []error
	path string
}

func (v *validator) bad(format string, args ...any) {
	v.errs = append(v.errs, fmt.Errorf(v.path+": "+format, args...))
}

// textProblem reports how s breaks the text conventions, or "" if it does
// not. Every string must be valid UTF-8 without control characters; prose
// additionally has its ligatures expanded, and trimmed prose has no
// surrounding whitespace.
func textProblem(s string, prose, trimmed bool) string {
	if !utf8.ValidString(s) {
		return "is not valid UTF-8"
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return fmt.Sprintf("contains control character U+%04X", r)
		}
	}
	if prose && strings.ContainsAny(s, "\uFB00\uFB01\uFB02\uFB03\uFB04\uFB05\uFB06") {
		return "contains an unexpanded ligature (U+FB00 to U+FB06)"
	}
	if trimmed && s != "" {
		first, _ := utf8.DecodeRuneInString(s)
		last, _ := utf8.DecodeLastRuneInString(s)
		if unicode.IsSpace(first) || unicode.IsSpace(last) {
			return "has leading or trailing whitespace"
		}
	}
	return ""
}

func (v *validator) text(where, s string) {
	if p := textProblem(s, false, false); p != "" {
		v.bad("%s %s", where, p)
	}
}

// words checks a string that is text to be read (a name, a title, a
// label), as opposed to an identifier or an address: besides what every
// string obeys, its ligatures are expanded.
func (v *validator) words(where, s string) {
	if p := textProblem(s, true, false); p != "" {
		v.bad("%s %s", where, p)
	}
}

func (v *validator) wordsEach(where string, ss []string) {
	for i, s := range ss {
		v.words(fmt.Sprintf("%s[%d]", where, i), s)
	}
}

func (v *validator) textMap(where string, m map[string]string) {
	for k, s := range m {
		v.text(where+" key", k)
		v.text(fmt.Sprintf("%s[%q]", where, k), s)
	}
}

func (v *validator) warnings(where string, ws []Warning) {
	for i, w := range ws {
		if w.Code == "" {
			v.bad("%s[%d]: empty code", where, i)
		}
		if !w.Severity.Valid() {
			v.bad("%s[%d] (%s): invalid severity %d", where, i, w.Code, w.Severity)
		}
		v.text(fmt.Sprintf("%s[%d].message", where, i), w.Message)
	}
}

func (v *validator) document(d *Document, path string, top bool) {
	v.path = path
	if d.SchemaVersion == "" {
		v.bad("schema_version is empty")
	}
	switch {
	case len(d.Parts) > 0:
		if d.Source.Format == "" {
			v.bad("source.format is empty")
		}
	case top:
		v.bad("document has no parts")
	case len(d.Warnings) == 0:
		// A child may be listed without content, but must say why.
		v.bad("child has no parts and no warning explaining why")
	}

	if d.Parts == nil {
		v.bad("parts is nil; use New, or an empty list, so that JSON has [] and not null")
	}
	v.text("source.format", d.Source.Format)
	v.text("source.name", d.Source.Name)
	v.text("source.mime", d.Source.MIME)
	if d.Source.Size < 0 {
		v.bad("source.size is negative")
	}

	m := &d.Metadata
	v.words("metadata.title", m.Title)
	v.wordsEach("metadata.authors", m.Authors)
	v.words("metadata.subject", m.Subject)
	v.wordsEach("metadata.keywords", m.Keywords)
	v.text("metadata.language", m.Language)
	v.text("metadata.application", m.Application)
	v.textMap("metadata.extra", m.Extra)

	for i, o := range d.Outline {
		v.words(fmt.Sprintf("outline[%d].title", i), o.Title)
		if o.Level < 1 {
			v.bad("outline[%d]: level %d is below 1", i, o.Level)
		}
		if o.Part < 0 {
			v.bad("outline[%d]: negative part number", i)
		}
	}

	v.warnings("warnings", d.Warnings)

	last := 0
	for i := range d.Parts {
		p := &d.Parts[i]
		pp := fmt.Sprintf("parts[%d]", i)
		if !p.Kind.Valid() {
			v.bad("%s: invalid kind %d", pp, p.Kind)
		}
		if p.Number <= last {
			v.bad("%s: number %d does not increase (previous %d)", pp, p.Number, last)
		}
		last = max(last, p.Number)
		if p.Kind == PartStored {
			if i != len(d.Parts)-1 {
				v.bad("%s: a stored part must be the last part", pp)
			}
			if p.Display != 0 {
				v.bad("%s: a stored part carries no part-level display reason (%s)", pp, p.Display)
			}
		}
		if p.Width < 0 || p.Height < 0 {
			v.bad("%s: negative size", pp)
		}
		v.words(pp+".name", p.Name)
		v.warnings(pp+".warnings", p.Warnings)
		if p.Blocks == nil {
			v.bad("%s: blocks is nil; call Part.Finish, so that JSON has [] and not null", pp)
		}
		if !p.Display.Valid() {
			v.bad("%s: invalid display reasons %#x", pp, uint16(p.Display))
		}
		for j := range p.Blocks {
			b := &p.Blocks[j]
			bp := fmt.Sprintf("%s.blocks[%d]", pp, j)
			v.block(b, bp)
			if b.Display&p.Display != p.Display {
				v.bad("%s: lacks the display reasons of its part (%s); call Part.Finish", bp, p.Display)
			}
		}
	}

	for i, c := range d.Children {
		cp := fmt.Sprintf("%s.children[%d]", path, i)
		if c == nil {
			v.path = cp
			v.bad("nil child")
			continue
		}
		v.document(c, cp, false)
	}
	v.path = path
}

func (v *validator) block(b *Block, bp string) {
	if !b.Kind.Valid() {
		v.bad("%s: invalid kind %d", bp, b.Kind)
		return
	}
	if !b.Origin.Valid() {
		v.bad("%s: invalid origin %d", bp, b.Origin)
	}
	// Code is verbatim; everything else is trimmed prose.
	if p := textProblem(b.Text, b.Kind != KindCode, b.Kind != KindCode); p != "" {
		v.bad("%s.text %s", bp, p)
	}
	if b.Text == "" && b.Kind != KindTable && b.Kind != KindImage && b.Kind != KindThematicBreak {
		v.bad("%s: %s block has empty text", bp, b.Kind)
	}
	v.words(bp+".marker", b.Marker)
	v.textMap(bp+".attrs", b.Attrs)
	if s, ok := b.Attrs[AttrStored]; ok && !slices.Contains(storedKindNames, s) {
		v.bad("%s: unknown stored kind %q", bp, s)
	}
	if s, ok := b.Attrs[AttrRole]; ok && !slices.Contains(roleNames, s) {
		v.bad("%s: unknown role %q", bp, s)
	}
	if s, ok := b.Attrs[AttrNoteType]; ok && b.Kind == KindNote && !slices.Contains(noteTypeNames, s) {
		v.bad("%s: unknown note type %q", bp, s)
	}
	if !b.Display.Valid() {
		v.bad("%s: invalid display reasons %#x", bp, uint16(b.Display))
	}

	switch b.Kind {
	case KindHeading:
		if b.Level < 1 || b.Level > 6 {
			v.bad("%s: heading level %d outside 1-6", bp, b.Level)
		}
	case KindListItem, KindQuote:
		if b.Level < 1 {
			v.bad("%s: %s level must be at least 1", bp, b.Kind)
		}
	case KindNote:
		if b.Attrs[AttrNoteType] == NoteField && b.Attrs[AttrStored] != StoredFieldCode {
			v.bad("%s: a field note must be marked %s=%s", bp, AttrStored, StoredFieldCode)
		}
	}
	if (b.Kind == KindTable) != (b.Table != nil) {
		v.bad("%s: table payload must be set exactly when kind is table", bp)
	}
	if (b.Kind == KindImage) != (b.Image != nil) {
		v.bad("%s: image payload must be set exactly when kind is image", bp)
	}
	if b.Kind == KindTable && b.Text != "" {
		v.bad("%s: table block must not carry text", bp)
	}
	if b.Kind == KindThematicBreak {
		if b.Text != "" || len(b.Spans) > 0 {
			v.bad("%s: thematic break must not carry text or spans", bp)
		}
		if b.Continued {
			v.bad("%s: thematic break cannot be continued", bp)
		}
		if b.Origin != OriginNative {
			v.bad("%s: a thematic break is declared by the source, never inferred", bp)
		}
	}

	for k, s := range b.Spans {
		sp := fmt.Sprintf("%s.spans[%d]", bp, k)
		v.text(sp+".href", s.Href)
		if s.Display&^b.Display != 0 {
			v.bad("%s: display reasons %s are not in the block's (%s)", sp, s.Display, b.Display)
		}
		if s.Start < 0 || s.End > len(b.Text) || s.Start >= s.End {
			v.bad("%s: range [%d,%d) outside text of length %d", sp, s.Start, s.End, len(b.Text))
			continue
		}
		if !utf8.RuneStart(b.Text[s.Start]) || (s.End < len(b.Text) && !utf8.RuneStart(b.Text[s.End])) {
			v.bad("%s: range [%d,%d) splits a UTF-8 character", sp, s.Start, s.End)
		}
	}
	if r := b.Loc.BBox; r.X1 < r.X0 || r.Y1 < r.Y0 {
		v.bad("%s: bbox has negative size", bp)
	}
	v.text(bp+".loc.range", b.Loc.Range)
	v.text(bp+".loc.anchor", b.Loc.Anchor)
	if b.Loc.Offset < 0 || b.Loc.Length < 0 {
		v.bad("%s: negative source offset or length", bp)
	}
	if img := b.Image; img != nil {
		v.text(bp+".image.id", img.ID)
		v.text(bp+".image.mime", img.MIME)
		if img.Width < 0 || img.Height < 0 {
			v.bad("%s: negative image size", bp)
		}
	}
	if t := b.Table; t != nil {
		v.table(t, bp)
		cols := 0
		if len(t.Rows) > 0 {
			cols = len(t.Rows[0])
		}
		for k, d := range t.Display {
			if d.Row < 0 || d.Col < 0 || d.RowSpan < 1 || d.ColSpan < 1 ||
				d.Row+d.RowSpan > len(t.Rows) || d.Col+d.ColSpan > cols {
				v.bad("%s: display region %d (%+v) is outside the %dx%d grid", bp, k, d, len(t.Rows), cols)
			}
			if d.Display == 0 || d.Display&^b.Display != 0 {
				v.bad("%s: display region %d has reasons %q, which must be non-empty and in the block's (%s)", bp, k, d.Display, b.Display)
			}
		}
	}
}

func (v *validator) table(t *Table, bp string) {
	cols := 0
	if len(t.Rows) > 0 {
		cols = len(t.Rows[0])
	}
	if cols == 0 {
		v.bad("%s: table has no cells; a table has at least one row and one column", bp)
	}
	for r, row := range t.Rows {
		if len(row) != cols {
			v.bad("%s: table row %d has %d cells, want %d", bp, r, len(row), cols)
		}
		for c, cell := range row {
			if p := textProblem(cell, true, false); p != "" {
				v.bad("%s.table[%d][%d] %s", bp, r, c, p)
			}
		}
	}
	prev := [2]int{-1, -1}
	for k, c := range t.Cells {
		cp := fmt.Sprintf("%s.table.cells[%d]", bp, k)
		if c.Row < 0 || c.Col < 0 || c.Row >= len(t.Rows) || c.Col >= cols {
			v.bad("%s: cell (%d,%d) outside the table", cp, c.Row, c.Col)
		}
		if c.Row < prev[0] || c.Row == prev[0] && c.Col <= prev[1] {
			v.bad("%s: cells not in order of row, then column, or a cell twice", cp)
		}
		prev = [2]int{c.Row, c.Col}
		if c.Href == "" && c.Formula == "" && c.Type == CellUnset {
			v.bad("%s: an entry that says nothing", cp)
		}
		if c.Type != CellUnset && !c.Type.Valid() {
			v.bad("%s: invalid type %d", cp, c.Type)
		}
		if c.Value != "" && c.Type == CellUnset {
			v.bad("%s: a value without a type", cp)
		}
		v.text(cp+".href", c.Href)
		v.text(cp+".formula", c.Formula) // verbatim, as the source writes it
		v.text(cp+".value", c.Value)
	}
	if t.HeaderRows < 0 || t.HeaderRows > len(t.Rows) {
		v.bad("%s: header_rows %d outside 0-%d", bp, t.HeaderRows, len(t.Rows))
	}
	for k, m := range t.Merges {
		if m.Row < 0 || m.Col < 0 || m.RowSpan < 1 || m.ColSpan < 1 ||
			m.RowSpan*m.ColSpan == 1 ||
			m.Row+m.RowSpan > len(t.Rows) || m.Col+m.ColSpan > cols {
			v.bad("%s: merge %d (%+v) is degenerate or outside the %dx%d grid", bp, k, m, len(t.Rows), cols)
		}
	}
}
