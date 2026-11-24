package ziprecover

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// archive writes a zip archive.
func archive(t testing.TB, build func(w *zip.Writer)) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	build(w)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// directoryAt is where the archive's directory begins.
func directoryAt(t testing.TB, data []byte) int {
	t.Helper()
	end := bytes.LastIndex(data, []byte("PK\x05\x06"))
	if end < 0 {
		t.Fatal("no end record")
	}
	return int(binary.LittleEndian.Uint32(data[end+16:]))
}

// member is what reading a member gave.
type member struct {
	data string
	err  error
}

// String compares by the error's text, not its address.
func (m member) String() string { return fmt.Sprintf("{%q %v}", m.data, m.err) }

// readAll reads every member of zr to its end, or to where it breaks off.
func readAll(t testing.TB, zr *zip.Reader) map[string]member {
	t.Helper()
	out := map[string]member{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			out[f.Name] = member{err: err}
			continue
		}
		data, err := io.ReadAll(io.LimitReader(rc, 4<<20))
		rc.Close()
		out[f.Name] = member{string(data), err}
	}
	return out
}

// raw writes a member with its sizes in its header.
func raw(t testing.TB, w *zip.Writer, name string, method uint16, body []byte) {
	t.Helper()
	h := &zip.FileHeader{Name: name, Method: method, UncompressedSize64: uint64(len(body)), CRC32: crc32.ChecksumIEEE(body)}
	var c bytes.Buffer
	if method == zip.Deflate {
		fw, _ := flate.NewWriter(&c, flate.DefaultCompression)
		fw.Write(body)
		fw.Close()
	} else {
		c.Write(body)
	}
	h.CompressedSize64 = uint64(c.Len())
	f, err := w.CreateRaw(h)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(c.Bytes())
}

// streamed writes a member whose sizes follow its data.
func streamed(t testing.TB, w *zip.Writer, name string, method uint16, body []byte) {
	t.Helper()
	f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
	if err != nil {
		t.Fatal(err)
	}
	f.Write(body)
}

var long = strings.Repeat("some text that compresses, ", 400)

// mixed is an archive of members in each form: sizes in the header or
// after the data, deflated or stored, empty, and a zip stored in it.
func mixed(t testing.TB) ([]byte, []string, map[string]string) {
	inner := archive(t, func(w *zip.Writer) {
		raw(t, w, "inner.txt", zip.Store, []byte("an archive inside"))
	})
	rng := rand.New(rand.NewSource(1))
	noise := make([]byte, 5000)
	rng.Read(noise)
	contents := map[string]string{
		"a.xml":      "<a>" + long + "</a>",
		"b.txt":      long[:3000],
		"empty.bin":  "",
		"noise.bin":  string(noise),
		"stored.bin": "stored as it is",
		"inner.zip":  string(inner),
		"c.xml":      "<c>" + long[:5000] + "</c>",
	}
	order := []string{"a.xml", "b.txt", "empty.bin", "noise.bin", "stored.bin", "inner.zip", "c.xml"}
	data := archive(t, func(w *zip.Writer) {
		streamed(t, w, "a.xml", zip.Deflate, []byte(contents["a.xml"]))
		raw(t, w, "b.txt", zip.Deflate, []byte(contents["b.txt"]))
		streamed(t, w, "empty.bin", zip.Deflate, nil)
		streamed(t, w, "noise.bin", zip.Deflate, noise)
		raw(t, w, "stored.bin", zip.Store, []byte(contents["stored.bin"]))
		raw(t, w, "inner.zip", zip.Store, inner)
		streamed(t, w, "c.xml", zip.Deflate, []byte(contents["c.xml"]))
	})
	return data, order, contents
}

// bothWays runs Open with the file read into memory, as inflate reads it,
// and from the file, as compress/flate reads it, checks that they agree,
// and returns what was found.
func bothWays(t testing.TB, data []byte, lim Limits) (*Recovery, map[string]member, []string, error) {
	t.Helper()
	open := func(held int64) (*Recovery, map[string]member, []string, error) {
		defer func(was int64) { maxHeld = was }(maxHeld)
		maxHeld = held
		zr, rec, err := Open(bytes.NewReader(data), int64(len(data)), lim)
		if err != nil {
			if zr != nil || rec != nil {
				t.Fatal("a result with an error")
			}
			return nil, nil, nil, err
		}
		var names []string
		for _, f := range zr.File {
			names = append(names, fmt.Sprintf("%s %d %d %x", f.Name, f.CompressedSize64, f.UncompressedSize64, f.CRC32))
		}
		return rec, readAll(t, zr), names, nil
	}
	rec, got, names, err := open(1 << 40)
	rec2, got2, names2, err2 := open(0)
	if fmt.Sprint(err) != fmt.Sprint(err2) || fmt.Sprint(rec) != fmt.Sprint(rec2) || fmt.Sprint(names) != fmt.Sprint(names2) || fmt.Sprint(got) != fmt.Sprint(got2) {
		t.Fatalf("in memory: %v %+v %q\nfrom the file: %v %+v %q", err, rec, names, err2, rec2, names2)
	}
	return rec, got, names, err
}

