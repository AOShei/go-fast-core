package gofastcore

import (
	"flag"
	"fmt"
	"slices"
	"strings"
)

// RenderFlags are the command-line flags every go-fast command offers for
// RenderOptions, with the same names, values and meaning in each:
//
//	-furniture                  Furniture
//	-stored[=kinds]             Stored, StoredKinds
//	-part-markers               PartMarkers
//	-exclude-display[=reasons]  ExcludeDisplay
//	-children (default true)    Children
//
// A command defines them with AddRenderFlags and, after parsing, sets its
// RenderOptions with Apply.
type RenderFlags struct {
	furniture   bool
	partMarkers bool
	children    bool
	stored      storedFlag
	exclude     displayFlag
}

// AddRenderFlags defines the shared render flags on fs and returns them.
func AddRenderFlags(fs *flag.FlagSet) *RenderFlags {
	f := &RenderFlags{}
	fs.BoolVar(&f.furniture, "furniture", false,
		"in Markdown and text, include running headers and footers")
	fs.Var(&f.stored, "stored",
		"in Markdown and text, include stored data (data the file keeps but shows nowhere): all of it, or with =kinds only those kinds (such as markup_comment, field_code, macro)")
	fs.BoolVar(&f.partMarkers, "part-markers", false,
		"in Markdown and text, put a line before each part saying which part it is")
	fs.Var(&f.exclude, "exclude-display",
		"in Markdown and text, leave out what the file declares not displayed: all of it, or with =reasons only those reasons ("+strings.Join(displayNames[:], ", ")+"); JSON always has everything")
	fs.BoolVar(&f.children, "children", true,
		"in Markdown and text, include embedded documents (attachments, members), each after a line naming it; -children=false leaves them out")
	return f
}

// Apply sets the fields of o that the render flags control, and leaves
// the others.
func (f *RenderFlags) Apply(o *RenderOptions) {
	o.Furniture = f.furniture
	o.Stored = f.stored.all
	o.StoredKinds = slices.Clone(f.stored.kinds)
	o.PartMarkers = f.partMarkers
	o.ExcludeDisplay = f.exclude.reasons
	o.Children = f.children
}

// splitNames splits a flag value on "," and "+".
func splitNames(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '+' })
}

// storedFlag is -stored: given alone, =true or =all it stands for every
// kind of stored data, and with =k1,k2 (or k1+k2) for the kinds named.
type storedFlag struct {
	all   bool
	kinds []string
}

func (s *storedFlag) IsBoolFlag() bool { return true }

func (s *storedFlag) String() string {
	if s == nil {
		return ""
	}
	if s.all {
		return "all"
	}
	return strings.Join(s.kinds, ",")
}

func (s *storedFlag) Set(v string) error {
	switch v {
	case "true", "all":
		s.all, s.kinds = true, nil
		return nil
	case "false", "":
		s.all, s.kinds = false, nil
		return nil
	}
	var kinds []string
	for _, k := range splitNames(v) {
		if !slices.Contains(storedKindNames, k) {
			return fmt.Errorf("unknown kind %q; the kinds are %s", k, strings.Join(storedKindNames, ", "))
		}
		kinds = append(kinds, k)
	}
	if len(kinds) == 0 {
		return fmt.Errorf("no kind in %q; the kinds are %s", v, strings.Join(storedKindNames, ", "))
	}
	s.all, s.kinds = false, kinds
	return nil
}

// displayFlag is -exclude-display: given alone, =true or =all it stands
// for every reason, and with =r1,r2 (or r1+r2) for the reasons named.
type displayFlag struct{ reasons Display }

func (h *displayFlag) IsBoolFlag() bool { return true }

func (h *displayFlag) String() string {
	if h == nil || h.reasons == 0 {
		return ""
	}
	if h.reasons == DisplayAny {
		return "all"
	}
	return h.reasons.String()
}

func (h *displayFlag) Set(v string) error {
	switch v {
	case "true", "all":
		h.reasons = DisplayAny
		return nil
	case "false", "":
		h.reasons = 0
		return nil
	}
	var reasons Display
	for _, name := range splitNames(v) {
		i := slices.Index(displayNames[:], name)
		if i < 0 {
			return fmt.Errorf("unknown reason %q; the reasons are %s", name, strings.Join(displayNames[:], ", "))
		}
		reasons |= 1 << i
	}
	if reasons == 0 {
		return fmt.Errorf("no reason in %q; the reasons are %s", v, strings.Join(displayNames[:], ", "))
	}
	h.reasons = reasons
	return nil
}
