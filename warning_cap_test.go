package gofastcore

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func fill(p *Part, n int, sev Severity) {
	for i := 0; i < n; i++ {
		p.Warn(sev, "x.many", "many")
	}
}

func lastWarning(ws []Warning) Warning { return ws[len(ws)-1] }

func TestPartWarnCap(t *testing.T) {
	var p Part
	fill(&p, MaxWarnings, SeverityInfo)
	if len(p.Warnings) != MaxWarnings {
		t.Fatalf("%d warnings, want %d", len(p.Warnings), MaxWarnings)
	}
	p.Warn(SeverityInfo, "x.over", "first over")
	if w := lastWarning(p.Warnings); w.Code != WarnWarningsOmitted || w.Message != "1 more warning is not listed; at most 1000 are kept per part" || w.Severity != SeverityInfo {
		t.Errorf("after one: %+v", w)
	}
	p.Warn(SeverityError, "x.over", "second over")
	p.Warn(SeverityWarning, "x.over", "third over")
	if len(p.Warnings) != MaxWarnings+1 {
		t.Fatalf("%d warnings, want %d", len(p.Warnings), MaxWarnings+1)
	}
	if w := lastWarning(p.Warnings); w.Severity != SeverityError || !strings.HasPrefix(w.Message, "3 more warnings are not listed") {
		t.Errorf("after three: %+v", w)
	}
	if p.Has("x.over") {
		t.Error("an omitted warning is listed")
	}
}

func TestDocumentWarnCap(t *testing.T) {
	d := New("txt")
	for i := 0; i < 5*MaxWarnings; i++ {
		d.Warn(SeverityInfo, "x.many", "many")
	}
	if len(d.Warnings) != MaxWarnings+1 {
		t.Fatalf("%d warnings, want %d", len(d.Warnings), MaxWarnings+1)
	}
	if w := lastWarning(d.Warnings); w.Severity != SeverityInfo || !strings.HasPrefix(w.Message, "4000 more") || !strings.Contains(w.Message, "per document") {
		t.Errorf("last warning %+v", w)
	}
}

// The count lives in the summary, so it survives JSON, and a decoded
// document goes on counting in the same summary.
func TestWarnCapAfterJSON(t *testing.T) {
	d := New("txt")
	p := Part{Kind: PartBody, Number: 1, Blocks: []Block{{Kind: KindParagraph, Text: "x"}}}
	fill(&p, MaxWarnings+5, SeverityError)
	p.Finish()
	d.Parts = append(d.Parts, p)
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var e Document
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.Parts[0].Warnings, e.Parts[0].Warnings) {
		t.Fatal("warnings differ after a JSON round trip")
	}
	e.Parts[0].Warn(SeverityInfo, "x.more", "more")
	ws := e.Parts[0].Warnings
	if len(ws) != MaxWarnings+1 {
		t.Fatalf("%d warnings, want %d", len(ws), MaxWarnings+1)
	}
	if w := lastWarning(ws); w.Severity != SeverityError || !strings.HasPrefix(w.Message, "6 more") {
		t.Errorf("last warning %+v", w)
	}
}

// An extractor that appends after the summary: the next Warn moves the
// summary back to the end and keeps its count and severity.
func TestWarnCapAfterDirectAppend(t *testing.T) {
	var p Part
	fill(&p, MaxWarnings+2, SeverityError)
	p.Warnings = append(p.Warnings, Warning{Code: "x.direct", Severity: SeverityInfo, Message: "direct"})
	p.Warn(SeverityInfo, "x.over", "over")
	n := 0
	for _, w := range p.Warnings {
		if w.Code == WarnWarningsOmitted {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d summaries, want 1", n)
	}
	if w := lastWarning(p.Warnings); w.Code != WarnWarningsOmitted || w.Severity != SeverityError || !strings.HasPrefix(w.Message, "3 more") {
		t.Errorf("last warning %+v", w)
	}
	if w := p.Warnings[len(p.Warnings)-2]; w.Code != "x.direct" {
		t.Errorf("the direct warning moved: %+v", w)
	}
	// A list filled past the bound by direct appends alone gets a summary.
	var q Part
	for i := 0; i < MaxWarnings+5; i++ {
		q.Warnings = append(q.Warnings, Warning{Code: "x.direct", Severity: SeverityInfo})
	}
	q.Warn(SeverityWarning, "x.over", "over")
	if w := lastWarning(q.Warnings); w.Code != WarnWarningsOmitted || !strings.HasPrefix(w.Message, "1 more warning ") {
		t.Errorf("last warning %+v", w)
	}
}

// Clearing the list starts again; nothing is remembered beside it.
func TestWarnCapAfterClear(t *testing.T) {
	var p Part
	fill(&p, MaxWarnings+3, SeverityInfo)
	p.Warnings = nil
	p.Warn(SeverityInfo, "x.new", "new")
	if len(p.Warnings) != 1 || p.Warnings[0].Code != "x.new" {
		t.Errorf("after clearing: %+v", p.Warnings)
	}
}

// An invalid severity does not raise the summary's.
func TestWarnCapInvalidSeverity(t *testing.T) {
	var p Part
	fill(&p, MaxWarnings, SeverityInfo)
	p.Warn(SeverityInfo, "x.over", "over")
	p.Warn(Severity(99), "x.bad", "bad")
	if w := lastWarning(p.Warnings); w.Severity != SeverityInfo || !strings.HasPrefix(w.Message, "2 more") {
		t.Errorf("last warning %+v", w)
	}
	var q Part
	fill(&q, MaxWarnings, SeverityInfo)
	q.Warn(Severity(0), "x.bad", "bad")
	if w := lastWarning(q.Warnings); w.Severity != SeverityWarning {
		t.Errorf("summary of invalid severities only: %+v", w)
	}
}

// The summary validates.
func TestWarnCapValidates(t *testing.T) {
	d := New("txt")
	p := Part{Kind: PartBody, Number: 1}
	p.Blocks = append(p.Blocks, Block{Kind: KindParagraph, Text: "x"})
	fill(&p, MaxWarnings+3, SeverityInfo)
	p.Finish()
	d.Parts = append(d.Parts, p)
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkWarnPastCap(b *testing.B) {
	var p Part
	fill(&p, MaxWarnings+1, SeverityInfo)
	b.ReportAllocs()
	for b.Loop() {
		p.Warn(SeverityInfo, "x.over", "over")
	}
}