func TestOpenSound(t *testing.T) {
	data, order, contents := mixed(t)
	zr, rec, err := Open(bytes.NewReader(data), int64(len(data)), Limits{})
	if err != nil || rec != nil || len(zr.File) != len(order) {
		t.Fatalf("%v, %+v", err, rec)
	}
	for name, m := range readAll(t, zr) {
		if m.err != nil || m.data != contents[name] {
			t.Errorf("%s: %d bytes, %v", name, len(m.data), m.err)
		}
	}
	if _, _, err := Open(bytes.NewReader(data), int64(len(data)), Limits{MaxMembers: 3}); !errors.Is(err, ErrTooManyMembers) {
		t.Errorf("more members than allowed: %v", err)
	}
}

// The members of an archive are found by their own headers in each of
// the forms a member's length is written in: in its header, or after its
// data, with a signature or without.
func TestRecoverFindsMembers(t *testing.T) {
	data, order, contents := mixed(t)
	cut := data[:directoryAt(t, data)]
	rec, got, _, err := bothWays(t, cut, Limits{})
	if err != nil || rec.Members != len(order) || rec.Cut != "" {
		t.Fatalf("%+v, %v", rec, err)
	}
	for _, name := range order {
		if m := got[name]; m.err != nil || m.data != contents[name] {
			t.Errorf("%s: %d bytes of %d, %v", name, len(m.data), len(contents[name]), m.err)
		}
	}
	// The zip stored inside is a member, not members.
	if _, ok := got["inner.txt"]; ok {
		t.Error("a header inside a member was taken for a member")
	}
	// Without the signature in front of each descriptor.
	plain := withoutDescriptorSignatures(t, data)
	if rec2, got2, _, err := bothWays(t, plain, Limits{}); err != nil || rec2.Members != len(order) || fmt.Sprint(got2) != fmt.Sprint(got) {
		t.Errorf("descriptors without signatures: %+v, %v", rec2, err)
	}
	// A member stored as it is, with its length after it, cannot be told
	// from what follows: the members before it are found.
	stored := archive(t, func(w *zip.Writer) {
		streamed(t, w, "a.xml", zip.Deflate, []byte(long))
		streamed(t, w, "s.bin", zip.Store, []byte("stored"))
		streamed(t, w, "c.xml", zip.Deflate, []byte(long))
	})
	if rec, _, _, err := bothWays(t, stored[:directoryAt(t, stored)], Limits{}); err != nil || rec.Members != 1 || rec.Cut != "" {
		t.Errorf("a stored member with its length after it: %+v, %v", rec, err)
	}
}

// withoutDescriptorSignatures returns the archive data without its
// directory, and without the signature in front of each data descriptor.
func withoutDescriptorSignatures(t testing.TB, data []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var at []int
	for _, f := range zr.File {
		if f.Flags&flagDescriptor != 0 {
			off, _ := f.DataOffset()
			at = append(at, int(off)+int(f.CompressedSize64))
		}
	}
	if len(at) == 0 {
		t.Fatal("no descriptors")
	}
	out := data[:directoryAt(t, data)]
	for i := len(at) - 1; i >= 0; i-- {
		if string(out[at[i]:at[i]+4]) != "PK\x07\x08" {
			t.Fatalf("no descriptor signature at %d", at[i])
		}
		out = append(out[:at[i]:at[i]], out[at[i]+4:]...)
	}
	return out
}

