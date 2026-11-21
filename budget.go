package gofastcore

import (
	"context"
	"sync/atomic"
)

// TextBudget counts the text a document may still produce, against
// Limits.MaxTextBytes.
//
// A file can be small and say a great deal: one long string that every
// cell of a sheet refers to, a form drawn on every page. What is repeated
// is stored once, so the limit on decompressed bytes does not see it, and
// the document that results holds, or writes out, the text as often as
// it occurs. The budget is what bounds that.
//
// An extractor gets the budget of the document it is reading from
// WithTextBudget, takes from it BlockCost and the bytes of the text of every
// block, CellCost and the bytes of every table
// cell it produces, and when Take reports false stops reading the part,
// keeps what it has and raises WarnLimitExceeded there. It calls Close
// when the document is done.
//
// Text outside blocks is taken from the budget as well, wherever the
// file decides both how long it is and how often it occurs: outline
// titles, a name made for every part, list markers, link targets. A
// string the extractor holds to the length of a name need not be. The
// outline is counted before the first part and a part's name with the
// part, so that the place of the cut does not depend on timing.
//
// The budget is the document's and its embedded documents' together. The
// context WithTextBudget returns carries the budget; an extractor passes
// that context to Options.ExtractChild, and the child's extractor, of
// whatever library, gets from WithTextBudget a budget of its own that is
// no larger than what its parent has left, and on Close takes what it
// used from its parent. Embedded documents are extracted one after
// another, so what each may use does not depend on timing.
//
// A nil *TextBudget has no limit. A TextBudget is safe for concurrent
// use.
type TextBudget struct {
	left   atomic.Int64
	limit  int64
	parent *TextBudget
	closed atomic.Bool
}

type textBudgetKey struct{}

// WithTextBudget returns the budget for one reading of a document, and
// ctx carrying it. The budget is limits.MaxTextBytes (DefaultMaxTextBytes
// if zero, none if negative), or what the document this one is embedded
// in has left, if ctx carries that and it is less.
func WithTextBudget(ctx context.Context, limits Limits) (context.Context, *TextBudget) {
	b := &TextBudget{limit: ResolveLimit(limits.MaxTextBytes, DefaultMaxTextBytes)}
	if parent, ok := ctx.Value(textBudgetKey{}).(*TextBudget); ok && parent != nil {
		b.parent = parent
		b.limit = min(b.limit, max(parent.Left(), 0))
	}
	b.left.Store(b.limit)
	return context.WithValue(ctx, textBudgetKey{}, b), b
}

// TextBudgetFrom returns the budget ctx carries, or nil.
func TextBudgetFrom(ctx context.Context) *TextBudget {
	b, _ := ctx.Value(textBudgetKey{}).(*TextBudget)
	return b
}

// Take takes n bytes of text from the budget and reports whether they
// were there to take. Once it has reported false the budget is spent:
// the extractor keeps what it had before the call and stops. n is not
// negative; Return gives bytes back.
func (b *TextBudget) Take(n int) bool {
	return b == nil || b.left.Add(-int64(n)) >= 0
}

// Left returns the bytes not yet taken; a negative number once more was
// asked for than there was.
func (b *TextBudget) Left() int64 {
	if b == nil {
		return 1<<63 - 1
	}
	return b.left.Load()
}

// Spent reports whether Take has reported false.
func (b *TextBudget) Spent() bool { return b != nil && b.left.Load() < 0 }

// Exhaust spends what is left, so that Spent reports true and every later
// Take reports false: for an extractor that has stopped at the limit by a
// count of its own (an outline already cut at the limit) and so has
// nothing left for what follows. It gives nothing back if more was taken
// than there was.
func (b *TextBudget) Exhaust() {
	if b == nil {
		return
	}
	for {
		l := b.left.Load()
		if l < 0 || b.left.CompareAndSwap(l, -1) {
			return
		}
	}
}

// Return gives back n bytes that were taken, for an extractor that reads
// a part again and will take them a second time.
func (b *TextBudget) Return(n int64) {
	if b != nil && n > 0 {
		b.left.Add(n)
	}
}

// Reset gives back everything taken, for an extractor that reads the
// document again from the start (as one that reads parts side by side
// may, to cut the document at the same place whatever the number of
// workers).
func (b *TextBudget) Reset() {
	if b != nil {
		b.left.Store(b.limit)
	}
}

// Close ends the reading: what the document took is taken from the
// budget of the document it is embedded in, if there is one. Later calls
// do nothing.
func (b *TextBudget) Close() {
	if b == nil || b.parent == nil || b.closed.Swap(true) {
		return
	}
	used := b.limit - max(b.left.Load(), 0)
	b.parent.left.Add(-used)
}
