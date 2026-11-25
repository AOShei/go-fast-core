package numfmt

import (
	"bytes"
	"testing"
)

// TestCacheZeroValue: a Cache not made by NewCache (new(Cache), or a
// sync.Pool's New written so) remembers nothing, as the nil *Cache does;
// it panicked before. Found by an independent review.
func TestCacheZeroValue(t *testing.T) {
	f := Parse("#,##0")
	want, wr := f.Number([]byte("p"), 1234, false)
	var got []byte
	var gr Result
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("new(Cache).Number(%q, 1234) panics: %v; Format.Number gives %q %v", "#,##0", r, want, wr)
			}
		}()
		c := new(Cache)
		c.Reset()
		got, gr = c.Number(f, []byte("p"), 1234, false)
		if !bytes.Equal(got, want) || gr != wr {
			t.Errorf("new(Cache).Number(%q, 1234) = %q %v, want %q %v", "#,##0", got, gr, want, wr)
		}
	}()
}
