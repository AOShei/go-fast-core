package inflate

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"testing"
)

// readZipMember reads the member's content through zr, as far as it goes,
// and the error that ended it.
func readZipMember(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("no member %s", name)
}

// TestDecompressorLikeCompressFlate: a member reads through Decompressor
// as through compress/flate, whole and damaged, but for archive/zip's
// one difference (see Decompressor): where they differ, both fail and the
// shorter is the start of the longer.
func TestDecompressorLikeCompressFlate(t *testing.T) {
	var text bytes.Buffer
	for i := range 4000 {
		fmt.Fprintf(&text, "<p class=\"c%d\">Paragraph %d of the member, long enough to need more than one piece.</p>\n", i%40, i)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("member.xml")
	w.Write(text.Bytes())
	w, _ = zw.Create("small.xml")
	w.Write([]byte("<small/>"))
	zw.Close()
	pkg := buf.Bytes()
	zr, _ := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	off, _ := zr.File[0].DataOffset()
	end := off + int64(zr.File[0].CompressedSize64)

	more, less := 0, 0
	check := func(data []byte, what string) {
		std, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		ours, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		ours.RegisterDecompressor(zip.Deflate, Decompressor)
		for _, name := range []string{"member.xml", "small.xml"} {
			want, wantErr := readZipMember(std, name)
			got, gotErr := readZipMember(ours, name)
			if bytes.Equal(got, want) && (gotErr == nil) == (wantErr == nil) {
				continue
			}
			short, long := got, want
			if len(short) > len(long) {
				short, long = long, short
			}
			if gotErr == nil || wantErr == nil || !bytes.HasPrefix(long, short) || len(want)-len(got) > maxRead {
				t.Fatalf("%s, %s: %d bytes (error %v), compress/flate %d (error %v)", what, name, len(got), gotErr, len(want), wantErr)
			}
			if len(got) > len(want) {
				more++
			} else {
				less++
			}
		}
	}
	check(pkg, "whole")
	for at := off; at < end; at += 1 + (end-off)/400 {
		bad := append([]byte(nil), pkg...)
		bad[at] ^= 0x5a
		check(bad, fmt.Sprintf("byte %d damaged", at))
	}
	for cut := off + 1; cut < end; cut += 1 + (end-off)/50 {
		short := append([]byte(nil), pkg...)
		copy(short[cut:end], make([]byte, end-cut))
		check(short, fmt.Sprintf("zeros from %d", cut))
	}
	t.Logf("damaged members that read differently: %d with more of their output, %d with less", more, less)
}

// Closing gives the Reader and the buffer back; a member closed reads no
// more, and closing twice is harmless.
func TestDecompressorClose(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("a.txt")
	w.Write(bytes.Repeat([]byte("abc "), 10000))
	zw.Close()
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	zr.RegisterDecompressor(zip.Deflate, Decompressor)
	for range 50 {
		rc, err := zr.File[0].Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		if err != nil || len(b) != 40000 {
			t.Fatalf("%d bytes, %v", len(b), err)
		}
		rc.Close()
		rc.Close()
		if n, err := rc.Read(make([]byte, 10)); n != 0 || err == nil {
			t.Fatalf("a read after Close: %d, %v", n, err)
		}
	}
}
