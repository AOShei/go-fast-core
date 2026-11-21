package gofastcore

import "context"

// DecodeBudget counts the bytes a document and the documents embedded in
// it may still decompress, against Limits.MaxDecodedBytes.
//
// The limit on decompressed bytes is what stops a decompression bomb. An
// extractor counts against it as it reads, and each document has its own
// count. But a document can embed thousands of documents, each of them a
// bomb that stays just inside the limit: without more, the limit would
// hold for each and the whole would cost thousands of times as much.
// DecodeBudget makes the limit the document's and its embedded documents'
// together, as TextBudget does for text, and in the same way.
//
// An extractor gets the budget of the document it is reading from
// WithDecodeBudget, and before it hands an embedded document to
// Options.ExtractChild sees to it that the budget says what the document
// has left (Take). It passes the context WithDecodeBudget returned. The
// child's extractor, of whatever library, gets from WithDecodeBudget a
// budget no larger than what its parent has left, reads within it, and
// on Close takes what it used from its parent; the parent then goes on
// with what is left. Embedded documents are extracted one after another.
//
// How an extractor counts while it reads is its own affair: it may take
// from the budget as it goes, or keep a count of its own and settle with
// the budget (Settle) before each embedded document and at the end.
//
// A nil *DecodeBudget has no limit. A DecodeBudget is safe for
// concurrent use.
type DecodeBudget struct {
	b TextBudget // the same counting
}

type decodeBudgetKey struct{}

// WithDecodeBudget returns the budget for one reading of a document, and
// ctx carrying it. The budget is limits.MaxDecodedBytes (def if zero,
// none if negative; the default is the extractor's own), or what the
// document this one is embedded in has left, if ctx carries that and it
// is less.
func WithDecodeBudget(ctx context.Context, limits Limits, def int64) (context.Context, *DecodeBudget) {
	d := &DecodeBudget{}
	d.b.limit = ResolveLimit(limits.MaxDecodedBytes, def)
	if parent, ok := ctx.Value(decodeBudgetKey{}).(*DecodeBudget); ok && parent != nil {
		d.b.parent = &parent.b
		d.b.limit = min(d.b.limit, max(parent.Left(), 0))
	}
	d.b.left.Store(d.b.limit)
	return context.WithValue(ctx, decodeBudgetKey{}, d), d
}

// DecodeBudgetFrom returns the budget ctx carries, or nil.
func DecodeBudgetFrom(ctx context.Context) *DecodeBudget {
	d, _ := ctx.Value(decodeBudgetKey{}).(*DecodeBudget)
	return d
}

// Take takes n decompressed bytes from the budget and reports whether
// they were there to take.
func (d *DecodeBudget) Take(n int64) bool {
	return d == nil || n <= 0 || d.b.left.Add(-n) >= 0
}

// Settle takes from the budget what is needed for Left to be at most left,
// and reports whether Left is then not negative. It is for an extractor
// that keeps a count of its own while it reads (left is what that count
// says the document has left) and settles with the budget before it hands
// an embedded document to Options.ExtractChild, and at the end. It never
// gives back: if the budget already has less than left, it is unchanged.
func (d *DecodeBudget) Settle(left int64) bool {
	if d == nil {
		return true
	}
	for {
		l := d.b.left.Load()
		if l <= left {
			return l >= 0
		}
		if d.b.left.CompareAndSwap(l, left) {
			return left >= 0
		}
	}
}

// Left returns the bytes not yet taken; a negative number once more was
// asked for than there was.
func (d *DecodeBudget) Left() int64 {
	if d == nil {
		return 1<<63 - 1
	}
	return d.b.left.Load()
}

// Close ends the reading: what the document took is taken from the
// budget of the document it is embedded in, if there is one. Later calls
// do nothing.
func (d *DecodeBudget) Close() {
	if d != nil {
		d.b.Close()
	}
}
