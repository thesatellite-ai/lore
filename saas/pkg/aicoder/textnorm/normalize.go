// Package textnorm normalizes user-supplied text at the write boundary.
//
// Strips Trojan Source bidi (U+202A-U+202E, U+2066-U+2069), zero-width chars
// (U+200B/C/D, U+FEFF), applies NFC normalization, validates UTF-8, rejects
// null bytes and empty bodies.
//
// Catches: R23 #19-22, R24 #1+#4+#5+#16+#17, R27 #31.
package textnorm

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var (
	ErrEmpty       = errors.New("textnorm: body is empty after normalization")
	ErrInvalidUTF8 = errors.New("textnorm: input is not valid UTF-8")
	ErrNullByte    = errors.New("textnorm: input contains null byte (binary content?)")
	ErrControlChar = errors.New("textnorm: input contains disallowed control character")
	ErrMixedScript = errors.New("textnorm: identifier contains disallowed character")
)

// utf8BOM is U+FEFF in UTF-8. Stored as Unicode escape so the source file
// itself contains no BOM (Go's parser rejects literal U+FEFF anywhere).
const utf8BOM = "\ufeff"

// stripChars are bidi + zero-width code points removed from input.
// All entries use Unicode escapes to keep the source file ASCII-clean.
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
	'\u200c': true, // ZWNJ
	'\u200d': true, // ZWJ
	'\ufeff': true, // ZWNBSP / BOM
}

// Normalize cleans a body string for storage.
//
// Steps (in order):
//  1. Validate UTF-8.
//  2. Reject null bytes.
//  3. Strip leading BOM.
//  4. CRLF -> LF, bare CR -> LF.
//  5. Strip bidi/zero-width code points.
//  6. Apply NFC.
//  7. Trim leading/trailing whitespace.
//  8. Reject empty.
func Normalize(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", ErrInvalidUTF8
	}
	if strings.ContainsRune(s, 0) {
		return "", ErrNullByte
	}

	s = strings.TrimPrefix(s, utf8BOM)

	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if stripChars[r] {
			continue
		}
		b.WriteRune(r)
	}
	s = b.String()

	s = norm.NFC.String(s)

	s = strings.TrimSpace(s)
	if s == "" {
		return "", ErrEmpty
	}
	return s, nil
}

// ValidateIdentifier checks a structural identifier (project/repo names) for
// safety. Returns the normalized identifier on success.
func ValidateIdentifier(s string) (string, error) {
	cleaned, err := Normalize(s)
	if err != nil {
		return "", err
	}
	for _, r := range cleaned {
		if !isIdentChar(r) {
			return "", fmt.Errorf("%w: %q", ErrMixedScript, string(r))
		}
	}
	return cleaned, nil
}

// ValidatePath rejects paths with control characters.
func ValidatePath(p string) error {
	for _, r := range p {
		if r == 0 || (unicode.IsControl(r) && r != '\t') {
			return fmt.Errorf("%w: U+%04X", ErrControlChar, r)
		}
	}
	return nil
}

func isIdentChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '-' || r == '_' || r == '.':
		return true
	}
	return false
}
