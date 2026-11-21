package gofastcore

import (
	"context"
	"flag"
	"io"
	"slices"
	"strings"
	"testing"
)

func parseRenderFlags(t *testing.T, args ...string) (RenderOptions, error) {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := AddRenderFlags(fs)
	err := fs.Parse(args)
	o := RenderOptions{}
	f.Apply(&o)
	return o, err
}

func TestRenderFlagsDefaults(t *testing.T) {
	o, err := parseRenderFlags(t)
	if err != nil {
		t.Fatal(err)
	}
	want := RenderOptions{Children: true}
	if o.Furniture != want.Furniture || o.Stored || o.StoredKinds != nil || o.PartMarkers ||
		o.ExcludeDisplay != 0 || !o.Children {
		t.Fatalf("defaults: got %+v, want %+v", o, want)
	}
}

func TestRenderFlagsValues(t *testing.T) {
	for _, c := range []struct {
		args    []string
		stored  bool
		kinds   []string
		exclude Display
	}{
		{[]string{"-stored"}, true, nil, 0},
		{[]string{"-stored=all"}, true, nil, 0},
		{[]string{"-stored=true"}, true, nil, 0},
		{[]string{"-stored=false"}, false, nil, 0},
		{[]string{"-stored="}, false, nil, 0},
		{[]string{"-stored=macro,field_code"}, false, []string{"macro", "field_code"}, 0},
		{[]string{"-stored=stylesheet+generator"}, false, []string{"stylesheet", "generator"}, 0},
		{[]string{"-exclude-display"}, false, nil, DisplayAny},
		{[]string{"-exclude-display=all"}, false, nil, DisplayAny},
		{[]string{"-exclude-display=hidden"}, false, nil, DisplayHidden},
		{[]string{"-exclude-display=no_ink,hidden"}, false, nil, DisplayHidden | DisplayNoInk},
		{[]string{"-exclude-display=hidden+no_ink"}, false, nil, DisplayHidden | DisplayNoInk},
		{[]string{"-exclude-display=false"}, false, nil, 0},
	} {
		o, err := parseRenderFlags(t, c.args...)
		if err != nil {
			t.Errorf("%v: %v", c.args, err)
			continue
		}
		if o.Stored != c.stored || !slices.Equal(o.StoredKinds, c.kinds) || o.ExcludeDisplay != c.exclude {
			t.Errorf("%v: got stored %v %v, exclude %v", c.args, o.Stored, o.StoredKinds, o.ExcludeDisplay)
		}
	}
	o, err := parseRenderFlags(t, "-furniture", "-part-markers", "-children=false")
	if err != nil || !o.Furniture || !o.PartMarkers || o.Children {
		t.Errorf("bool flags: %+v, %v", o, err)
	}
}

func TestRenderFlagsErrors(t *testing.T) {
	for _, c := range []struct{ arg, want string }{
		{"-stored=nope", "macro"},
		{"-stored=,", "no kind"},
		{"-exclude-display=unknown", "hidden, no_ink"},
		{"-exclude-display=tiny", "hidden, no_ink"},
		{"-exclude-display=+", "no reason"},
	} {
		_, err := parseRenderFlags(t, c.arg)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error with %q", c.arg, err, c.want)
		}
	}
}

func TestStoredKindNamesIsACopy(t *testing.T) {
	a := StoredKindNames()
	a[0] = "changed"
	if StoredKindNames()[0] == "changed" {
		t.Fatal("StoredKindNames returned the package's own slice")
	}
	if !slices.Contains(StoredKindNames(), StoredCustomProperty) {
		t.Fatal("StoredKindNames lacks custom_property")
	}
}

