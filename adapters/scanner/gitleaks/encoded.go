package gitleaks

// minEncodedRun is the shortest run gitleaks' codec treats as a base64
// candidate (`[\w\/+-]{16,}` in detect/codec/encodings.go); its hex
// pattern (`[0-9A-Fa-f]{32,}`) is a longer run of a subset of the same
// bytes, so a text with no such run holds neither.
const minEncodedRun = 16

// mayHoldEncoding reports whether text could hold a segment gitleaks'
// codec decodes (3.6: percent, unicode, hex, base64). It is a cheap,
// one-pass superset of the codec's own regex (detect/codec/encodings.go,
// v8.30.1):
//
//	percent  %[0-9A-Fa-f]{2}…        -> a '%' followed by two hex digits
//	unicode  U\+[hex]{4} | \\{1,2}u… -> "U+", or a '\' followed by u or U
//	hex      [0-9A-Fa-f]{32,}        -> covered by the base64 run below
//	base64   [\w\/+-]{16,}={0,2}     -> a run of 16 bytes of [A-Za-z0-9_/+-]
//
// Invariant: false means codec.Decoder.Decode finds no segment in text,
// so skipping the decode passes changes no finding (SECRETS 3.8: the
// decode passes were about half of a no-hit scan). A true answer only
// means "run the codec": it may still find nothing. A gitleaks upgrade
// that widens the codec's patterns must widen this check too;
// TestMayHoldEncodingIsASuperset fails when it does not.
func mayHoldEncoding(text string) bool {
	run := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '%' && i+2 < len(text) && isHexByte(text[i+1]) && isHexByte(text[i+2]):
			return true
		case c == '+' && i > 0 && text[i-1] == 'U':
			return true
		case c == '\\' && i+1 < len(text) && (text[i+1] == 'u' || text[i+1] == 'U'):
			return true
		}
		if isBase64RunByte(c) {
			run++
			if run >= minEncodedRun {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

// isHexByte is [0-9A-Fa-f].
func isHexByte(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

// isBase64RunByte is the codec's base64 class [\w\/+-] (\w is ASCII
// [0-9A-Za-z_] in Go's regexp).
func isBase64RunByte(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') ||
		c == '_' || c == '/' || c == '+' || c == '-'
}
