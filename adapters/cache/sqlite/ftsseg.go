package sqlite

import (
	"strings"
	"unicode"

	"github.com/thesatellite-ai/lore/logic/textnorm"
)

// FTS segmentation (I-5): the unicode61 tokenizer splits words at spaces
// and punctuation only, so a Chinese or Japanese sentence (no spaces
// between words) was one long token and `lore search 迁移` never found
// "数据库迁移规则"; emoji were separators, so `lore search 🚀` found
// nothing. The cache therefore stores every CJK character and every
// emoji/symbol as its own token, by putting a segment mark between it and
// a neighbouring word character, in the indexed text (ftsIndexText) and
// in the query (ftsQueryText) alike. FTS5 turns a query word that
// tokenizes into several tokens into a phrase of consecutive tokens, so
// "迁移" matches exactly where those two characters stand next to each
// other in the text: a substring match inside a CJK run, never a match of
// the characters scattered around the row.
//
// The mark is U+200B (zero width space): unicode61 treats it as a
// separator, and logic/textnorm drops it from every stored text value, so
// any U+200B in an FTS column is a mark and is removed again from the
// snippet (ftsSnippetText). The emoji tokens need the tokenizer to treat
// symbols (category So) and the skin-tone modifiers as token characters
// (ftsTokenizer); each is its own token, so 👍 finds 👍🏽 and 👩‍💻 finds
// the joined sequence (the ZWJ, a separator, sits between two tokens).

// ftsSegMark separates two tokens in indexed text and in a query.
const ftsSegMark = '​'

// Skin-tone modifiers U+1F3FB..U+1F3FF (category Sk, made token characters
// by ftsTokenizer's tokenchars; other Sk characters such as ` and ^ stay
// separators so `code` is still found as code).
const (
	skinToneFirst = '\U0001F3FB'
	skinToneLast  = '\U0001F3FF'
)

// Unicode blocks of CJK characters that carry no script of their own but
// belong to words: the iteration mark 々 and the katakana long-vowel
// mark ー (script Common).
const (
	cjkIterationMark = '々'
	katakanaLongMark = 'ー'
)

// segmented reports whether r is a token of its own: a Han, Hiragana,
// Katakana or Hangul character, a symbol (emoji and the like) or a skin
// tone modifier.
func segmented(r rune) bool {
	switch {
	case r < 0x2000:
		return r >= 0x80 && unicode.Is(unicode.So, r) // ©, ®, °: Latin-1 symbols
	case r == cjkIterationMark, r == katakanaLongMark:
		return true
	case r >= skinToneFirst && r <= skinToneLast:
		return true
	}
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul, unicode.So)
}

// wordChar reports whether r is part of a token for ftsTokenizer: a
// letter, a number, a private-use character or a segmented one.
func wordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Co, r) || segmented(r)
}

// ftsSegment puts ftsSegMark between two adjacent word characters when
// either is segmented. Text without a segmented character is returned as
// is (no allocation). It never adds a mark next to a space, quote, '*' or
// other FTS5 query syntax, so it is safe on a query as well as on text.
func ftsSegment(s string) string {
	if strings.IndexFunc(s, segmented) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + len(s)/2)
	var prev rune
	for i, r := range s {
		if i > 0 && wordChar(prev) && wordChar(r) && (segmented(prev) || segmented(r)) {
			b.WriteRune(ftsSegMark)
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

// ftsIndexText is a field value as the FTS table stores it: the Turkish
// dotless ı as i (textnorm.SearchFold, I-9; one rune for one rune, so
// unfoldSnippet can give a snippet its letters back), then segmented.
func ftsIndexText(s string) string { return ftsSegment(textnorm.SearchFold(s)) }

// ftsQueryText is a MATCH expression in the form that finds ftsIndexText.
func ftsQueryText(match string) string { return ftsSegment(textnorm.SearchFold(match)) }

// ftsSnippetText is an FTS snippet with the segment marks removed, so the
// person reads the row's own text.
func ftsSnippetText(s string) string {
	if !strings.ContainsRune(s, ftsSegMark) {
		return s
	}
	return strings.ReplaceAll(s, string(ftsSegMark), "")
}