// Cut anywhere before its directory, an archive gives every member before
// the cut whole, the member it is cut in up to the cut with an error, and
// says which.
func TestCutAnywhere(t *testing.T) {
	data, order, contents := mixed(t)
	at := directoryAt(t, data)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	starts := map[string]int64{}
	for _, f := range zr.File {
		off, _ := f.DataOffset()
		starts[f.Name] = off
	}
	refused := 0
	for cut := 0; cut <= at; cut++ {
		rec, got, _, err := bothWays(t, data[:cut], Limits{})
		if err != nil {
			if !errors.Is(err, ErrNoMembers) && !errors.Is(err, ErrNotZip) {
				t.Fatalf("cut at %d: %v", cut, err)
			}
			refused++
			continue
		}
		if rec.Members != len(got) {
			t.Fatalf("cut at %d: %d members reported, %d read", cut, rec.Members, len(got))
		}
		for i, name := range order[:rec.Members] {
			m := got[name]
			whole := i < rec.Members-1 || rec.Cut == ""
			switch {
			case whole && (m.err != nil || m.data != contents[name]):
				t.Fatalf("cut at %d: %s, before the cut, reads as %d bytes of %d, %v", cut, name, len(m.data), len(contents[name]), m.err)
			case !whole && (rec.Cut != name || m.err == nil || !strings.HasPrefix(contents[name], m.data)):
				t.Fatalf("cut at %d: %s, cut, reads as %d bytes, %v (cut %q)", cut, name, len(m.data), m.err, rec.Cut)
			}
		}
		// A cut after a member's data and descriptor loses only what
		// follows.
		if rec.Cut == "" && rec.Members < len(order) && int64(cut) > starts[order[rec.Members]] {
			t.Fatalf("cut at %d, inside %s: not reported", cut, order[rec.Members])
		}
	}
	// Only a cut before the first member's data is refused.
	if refused != 30+len("a.xml")+1 {
		t.Errorf("%d cuts refused", refused)
	}
}

// Members whose length follows them are decompressed to find where they
// end: a bomb among them meets the budget, in time.
func TestRecoveryIsBounded(t *testing.T) {
	bomb := archive(t, func(w *zip.Writer) {
		streamed(t, w, "a.txt", zip.Deflate, []byte("x"))
		streamed(t, w, "zeros.bin", zip.Deflate, make([]byte, 64<<20))
	})
	cut := bomb[:directoryAt(t, bomb)]
	for _, held := range []int64{1 << 40, 0} {
		maxHeld = held
		start := time.Now()
		if _, _, err := Open(bytes.NewReader(cut), int64(len(cut)), Limits{MaxDecodedBytes: 1 << 20}); !errors.Is(err, ErrBudget) {
			t.Errorf("over the budget: %v", err)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%v to meet the budget", d)
		}
		if _, rec, err := Open(bytes.NewReader(cut), int64(len(cut)), Limits{}); err != nil || rec.Members != 2 {
			t.Errorf("without a budget: %+v, %v", rec, err)
		}
	}
	maxHeld = 32 << 20
	// The budget is for all the members together.
	two := archive(t, func(w *zip.Writer) {
		streamed(t, w, "a.bin", zip.Deflate, make([]byte, 600<<10))
		streamed(t, w, "b.bin", zip.Deflate, make([]byte, 600<<10))
	})
	cut = two[:directoryAt(t, two)]
	if _, _, err := Open(bytes.NewReader(cut), int64(len(cut)), Limits{MaxDecodedBytes: 1 << 20}); !errors.Is(err, ErrBudget) {
		t.Errorf("two members over the budget together: %v", err)
	}
	if _, rec, err := Open(bytes.NewReader(cut), int64(len(cut)), Limits{MaxDecodedBytes: 1200 << 10}); err != nil || rec.Members != 2 {
		t.Errorf("two members within the budget: %+v, %v", rec, err)
	}
	many := archive(t, func(w *zip.Writer) {
		for i := range 50 {
			raw(t, w, fmt.Sprintf("m%02d", i), zip.Store, []byte{1})
		}
	})
	cut = many[:directoryAt(t, many)]
	if _, _, err := Open(bytes.NewReader(cut), int64(len(cut)), Limits{MaxMembers: 20}); !errors.Is(err, ErrTooManyMembers) {
		t.Errorf("more members than allowed: %v", err)
	}
	// Bytes that only begin like an archive, or not even that.
	for _, junk := range [][]byte{
		nil,
		[]byte("PK"),
		[]byte("PK\x03\x04"),
		append([]byte("PK\x03\x04"), make([]byte, 26)...),
		append([]byte("PK\x03\x04"), bytes.Repeat([]byte{0xff}, 60)...),
		[]byte("not an archive at all, but long enough to be read"),
	} {
		if _, _, err := Open(bytes.NewReader(junk), int64(len(junk)), Limits{}); !errors.Is(err, ErrNoMembers) && !errors.Is(err, ErrNotZip) {
			t.Errorf("%q: %v", junk, err)
		}
	}
}

