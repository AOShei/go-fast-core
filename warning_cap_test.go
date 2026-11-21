package gofastcore

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPartWarnCap(t *testing.T) {
	var p Part
	for i := 0; i < MaxWarnings; i++ {
		p.Warn(SeverityInfo, "x.kept", "kept")
	}
	if len(p.Warnings) != MaxWarnings {
		t.Fatalf("%d warnings, want %d", len(p.Warnings), MaxWarnings)
	}
	p.Warn(SeverityInfo, "x.over", "first over")
	p.Warn(SeverityError, "x.over", "second over")
	p.Warn(SeverityWarning, "x.over", "third over")
	if len(p.Warnings) != MaxWarnings+1 {
		t.Fatalf("%d warnings, want %d", len(p.Warnings), MaxWarnings+1)
	}
	last := p.Warnings[MaxWarnings]
	if last.Code != WarnWarningsOmitted || last.Severity != SeverityError {
		t.Errorf("last warning %+v, want %s at error", last, WarnWarningsOmitted)
	}
	if !strings.HasPrefix(last.Message, "3 more warnings are not listed") || !strings.Contains(last.Message, "per part") {
		t.Errorf("message %q", last.Message)
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
	last := d.Warnings[MaxWarnings]
	if last.Code != WarnWarningsOmitted || last.Severity != SeverityInfo || !strings.HasPrefix(last.Message, "4000 more") || !strings.Contains(last.Message, "per document") {
		t.Errorf("last warning %+v", last)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "omitted\":") {
		t.Error("the counter is written to JSON")
	}
}

// A list that an extractor filled past the bound by itself, or edited
// after Warn, still gets one summary at its end.
func TestWarnCapAfterDirectAppend(t *testing.T) {
	var p Part
	for i := 0; i < MaxWarnings+5; i++ {
		p.Warnings = append(p.Warnings, Warning{Code: "x.direct", Severity: SeverityInfo})
	}
	p.Warn(SeverityWarning, "x.over", "over")
	p.Warn(SeverityInfo, "x.over", "over")
	if n := len(p.Warnings); n != MaxWarnings+6 {
		t.Fatalf("%d warnings, want %d", n, MaxWarnings+6)
	}
	if last := p.Warnings[len(p.Warnings)-1]; last.Code != WarnWarningsOmitted || last.Severity != SeverityWarning || !strings.HasPrefix(last.Message, "2 more") {
		t.Errorf("last warning %+v", last)
	}
	p.Warnings = p.Warnings[:len(p.Warnings)-1] // an extractor drops the summary
	p.Warn(SeverityInfo, "x.over", "over")
	if last := p.Warnings[len(p.Warnings)-1]; last.Code != WarnWarningsOmitted || !strings.HasPrefix(last.Message, "3 more") {
		t.Errorf("last warning %+v", last)
	}
}

// The summary validates, and a part is still valid with it.
func TestWarnCapValidates(t *testing.T) {
	d := New("txt")
	p := Part{Kind: PartBody, Number: 1}
	p.Blocks = append(p.Blocks, Block{Kind: KindParagraph, Text: "x"})
	for i := 0; i < MaxWarnings+3; i++ {
		p.Warn(SeverityInfo, "x.many", "many")
	}
	p.Finish()
	d.Parts = append(d.Parts, p)
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
}
