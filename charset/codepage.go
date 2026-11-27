package charset

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
