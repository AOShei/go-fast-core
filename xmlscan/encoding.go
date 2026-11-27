package xmlscan

import (
	"bytes"
	"fmt"
	"io"

	"github.com/AOShei/go-fast-core/charset"
)

// sniff looks at the first bytes of the input, skips a byte order mark and
// arranges for input that is not UTF-8 to be converted on the way in, from
// UTF-16 (by its mark, or by a '<' with a zero byte beside it) or from the
// encoding an XML declaration names, decoded by go-fast-core/charset. An
// encoding it does not decode is ErrEncoding.
func (s *Scanner) sniff() {
	for s.end-s.pos < 5 && s.more() { // enough to recognize "<?xml"
	}
	if s.err != nil {
		return
	}
	b := s.buf[s.pos:s.end]
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		s.pos += 3
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		s.pos += 2
		s.convert(charset.UTF16LE)
		return
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		s.pos += 2
		s.convert(charset.UTF16BE)
		return
	case len(b) >= 4 && b[0] == '<' && b[1] == 0 && b[3] == 0:
		s.convert(charset.UTF16LE)
		return
	case len(b) >= 4 && b[0] == 0 && b[1] == '<' && b[2] == 0:
		s.convert(charset.UTF16BE)
		return
	}

	// An XML declaration may name another encoding. It is "<?xml"
	// followed by white space; "<?xml-stylesheet" and the like are not it.
	for s.end-s.pos < 6 && s.more() {
	}
	if b = s.buf[s.pos:s.end]; len(b) < 6 || !bytes.HasPrefix(b, []byte("<?xml")) || !isSpace(b[5]) {
		return
	}
	for bytes.Index(s.buf[s.pos:s.end], []byte("?>")) < 0 && s.end-s.pos < 1024 && s.more() {
	}
	if s.err != nil {
		return
	}
	decl := s.buf[s.pos:s.end]
	if i := bytes.Index(decl, []byte("?>")); i >= 0 {
		decl = decl[:i]
	} else {
		return
	}
	name := declaredEncoding(decl)
	if len(name) == 0 {
		return
	}
	enc, ok := charset.Lookup(string(name))
	switch {
	case !ok:
		s.fail(fmt.Errorf("%w: %q", ErrEncoding, name))
	case enc == charset.UTF8, enc == charset.UTF16LE, enc == charset.UTF16BE:
		// UTF-16 named in the declaration of input that reads as ASCII
		// is a mislabel: the bytes are what count.
	case enc != charset.ISO2022JP && s.eof && !nonASCII(s.buf[s.pos:s.end]):
		// All of it is in the window and is ASCII, which every other
		// encoding a declaration can name (one that reads as ASCII)
		// leaves as it is.
	default:
		s.convert(enc)
	}
}

func nonASCII(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return true
		}
	}
	return false
}

// declaredEncoding returns the value of the encoding pseudo-attribute of an
// XML declaration, or nil. It does not allocate: every part has one.
func declaredEncoding(decl []byte) []byte {
	i := bytes.Index(decl, []byte("encoding"))
	if i < 1 || !isSpace(decl[i-1]) {
		return nil
	}
	rest := bytes.TrimLeft(decl[i+len("encoding"):], " \t\r\n")
	if len(rest) == 0 || rest[0] != '=' {
		return nil
	}
	rest = bytes.TrimLeft(rest[1:], " \t\r\n")
	if len(rest) == 0 || (rest[0] != '"' && rest[0] != '\'') {
		return nil
	}
	end := bytes.IndexByte(rest[1:], rest[0])
	if end < 0 {
		return nil
	}
	return rest[1 : 1+end]
}

// convert puts a decoding reader between the input and the window. What
// is already in the window is fed through it first. Offsets are then in
// the decoded text.
func (s *Scanner) convert(enc charset.Encoding) {
	pending := append([]byte(nil), s.buf[s.pos:s.end]...)
	var src io.Reader = bytes.NewReader(pending)
	if s.r != nil && !s.eof {
		src = io.MultiReader(src, s.r)
	}
	if !s.own {
		s.buf, s.own = getWindow(DefaultBufferSize), true
	}
	s.r, s.pos, s.end, s.base, s.eof = charset.NewReader(src, enc), 0, 0, s.baseOffset, false
	s.converted = true
}

// UTF8 returns the input as the scanner reads it: converted to UTF-8 from
// the encoding its byte order mark or declaration names, without the
// mark. converted reports that it was not UTF-8. The error is
// ErrEncoding, for an encoding the scanner does not read, or the
// reader's.
func UTF8(r io.Reader) (utf8 io.Reader, converted bool, err error) {
	s := &Scanner{}
	s.reset(nil)
	s.r, s.own, s.buf = r, true, make([]byte, 4096)
	s.sniff()
	if s.err != nil {
		return nil, false, s.err
	}
	pending := append([]byte(nil), s.buf[s.pos:s.end]...)
	utf8 = bytes.NewReader(pending)
	if s.r != nil && !s.eof {
		utf8 = io.MultiReader(utf8, s.r)
	}
	return utf8, s.converted, nil
}