func TestValidateClosedValues(t *testing.T) {
	for _, c := range []struct {
		block Block
		want  string
	}{
		{Block{Kind: KindParagraph, Text: "x", Attrs: map[string]string{AttrStored: "pdf.thing"}}, "unknown stored kind"},
		{Block{Kind: KindParagraph, Text: "x", Attrs: map[string]string{AttrRole: "banner"}}, "unknown role"},
		{Block{Kind: KindNote, Text: "x", Attrs: map[string]string{AttrNoteType: "sticky"}}, "unknown note type"},
		{Block{Kind: KindParagraph, Text: "x", Attrs: map[string]string{AttrStored: StoredGenerator}}, ""},
		{Block{Kind: KindParagraph, Text: "x", Attrs: map[string]string{AttrRole: RoleSignature}}, ""},
		{Block{Kind: KindNote, Text: "x", Attrs: map[string]string{AttrNoteType: NoteMIMEEpilogue}}, ""},
		// "type" means a note type only on a note.
		{Block{Kind: KindParagraph, Text: "x", Attrs: map[string]string{AttrNoteType: "anything"}}, ""},
	} {
		d := New("test")
		p := Part{Kind: PartBody, Number: 1, Blocks: []Block{c.block}}
		p.Finish()
		d.Parts = append(d.Parts, p)
		err := d.Validate()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%v: unexpected %v", c.block.Attrs, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%v: got %v, want %q", c.block.Attrs, err, c.want)
		}
	}
}

func TestTextBudgetExhaust(t *testing.T) {
	_, b := WithTextBudget(context.Background(), Limits{MaxTextBytes: 100})
	b.Take(10)
	b.Exhaust()
	if !b.Spent() || b.Take(1) {
		t.Fatalf("after Exhaust: spent %v, left %d", b.Spent(), b.Left())
	}
	b.Exhaust() // again: no change, no give-back
	if b.Left() >= 0 {
		t.Fatalf("left %d", b.Left())
	}
	var nilb *TextBudget
	nilb.Exhaust()

	// A child that exhausts its budget takes all it was given from its
	// parent on Close.
	ctx, parent := WithTextBudget(context.Background(), Limits{MaxTextBytes: 100})
	parent.Take(30)
	_, child := WithTextBudget(ctx, Limits{})
	child.Exhaust()
	child.Close()
	if parent.Left() != 0 {
		t.Fatalf("parent left %d, want 0", parent.Left())
	}
}

func TestDecodeBudgetSettle(t *testing.T) {
	_, d := WithDecodeBudget(context.Background(), Limits{MaxDecodedBytes: 100}, 0)
	if !d.Settle(60) || d.Left() != 60 {
		t.Fatalf("settle 60: left %d", d.Left())
	}
	if !d.Settle(80) || d.Left() != 60 {
		t.Fatalf("settle 80 gave back: left %d", d.Left())
	}
	d.Take(10)
	if !d.Settle(50) || d.Left() != 50 {
		t.Fatalf("settle 50: left %d", d.Left())
	}
	if d.Settle(-5) || d.Left() != -5 {
		t.Fatalf("settle -5: left %d", d.Left())
	}
	var nild *DecodeBudget
	if !nild.Settle(-1) {
		t.Fatal("nil budget settled false")
	}
}

// Take never gives back: a negative n is taken as 0, and Take(0) only
// reports whether the budget is spent.
func TestTextBudgetTakeNegative(t *testing.T) {
	_, b := WithTextBudget(context.Background(), Limits{MaxTextBytes: 100})
	if !b.Take(40) || !b.Take(-30) || b.Left() != 60 {
		t.Fatalf("left %d, want 60", b.Left())
	}
	if !b.Take(0) || b.Left() != 60 {
		t.Fatalf("Take(0) on a budget with 60 left: left %d", b.Left())
	}
	b.Take(61)
	if b.Take(-1000) || b.Take(0) || !b.Spent() || b.Left() != -1 {
		t.Fatalf("spent budget: left %d", b.Left())
	}
	var nilb *TextBudget
	if !nilb.Take(-1) || !nilb.Take(0) {
		t.Fatal("nil budget refused")
	}
}
