package attach

import (
	"bytes"
	"encoding/binary"
	"unicode/utf16"
	"unicode/utf8"
)

var (
	bomUTF8    = []byte{0xef, 0xbb, 0xbf}
	bomUTF16LE = []byte{0xff, 0xfe}
	bomUTF16BE = []byte{0xfe, 0xff}
)

// decodeText returns data as UTF-8 without a BOM when it is text: valid UTF-8
// (with or without a BOM) or UTF-16 with a BOM, without NUL and with few
// control characters. truncated: data is only the head of the file, so it may
// end in the middle of a character (which is then dropped).
func decodeText(data []byte, truncated bool) ([]byte, bool) {
	var text []byte
	switch {
	case bytes.HasPrefix(data, bomUTF16LE), bytes.HasPrefix(data, bomUTF16BE):
		var ok bool
		if text, ok = decodeUTF16(data, truncated); !ok {
			return nil, false
		}
	default:
		text = bytes.TrimPrefix(data, bomUTF8)
		if truncated {
			text = trimPartialRune(text)
		}
		if !utf8.Valid(text) {
			return nil, false
		}
	}
	if !fewControls(text) {
		return nil, false
	}
	return text, true
}

// trimPartialRune drops an incomplete UTF-8 sequence at the end of b.
func trimPartialRune(b []byte) []byte {
	for i := 1; i <= utf8.UTFMax-1 && i <= len(b); i++ {
		c := b[len(b)-i]
		if c < 0x80 {
			return b // ASCII: nothing partial after it
		}
		if utf8.RuneStart(c) {
			if !utf8.FullRune(b[len(b)-i:]) {
				return b[:len(b)-i]
			}
			return b
		}
	}
	return b
}

// decodeUTF16 converts UTF-16 with a BOM to UTF-8; false when it has a NUL
// code point or an unpaired surrogate (or an odd length, unless truncated).
func decodeUTF16(data []byte, truncated bool) ([]byte, bool) {
	var order binary.ByteOrder = binary.LittleEndian
	if data[0] == 0xfe {
		order = binary.BigEndian
	}
	data = data[2:]
	if len(data)%2 != 0 {
		if !truncated {
			return nil, false
		}
		data = data[:len(data)-1]
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = order.Uint16(data[2*i:])
	}
	if truncated && len(units) > 0 && utf16.IsSurrogate(rune(units[len(units)-1])) &&
		units[len(units)-1] < 0xdc00 {
		units = units[:len(units)-1] // a high surrogate cut from its pair
	}
	out := make([]byte, 0, len(units)*3/2)
	for i := 0; i < len(units); i++ {
		u := units[i]
		var r rune
		switch {
		case u == 0:
			return nil, false
		case u >= 0xd800 && u < 0xdc00:
			if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
				return nil, false
			}
			r = utf16.DecodeRune(rune(u), rune(units[i+1]))
			i++
		case u >= 0xdc00 && u <= 0xdfff:
			return nil, false
		default:
			r = rune(u)
		}
		out = utf8.AppendRune(out, r)
	}
	return out, true
}

// fewControls: no NUL, and at most 2% of the bytes are C0 controls other
// than \t \n \r \f \v \b and ESC, or DEL.
func fewControls(b []byte) bool {
	n := 0
	for _, c := range b {
		switch {
		case c == 0:
			return false
		case c == '\t', c == '\n', c == '\r', c == '\f', c == '\v', c == '\b', c == 0x1b:
		case c < 0x20, c == 0x7f:
			n++
		}
	}
	return n*50 <= len(b)
}
