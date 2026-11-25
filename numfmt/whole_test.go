package numfmt

import (
	"bufio"
	"bytes"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
)

// withoutWhole is f with the whole-number path turned off: what the
// general steps give.
func withoutWhole(f *Format) *Format {
	g := *f
	g.sections = append([]section(nil), f.sections...)
	for i := range g.sections {
		g.sections[i].whole = false
	}
	return &g
}

// wholeCodes are formats to try: the LibreOffice table's, the built-in
// ones, and shapes near the edge of a plain layout.
func wholeCodes(t testing.TB) []string {
	seen := map[string]bool{}
	var codes []string
	add := func(c string) {
		if !seen[c] {
			seen[c] = true
			codes = append(codes, c)
		}
	}
	f, err := os.Open("testdata/libreoffice.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		add(strings.SplitN(sc.Text(), "\t", 2)[0])
	}
	for id := 0; id < 200; id++ {
		add(Builtin(id))
	}
	for _, c := range []string{
		"0", "#", "?", "00000", "#,##0", "#,###", "0,000", "??,??0", "#,##0.00", "#,##0.##", "0.??", "0.", "0.#0?",
		`"$"#,##0_);[Red]\("$"#,##0\)`, `_-* #,##0_-;\-* #,##0_-;_-* "-"??_-;_-@_-`, `#,##0_ ;\-#,##0\ `,
		`_("$"* #,##0.00_);_("$"* \(#,##0.00\);_("$"* "-"??_);_(@_)`, `"£"#,##0.00`, `[$€-407] #,##0`,
		`#,##0" kg"`, `"x"0"y"0`, `000-00-0000`, `0 0`, `0%`, `#,##0,`, `0,,`, `0.00E+00`, `# ?/?`, `0/4`,
		`[>=100]0;[<0]"n";0.0`, `[<=-5]0.0;0`, `0;0;`, `;;;`, `0;-0;"zero"`, `"text only"`, `  0  `, `0;;0.0`,
		`* #,##0`, `_(0_)`, `0.0;(0.0);-`, `#,##0;#,##0;`, `[Red]0;[Blue]-0`, `@`, `General`, `0;@`,
	} {
		add(c)
	}
	return codes
}

func wholeValues() []float64 {
	vs := []float64{0, math.Copysign(0, -1), 1e15, -1e15, 1e15 - 1, -(1e15 - 1), 0.5, -0.5, 1.000001, 999.9999, 1e16, 1 << 53}
	for _, n := range []float64{1, 9, 10, 99, 100, 999, 1000, 1001, 9999, 10000, 12345, 99999, 100000, 999999, 1e6, 1234567, 123456789, 1e9, 1e12, 99999999999999, 1e14} {
		vs = append(vs, n, -n, n+0.5, -n-0.25)
	}
	r := rand.New(rand.NewPCG(5, 6))
	for i := 0; i < 200; i++ {
		v := math.Trunc(math.Pow(10, r.Float64()*15))
		if r.IntN(2) == 0 {
			v = -v
		}
		vs = append(vs, v)
	}
	return vs
}

// TestWholeIsNumber: through a plain layout, a whole number is written as
// the general steps write it, bytes and Result, after a prefix in dst.
func TestWholeIsNumber(t *testing.T) {
	codes, values := wholeCodes(t), wholeValues()
	fast := 0
	for _, code := range codes {
		f := Parse(code)
		g := withoutWhole(f)
		for _, v := range values {
			for _, d1904 := range []bool{false, true} {
				got, gr := f.Number([]byte("p"), v, d1904)
				want, wr := g.Number([]byte("p"), v, d1904)
				if !bytes.Equal(got, want) || gr != wr {
					t.Fatalf("%q of %v: %q %v, the general steps give %q %v", code, v, got, gr, want, wr)
				}
				if s, _ := f.pick(v); s != nil && s.whole && v == math.Trunc(v) && math.Abs(v) < 1e15 {
					fast++
				}
			}
		}
	}
	if fast < 30000 {
		t.Errorf("only %d calls took the whole-number path", fast)
	}
}

// TestWholeLayouts: which shapes are plain.
func TestWholeLayouts(t *testing.T) {
	for code, want := range map[string]bool{
		"0": true, "#,##0": true, "0.00": true, "#,##0.##": true, "??0.0?": true, `"$"#,##0_)`: true,
		`_-* #,##0_-`: true, `#,##0" kg"`: true, `[Red]0`: true, `[>=100]0`: true,
		"0%": false, "#,##0,": false, "0.00E+00": false, "# ?/?": false, "000-00-0000": false,
		`"x"0"y"0`: false, ".00": false, `"text"`: false, "@": false, "General": false, "yyyy": false,
	} {
		s := Parse(code).sections[0]
		if s.whole != want {
			t.Errorf("%q: plain %v, want %v", code, s.whole, want)
		}
	}
}

// FuzzWhole: any code and any whole number give what the general steps
// give.
func FuzzWhole(f *testing.F) {
	for _, code := range []string{"#,##0", "0.00", `_-* #,##0_-;\-* #,##0_-;_-* "-"??_-;_-@_-`, "??,??0.0?", `[<0]"n"0;0`, "0;;", "#"} {
		f.Add(code, int64(-1234567), false)
		f.Add(code, int64(0), true)
	}
	f.Fuzz(func(t *testing.T, code string, n int64, d1904 bool) {
		v := float64(n % 1e15)
		format := Parse(code)
		got, gr := format.Number([]byte("p"), v, d1904)
		want, wr := withoutWhole(format).Number([]byte("p"), v, d1904)
		if !bytes.Equal(got, want) || gr != wr {
			t.Fatalf("%q of %v: %q %v, the general steps give %q %v", code, v, got, gr, want, wr)
		}
	})
}
