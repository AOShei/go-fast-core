package charset

import "strings"

// Symbol is what ForCharset returns for a font with the Symbol character
// set: its bytes are the font's glyph numbers, not characters of any code
// page (see go-fast-core/symbol).
const Symbol = -2

// CodePage returns the encoding of Windows code page cp: 874, 932, 936,
// 949, 950, 1250 to 1258, 10000 (Mac Roman), 1200 and 1201 (UTF-16),
// 20866 and 21866 (KOI8), 28591 to 28605 (ISO-8859; 28591 as
// windows-1252, as for the label), 50220 to 50222 (ISO-2022-JP), 51932
// (EUC-JP), 54936 (GB18030) and 65001 (UTF-8).
func CodePage(cp int) (Encoding, bool) {
	e, ok := codePages[cp]
	return e, ok
}

var codePages = map[int]Encoding{
	874: Windows874, 932: ShiftJIS, 936: GBK, 949: EUCKR, 950: Big5,
	1250: Windows1250, 1251: Windows1251, 1252: Windows1252, 1253: Windows1253,
	1254: Windows1254, 1255: Windows1255, 1256: Windows1256, 1257: Windows1257,
	1258: Windows1258, 10000: Macintosh, 10007: MacCyrillic, 866: IBM866,
	1200: UTF16LE, 1201: UTF16BE, 20866: KOI8R, 21866: KOI8U,
	28591: Windows1252, 28592: ISO8859_2, 28593: ISO8859_3, 28594: ISO8859_4,
	28595: ISO8859_5, 28596: ISO8859_6, 28597: ISO8859_7, 28598: ISO8859_8,
	28599: Windows1254, 28600: ISO8859_10, 28603: ISO8859_13, 28604: ISO8859_14,
	28605: ISO8859_15, 50220: ISO2022JP, 50221: ISO2022JP, 50222: ISO2022JP,
	51932: EUCJP, 54936: GB18030, 65001: UTF8,
}

// CodePage returns the Windows code page of e, or 0 for one that has none
// (ISO-8859-16).
func (e Encoding) CodePage() int {
	if cp := e.codePage(); cp != 0 {
		return cp
	}
	for cp, c := range codePages {
		if c == e && cp != 28591 && cp != 28599 && cp != 50221 && cp != 50222 {
			return cp
		}
	}
	return 0
}

// ForCharset returns the code page of a font's character set (a Windows
// LOGFONT charset, RTF's \fcharsetN, the charset of a Word font table),
// Symbol for the Symbol character set, or 0 when the character set says
// nothing more than the document's code page. 437 (OEM United States),
// 850 (OEM Latin 1) and 1361 (Johab) are not decoded here.
func ForCharset(charset int) int {
	switch charset {
	case 0:
		return 1252
	case 2:
		return Symbol
	case 77:
		return 10000
	case 128:
		return 932
	case 129:
		return 949
	case 130:
		return 1361
	case 134:
		return 936
	case 136:
		return 950
	case 161:
		return 1253
	case 162:
		return 1254
	case 163:
		return 1258
	case 177:
		return 1255
	case 178:
		return 1256
	case 186:
		return 1257
	case 204:
		return 1251
	case 222:
		return 874
	case 238:
		return 1250
	case 254:
		return 437
	case 255:
		return 850
	}
	return 0
}

// A PageDecoder decodes bytes one at a time in Windows code pages that may
// change from one byte to the next, as RTF's \'hh escapes and font
// changes do. A character half read when the code page changes is not a
// character. Bytes above 0x7F in a code page not decoded here are not
// characters either, but for the Symbol character set's bullet (0xB7).
type PageDecoder struct {
	d      Decoder
	cp     int
	known  bool
	set    bool
	single *[128]uint16 // the code page's table, if it is a single-byte one
}

// Byte decodes byte b of code page cp: see Decoder.Byte.
func (p *PageDecoder) Byte(cp int, b byte, emit func(r rune, ok bool)) {
	if p.set && cp == p.cp && p.known && p.d.n == 0 {
		// The usual cases, in the code page of the byte before: as
		// Decoder.Byte has them, without the call.
		if b < 0x80 && byteWise(p.d.enc) {
			emit(rune(b), true)
			return
		}
		if t := p.single; t != nil {
			switch r := t[b-0x80]; {
			case r != 0:
				emit(rune(r), true)
			case b < 0xA0:
				emit(rune(b), true)
			default:
				emit(0xFFFD, false)
			}
			return
		}
	}
	if !p.set || cp != p.cp {
		p.Flush(emit)
		enc, ok := CodePage(cp)
		p.cp, p.known, p.set = cp, ok, true
		p.d.Reset(enc)
		p.single = nil
		if ok {
			if enc == Windows1252 {
				p.single = &cp1252 // its undefined bytes are the C1 controls win1252 gives them
			} else {
				p.single = enc.single()
			}
		}
	}
	switch {
	case p.known:
		p.d.Byte(b, emit)
	case b < 0x80:
		emit(rune(b), true)
	case cp == Symbol && b == 0xB7:
		emit(0x2022, true) // the bullet of the Symbol font
	default:
		emit(0xFFFD, false)
	}
}

// Flush ends a run of bytes: a character half read is not a character.
func (p *PageDecoder) Flush(emit func(r rune, ok bool)) {
	if p.known {
		p.d.Flush(emit)
	}
}

// DecodeCodePage decodes b, in Windows code page cp, to a string, as a
// PageDecoder does, and returns how many characters were not decoded and
// became U+FFFD.
func DecodeCodePage(cp int, b []byte) (s string, undecoded int) {
	ascii := true
	for _, c := range b {
		if c >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		if enc, ok := CodePage(cp); !ok || byteWise(enc) {
			return string(b), 0
		}
	}
	var sb strings.Builder
	sb.Grow(len(b) + len(b)/2)
	var p PageDecoder
	emit := func(r rune, ok bool) {
		if !ok {
			undecoded++
		}
		sb.WriteRune(r)
	}
	for _, c := range b {
		p.Byte(cp, c, emit)
	}
	p.Flush(emit)
	return sb.String(), undecoded
}
