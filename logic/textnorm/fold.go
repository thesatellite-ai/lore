package textnorm

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// SearchFold is s as the full-text index and a search query both see it:
// the Turkish dotless ı (U+0131) as a plain i. The index tokenizer
// (unicode61, diacritics removed) already lowercases I and folds the
// dotted İ (U+0130) to i, but ı has no decomposition, so "ıspanak" never
// met a typed ISPANAK and DİYARBAKIR never met diyarbakır (finding I-9).
// Folding ı on both sides makes i, ı, I and İ one letter for search. One
// rune becomes one rune, so text keeps its shape; only a snippet shows
// the plain i.
func SearchFold(s string) string {
	if !strings.ContainsRune(s, dotlessI) {
		return s
	}
	return strings.ReplaceAll(s, string(dotlessI), "i")
}

// dotlessI is the Turkish lowercase dotless i.
const dotlessI = 'ı'

// letterFolds are the Latin letters that carry no combining mark to drop
// but have a plain ASCII spelling people type for them.
var letterFolds = map[rune]string{
	'ı': "i", 'ß': "ss", 'ø': "o", 'Ø': "O", 'æ': "ae", 'Æ': "AE", 'œ': "oe", 'Œ': "OE",
	'đ': "d", 'Đ': "D", 'ł': "l", 'Ł': "L", 'þ': "th", 'Þ': "TH", 'ð': "d", 'Ð': "D",
}

// ASCIIFold is s with its accents dropped and the letters above spelled
// in ASCII: "Chloé" → "Chloe", "Øyvind" → "Oyvind", "İlkay" → "Ilkay".
// A handle is one ASCII word, so this runs before the handle rule maps
// what is left to '-' (finding I-10: `--handle chloé` was cut to @chlo).
// Characters with no Latin spelling (CJK, Cyrillic) are kept as they are.
func ASCIIFold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if f, ok := letterFolds[r]; ok {
			b.WriteString(f)
			continue
		}
		b.WriteRune(r)
	}
	return norm.NFC.String(b.String())
}
