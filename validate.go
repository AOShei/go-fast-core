package gofastcore

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// Validate checks that the document obeys the model's structural contract
// and returns every violation found, joined with errors.Join.
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

func (v *validator) text(where, s string) {
	if !utf8.ValidString(s) {
		v.bad("%s is not valid UTF-8", where)
	}
}

func (v *validator) texts(where string, ss []string) {
	for i, s := range ss {
		v.text(fmt.Sprintf("%s[%d]", where, i), s)
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

	v.text("source.format", d.Source.Format)
	v.text("source.name", d.Source.Name)
	v.text("source.mime", d.Source.MIME)
	if d.Source.Size < 0 {
		v.bad("source.size is negative")
	}

	m := &d.Metadata
	v.text("metadata.title", m.Title)
	v.texts("metadata.authors", m.Authors)
	v.text("metadata.subject", m.Subject)
	v.texts("metadata.keywords", m.Keywords)
	v.text("metadata.language", m.Language)
	v.text("metadata.application", m.Application)
	v.textMap("metadata.extra", m.Extra)

	for i, o := range d.Outline {
		v.text(fmt.Sprintf("outline[%d].title", i), o.Title)
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
		if p.Width < 0 || p.Height < 0 {
			v.bad("%s: negative size", pp)
		}
		v.text(pp+".name", p.Name)
		v.warnings(pp+".warnings", p.Warnings)
		for j := range p.Blocks {
			v.block(&p.Blocks[j], fmt.Sprintf("%s.blocks[%d]", pp, j))
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
	v.text(bp+".text", b.Text)
	v.text(bp+".marker", b.Marker)
	v.textMap(bp+".attrs", b.Attrs)

	switch b.Kind {
	case KindHeading:
		if b.Level < 1 || b.Level > 6 {
			v.bad("%s: heading level %d outside 1-6", bp, b.Level)
		}
	case KindListItem, KindQuote:
		if b.Level < 1 {
			v.bad("%s: %s level must be at least 1", bp, b.Kind)
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

	for k, s := range b.Spans {
		sp := fmt.Sprintf("%s.spans[%d]", bp, k)
		v.text(sp+".href", s.Href)
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
	}
}

func (v *validator) table(t *Table, bp string) {
	cols := 0
	if len(t.Rows) > 0 {
		cols = len(t.Rows[0])
	}
	for r, row := range t.Rows {
		if len(row) != cols {
			v.bad("%s: table row %d has %d cells, want %d", bp, r, len(row), cols)
		}
		for c, cell := range row {
			if !utf8.ValidString(cell) {
				v.bad("%s.table[%d][%d] is not valid UTF-8", bp, r, c)
			}
		}
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
