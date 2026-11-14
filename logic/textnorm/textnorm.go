// Package textnorm is the text cleanup every stored text value goes through
// (decision P3-TEXT-1, ported from v0.1.17 saas/pkg/aicoder/textnorm, which
// is the reference because the design pages are silent).
//
// Why it exists: row text is rendered into LORE.md and read by people and
// coding agents. Invisible characters change what a reader sees without
// changing what an agent reads: a right-to-left override (U+202E) can make
// "safe<RLO>live_ks" display reversed (the "Trojan Source" class), a zero
// width space splits a word so a search or a leak rule misses it, and a
// CRLF from a Windows paste makes the same text two different byte
// strings across machines (different file hashes, spurious merge diffs).
// v0.1.17 cleaned bodies before storing them; the core lost that, so this
// package restores it on the one write path (logic/validate).
//
// Flow:
//
//	Clean      valid UTF-8? no NUL? ─► drop leading BOM ─► CRLF/CR → LF
//	           ─► drop bidi controls, ZWSP and a mid-text BOM ─► NFC
//	Normalize  Clean ─► trim ─► refuse empty (v0.1.17 Normalize, for a body)
//	ValidateIdentifier  Normalize ─► only [a-z0-9._-]
//	ValidatePath        refuse NUL and control characters (tab allowed)
//
// Fail closed: input that cannot be cleaned (invalid UTF-8, a NUL byte:
// binary content) is refused, never stored as is.
//
// Invariant: no error repeats the input (it may be a secret pasted by
// mistake); errors name only the problem and, for a control character, its
// code point.
//
// Layer L1: pure, no I/O; imports model only (plus x/text for NFC).
package textnorm

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/thesatellite-ai/lore/model/errs"
)

// utf8BOM is the byte order mark some editors put at the start of a file.
const utf8BOM = "\ufeff"

// Error texts (no value is ever repeated, see the package invariant).
const (
	msgInvalidUTF8  = "text is not valid UTF-8"
	msgNullByte     = "text contains a NUL byte (binary content?)"
	msgEmpty        = "text is empty after cleanup"
	msgControlChar  = "text contains a control character"
	msgIdentChar    = "identifier may contain only a-z, 0-9, '.', '-' and '_'"
	controlCharForm = "%s: U+%04X"
)

// stripChars are removed wherever they appear: the bidirectional
// embedding, override and isolate controls, ZWSP and ZWNBSP (the BOM used
// mid-text). ZWNJ (U+200C) and ZWJ (U+200D) are kept: they are part of
// the spelling (Persian می‌خواهم, Devanagari and Malayalam conjuncts) and
// of joined emoji (👩‍💻, 👨‍👩‍👧‍👦), so dropping them, as v0.1.17's list did,
// changed what the person wrote (I-4). They cannot reorder text the way a
// bidi control can. The FTS index relies on ZWSP never being stored
// (adapters/cache/sqlite/ftsseg.go).
var stripChars = map[rune]bool{
	'\u202a': true, // LRE
	'\u202b': true, // RLE
	'\u202c': true, // PDF
	'\u202d': true, // LRO
	'\u202e': true, // RLO
	'\u2066': true, // LRI
	'\u2067': true, // RLI
	'\u2068': true, // FSI
	'\u2069': true, // PDI
	'\u200b': true, // ZWSP
	'\ufeff': true, // ZWNBSP / BOM
}

// Clean returns s with a leading BOM dropped, CRLF and lone CR turned into
// LF, every stripChars character removed and the result NFC-normalised.
// Whitespace is kept (a text field's own rules decide about trimming).
// Invalid UTF-8 is E_INVALID_UTF8 and a NUL byte E_INVALID_INPUT. Clean is
// idempotent: Clean(Clean(s)) == Clean(s).
func Clean(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", errs.New(errs.InvalidUTF8, msgInvalidUTF8)
	}
	if strings.ContainsRune(s, 0) {
		return "", errs.New(errs.InvalidInput, msgNullByte)
	}
	s = strings.TrimPrefix(s, utf8BOM)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if strings.IndexFunc(s, func(r rune) bool { return stripChars[r] }) >= 0 {
		var b strings.Builder
		b.Grow(len(s))
		for _, r := range s {
			if !stripChars[r] {
				b.WriteRune(r)
			}
		}
		s = b.String()
	}
	return norm.NFC.String(s), nil
}

// Normalize is Clean plus trimming, refusing text that is empty afterwards
// (E_INVALID_INPUT): v0.1.17's Normalize, for a body that must say
// something.
func Normalize(s string) (string, error) {
	c, err := Clean(s)
	if err != nil {
		return "", err
	}
	c = strings.TrimSpace(c)
	if c == "" {
		return "", errs.New(errs.InvalidInput, msgEmpty)
	}
	return c, nil
}

// ValidateIdentifier normalises s and refuses any character outside
// [a-z0-9._-] (a project or folder name; mixed scripts and look-alike
// letters are not allowed, E_INVALID_INPUT).
func ValidateIdentifier(s string) (string, error) {
	c, err := Normalize(s)
	if err != nil {
		return "", err
	}
	for _, r := range c {
		if !isIdentChar(r) {
			return "", errs.New(errs.InvalidInput, msgIdentChar)
		}
	}
	return c, nil
}

// ValidatePath refuses a path holding NUL or a control character other
// than tab (E_INVALID_INPUT, naming the code point only).
func ValidatePath(p string) error {
	for _, r := range p {
		if r == 0 || (unicode.IsControl(r) && r != '\t') {
			return errs.New(errs.InvalidInput, fmt.Sprintf(controlCharForm, msgControlChar, r))
		}
	}
	return nil
}

// isIdentChar is one character an identifier may hold.
func isIdentChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
}

// EscapeControls is s as it may be shown on a terminal or written into a
// rendered file a person views (I-16): every C0 control character other
// than tab and line feed, DEL and every C1 control (U+0080..U+009F) is
// replaced by its escape, \x1b for ESC, \u009b for CSI. Stored text keeps
// those bytes (a row is not changed by being read); only what is printed
// is escaped, so a teammate's row holding ESC[2J, an OSC hyperlink or the
// conceal attribute cannot clear, recolour or relabel the reader's
// terminal or hide text from a person while an agent reads it. --json
// output needs none of this: JSON encodes every control as \u00XX. Text
// with no such character is returned as is (no allocation).
func EscapeControls(s string) string {
	if strings.IndexFunc(s, escaped) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for _, r := range s {
		switch {
		case !escaped(r):
			b.WriteRune(r)
		case r < 0x80:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

// escaped reports whether EscapeControls replaces r.
func escaped(r rune) bool {
	return r < 0x20 && r != '\t' && r != '\n' || r == 0x7f || r >= 0x80 && r <= 0x9f
}