func TestMessage(t *testing.T) {
	for _, c := range []struct {
		rec   Recovery
		label func(string) string
		want  string
	}{
		{Recovery{Members: 3}, nil, "the archive's directory cannot be read: the file is cut short or damaged. 3 members were found by their own headers; any that followed are lost"},
		{Recovery{Members: 2, Cut: "word/document.xml"}, nil, "the archive's directory cannot be read: the file is cut short or damaged. 2 members were found by their own headers; the last, word/document.xml, breaks off"},
		{Recovery{Members: 1, Cut: "a\x00b\x1b[31m\xff.xml"}, nil, "the archive's directory cannot be read: the file is cut short or damaged. 1 members were found by their own headers; the last, ab[31m.xml, breaks off"},
		{Recovery{Members: 1, Cut: "x"}, strings.ToUpper, "the archive's directory cannot be read: the file is cut short or damaged. 1 members were found by their own headers; the last, X, breaks off"},
	} {
		if got := c.rec.Message(c.label); got != c.want {
			t.Errorf("%+v:\n got %q\nwant %q", c.rec, got, c.want)
		}
	}
	if got := defaultLabel(strings.Repeat("é", 300)); len(got) > maxLabel+len("…") || !strings.HasSuffix(got, "…") {
		t.Errorf("a long name: %d bytes", len(got))
	}
}

// FuzzRecover gives recovery arbitrary bytes. Whatever they are, it
// returns in time, stays within its bounds, what it returns can be read,
// and it finds the same in memory as from the file.
func FuzzRecover(f *testing.F) {
	data, _, _ := mixed(f)
	cut := data[:directoryAt(f, data)]
	f.Add(cut)
	f.Add(cut[:len(cut)/2])
	f.Add(cut[:40])
	f.Add([]byte("PK\x03\x04"))
	small := archive(f, func(w *zip.Writer) {
		streamed(f, w, "a", zip.Deflate, []byte(long[:300]))
		raw(f, w, "b", zip.Deflate, []byte(long[:200]))
	})
	f.Add(small[:directoryAt(f, small)])
	f.Fuzz(func(t *testing.T, data []byte) {
		lim := Limits{MaxDecodedBytes: 1 << 20, MaxMembers: 64}
		rec, _, names, err := bothWays(t, data, lim)
		if err != nil {
			return
		}
		if rec != nil && (rec.Members != len(names) || rec.Members == 0 || rec.Members > 64) {
			t.Fatalf("%d members reported, %d in the directory", rec.Members, len(names))
		}
		zr, _, _ := Open(bytes.NewReader(data), int64(len(data)), lim)
		for _, f := range zr.File {
			if off, err := f.DataOffset(); err != nil || off > int64(len(data)) || off+int64(f.CompressedSize64) > int64(len(data)) {
				if rec != nil {
					t.Fatalf("member %q: data at %d, %d bytes, in a file of %d (%v)", f.Name, off, f.CompressedSize64, len(data), err)
				}
			}
		}
	})
}

// A file cut short after a zip stored in it has that zip's directory at
// its end. archive/zip takes it for the file's own; Open does not, and
// recovers the file's members.
func TestStoredArchiveBeforeTheCut(t *testing.T) {
	data, order, _ := mixed(t)
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	var end int64
	for _, f := range zr.File {
		if f.Name == "inner.zip" {
			off, _ := f.DataOffset()
			end = off + int64(f.CompressedSize64)
		}
	}
	cut := data[:end]
	if inner, err := zip.NewReader(bytes.NewReader(cut), int64(len(cut))); err != nil || len(inner.File) != 1 || inner.File[0].Name != "inner.txt" {
		t.Fatalf("archive/zip does not read the stored zip's directory: %v", err)
	}
	rec, got, _, err := bothWays(t, cut, Limits{})
	if err != nil || rec == nil || rec.Members != 6 || rec.Cut != "" || len(got) != 6 {
		t.Fatalf("%+v, %v, %d members", rec, err, len(got))
	}
	for _, name := range order[:6] {
		if m := got[name]; m.err != nil {
			t.Errorf("%s: %v", name, m.err)
		}
	}
	// Data in front of an archive, as a self-extracting program has, is
	// still allowed.
	front := append([]byte("MZ a program first "), data...)
	if zr, rec, err := Open(bytes.NewReader(front), int64(len(front)), Limits{}); err != nil || rec != nil || len(zr.File) != len(order) {
		t.Errorf("an archive after other data: %+v, %v", rec, err)
	}
}
