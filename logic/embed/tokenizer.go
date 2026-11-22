package embed

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// tokenizer turns text into vocabulary ids the way the model's Hugging
// Face WordPiece tokenizer does with add_special_tokens=False (what
// model2vec calls): the BERT normaliser (clean, CJK split, accents
// stripped, lower case), the BERT pre-tokeniser (whitespace and
// punctuation), then greedy longest-match WordPiece. Ids match model2vec
// 0.10.0 exactly on the exploration's 200-text parity set.
type tokenizer struct {
	order    []uint32 // ids sorted by token bytes
	off      []uint32 // token byte offsets by id, n+1 entries
	blob     string   // every token's bytes
	prefix   string   // the continuing-subword prefix (##)
	maxChars int      // a longer word is one unknown token
	unkID    int      // -1 when none
	added    []added

	clean, chinese, strip, lower bool
}

// encode returns the ids of text (no special tokens added).
func (t *tokenizer) encode(text string) []int {
	var ids []int
	// added tokens are matched on the raw text first and never split
	for len(text) > 0 {
		at, tok := -1, added{}
		for _, a := range t.added {
			if i := strings.Index(text, a.Content); i >= 0 && (at < 0 || i < at || (i == at && len(a.Content) > len(tok.Content))) {
				at, tok = i, a
			}
		}
		if at < 0 {
			ids = t.encodeSegment(text, ids)
			break
		}
		ids = t.encodeSegment(text[:at], ids)
		ids = append(ids, tok.ID)
		text = text[at+len(tok.Content):]
	}
	return ids
}

// encodeSegment appends the ids of s (no added token in it).
func (t *tokenizer) encodeSegment(s string, ids []int) []int {
	for _, w := range preTokenize(t.normalize(s)) {
		ids = t.wordPiece(w, ids)
	}
	return ids
}

// lookup finds s, with the continuing-subword prefix when prefixed.
func (t *tokenizer) lookup(prefixed bool, s string) (int, bool) {
	p := ""
	if prefixed {
		p = t.prefix
	}
	return t.find(p, s)
}

// normalize is the BERT normaliser.
func (t *tokenizer) normalize(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, c := range s {
		if t.clean {
			if c == 0 || c == 0xfffd || isControl(c) {
				continue
			}
			if isSpace(c) {
				sb.WriteByte(' ')
				continue
			}
		}
		if t.chinese && isChinese(c) {
			sb.WriteByte(' ')
			sb.WriteRune(c)
			sb.WriteByte(' ')
			continue
		}
		sb.WriteRune(c)
	}
	out := sb.String()
	if t.strip {
		d := norm.NFD.String(out)
		sb.Reset()
		for _, c := range d {
			if !unicode.Is(unicode.Mn, c) {
				sb.WriteRune(c)
			}
		}
		out = sb.String()
	}
	if t.lower {
		out = rustLower(out)
	}
	return out
}

// rustLower lowercases like the Hugging Face tokenizer: Rust's
// char::to_lowercase per char (full mappings, no final-sigma rule).
func rustLower(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToLower(s)
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for _, c := range s {
		if c == 'İ' { // U+0130 -> i + combining dot above
			sb.WriteString("i̇")
			continue
		}
		sb.WriteRune(unicode.ToLower(c))
	}
	return sb.String()
}

// isSpace is BERT's whitespace.
func isSpace(c rune) bool {
	switch c {
	case '\t', '\n', '\r', ' ':
		return true
	}
	return unicode.Is(unicode.White_Space, c)
}

// isControl is the tokenizer's is_control: categories Cc, Cf, Cn, Co, Cs,
// but not tab, newline or carriage return.
func isControl(c rune) bool {
	switch c {
	case '\t', '\n', '\r':
		return false
	}
	if unicode.In(c, unicode.Cc, unicode.Cf, unicode.Co, unicode.Cs) {
		return true
	}
	return !unicode.In(c, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Z) // Cn
}

// isChinese is a CJK ideograph (each becomes its own word).
func isChinese(c rune) bool {
	return (c >= 0x4E00 && c <= 0x9FFF) || (c >= 0x3400 && c <= 0x4DBF) || (c >= 0x20000 && c <= 0x2A6DF) ||
		(c >= 0x2A700 && c <= 0x2B73F) || (c >= 0x2B740 && c <= 0x2B81F) || (c >= 0x2B920 && c <= 0x2CEAF) ||
		(c >= 0xF900 && c <= 0xFAFF) || (c >= 0x2F800 && c <= 0x2FA1F)
}

// isPunct is BERT's punctuation: ASCII symbols and Unicode P.
func isPunct(c rune) bool {
	if c < 0x80 {
		return (c >= 33 && c <= 47) || (c >= 58 && c <= 64) || (c >= 91 && c <= 96) || (c >= 123 && c <= 126)
	}
	return unicode.Is(unicode.P, c)
}

// preTokenize splits on whitespace (dropped) and punctuation (each
// punctuation char its own word).
func preTokenize(s string) []string {
	var out []string
	start := -1
	for i, c := range s {
		switch {
		case unicode.Is(unicode.White_Space, c):
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		case isPunct(c):
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
			out = append(out, s[i:i+utf8.RuneLen(c)])
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

// wordPiece appends w's pieces: the longest vocabulary prefix, then the
// longest ##-continuation of the rest; a word with an unknown piece is one
// unknown token.
func (t *tokenizer) wordPiece(w string, ids []int) []int {
	if utf8.RuneCountInString(w) > t.maxChars {
		if t.unkID >= 0 {
			ids = append(ids, t.unkID)
		}
		return ids
	}
	mark := len(ids)
	start := 0
	for start < len(w) {
		end := len(w)
		found := -1
		for end > start {
			if id, ok := t.lookup(start > 0, w[start:end]); ok {
				found = id
				break
			}
			_, sz := utf8.DecodeLastRuneInString(w[start:end])
			end -= sz
		}
		if found < 0 {
			ids = ids[:mark]
			if t.unkID >= 0 {
				ids = append(ids, t.unkID)
			}
			return ids
		}
		ids = append(ids, found)
		start = end
	}
	return ids
}
