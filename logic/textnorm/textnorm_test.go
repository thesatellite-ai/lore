package textnorm

// The 17 v0.1.17 textnorm tests (saas/pkg/aicoder/textnorm) as matrices,
// plus Clean's own cases (whitespace kept, idempotent, errors never echo
// the input). P3-TEXT-1.

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// codeOf is the errs code of err ("" when none).
func codeOf(err error) errs.Code {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// bidiAndZW is every character Clean must strip.
var bidiAndZW = []rune{
	'\u200b', '\u200c', '\u200d', '\ufeff',
	'\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
	'\u2066', '\u2067', '\u2068', '\u2069',
}

// TestNormalizeMatrix ports TestNormalize_NFC, _StripBOM, _CRLF,
// _StripBareCR, _StripBidiOverride, _PreservesEmoji, _PreservesCJK,
// _PreservesRTL and _PreservesEmbeddedTabs.
func TestNormalizeMatrix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, in, want string }{
		{"NFC", "cafe\u0301", "caf\u00e9"},
		{"strip BOM", "\ufeffhello", "hello"},
		{"CRLF", "a\r\nb\r\nc", "a\nb\nc"},
		{"bare CR", "a\rb\rc", "a\nb\nc"},
		{"mixed CRLF and CR", "a\r\n\rb", "a\n\nb"},
		{"bidi override (Trojan Source)", "visible\u202e hidden", "visible hidden"},
		{"emoji kept", "hello \U0001F44B world", "hello \U0001F44B world"},
		{"CJK kept", "\u4f60\u597d\u4e16\u754c", "\u4f60\u597d\u4e16\u754c"},
		{"RTL letters kept", "\u0645\u0631\u062d\u0628\u0627", "\u0645\u0631\u062d\u0628\u0627"},
		{"embedded tabs kept", "code\tblock\there", "code\tblock\there"},
		{"trimmed", "  padded\n", "padded"},
	} {
		got, err := Normalize(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("%s: Normalize(%q) = %q, %v; want %q", tc.name, tc.in, got, err, tc.want)
		}
	}
}

// TestStripAllBidiAndZW ports TestNormalize_StripAllBidiAndZW: each
// character alone, at the start, the end and mid-word, through Clean and
// Normalize.
func TestStripAllBidiAndZW(t *testing.T) {
	t.Parallel()
	for _, r := range bidiAndZW {
		for _, in := range []string{"a" + string(r) + "b", string(r) + "ab", "ab" + string(r)} {
			for name, f := range map[string]func(string) (string, error){"Clean": Clean, "Normalize": Normalize} {
				got, err := f(in)
				if err != nil || got != "ab" {
					t.Errorf("%s(%U in %q) = %q, %v; want \"ab\"", name, r, in, got, err)
				}
			}
		}
	}
}

// TestRefusals ports TestNormalize_RejectEmpty, _RejectNullByte and
// _RejectInvalidUTF8: each with its code, and the message never repeats
// the input.
func TestRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   string
		want errs.Code
	}{
		{"empty", "", errs.InvalidInput},
		{"space", " ", errs.InvalidInput},
		{"tab", "\t", errs.InvalidInput},
		{"newline", "\n", errs.InvalidInput},
		{"whitespace mix", "  \t \n  ", errs.InvalidInput},
		{"only bidi", "\u202e\u200b", errs.InvalidInput},
		{"NUL byte", "secretvalue\x00world", errs.InvalidInput},
		{"invalid UTF-8", "secretvalue" + string([]byte{0xff, 0xfe, 0xfd}), errs.InvalidUTF8},
	} {
		_, err := Normalize(tc.in)
		if codeOf(err) != tc.want {
			t.Errorf("%s: Normalize err = %v, want %s", tc.name, err, tc.want)
			continue
		}
		if strings.Contains(err.Error(), "secretvalue") {
			t.Errorf("%s: the error repeats the input: %v", tc.name, err)
		}
	}
	// Clean refuses the same unreadable input but keeps whitespace-only text.
	for _, in := range []string{"a\x00b", string([]byte{0xc3})} {
		if _, err := Clean(in); err == nil {
			t.Errorf("Clean(%q) accepted unreadable input", in)
		}
	}
	if got, err := Clean("  "); err != nil || got != "  " {
		t.Errorf("Clean(spaces) = %q, %v; want the spaces kept", got, err)
	}
}

// TestCleanKeepsWhitespaceAndIsIdempotent: Clean leaves indentation and a
// trailing newline alone, and a second pass changes nothing (a stored value
// re-validated on every later edit must not drift).
func TestCleanKeepsWhitespaceAndIsIdempotent(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"  indented\r\n\tcode\r\n", "\ufeff\u202esafe\u200b\r", "cafe\u0301 \u4f60", "plain",
	} {
		once, err := Clean(in)
		if err != nil {
			t.Fatalf("Clean(%q): %v", in, err)
		}
		twice, err := Clean(once)
		if err != nil || twice != once {
			t.Errorf("Clean not idempotent on %q: %q then %q (%v)", in, once, twice, err)
		}
		if strings.ContainsAny(once, "\r\u202e\u200b\ufeff") {
			t.Errorf("Clean(%q) = %q still holds CR, bidi or zero-width", in, once)
		}
	}
	if got, _ := Clean("  indented\r\n\tcode\r\n"); got != "  indented\n\tcode\n" {
		t.Errorf("whitespace not kept: %q", got)
	}
}

// TestValidateIdentifier ports TestValidateIdentifier_GoodIDs and
// _RejectsBadIDs.
func TestValidateIdentifier(t *testing.T) {
	t.Parallel()
	for _, c := range []string{"chatbot", "task", "web1", "shared-frontend", "api.v2", "my_project_name"} {
		if got, err := ValidateIdentifier(c); err != nil || got != c {
			t.Errorf("ValidateIdentifier(%q) = %q, %v", c, got, err)
		}
	}
	for _, c := range []string{
		"WithCaps", "with space", "with/slash", "with$dollar",
		"ch\u00e0rbot", "\u043f\u0440\u043e\u0435\u043a\u0442", "", " ",
	} {
		if _, err := ValidateIdentifier(c); codeOf(err) != errs.InvalidInput {
			t.Errorf("ValidateIdentifier(%q) err = %v, want E_INVALID_INPUT", c, err)
		}
	}
}

// TestValidatePath ports TestValidatePath_GoodPaths and
// _RejectsControlChars.
func TestValidatePath(t *testing.T) {
	t.Parallel()
	for _, c := range []string{"/usr/local/bin", "./relative/path", "file with spaces.txt", "~/dir", "with/tab\there"} {
		if err := ValidatePath(c); err != nil {
			t.Errorf("ValidatePath(%q) = %v", c, err)
		}
	}
	for _, c := range []string{"with\nnewline", "with\rCR", "with\x00null", "with\x07bell"} {
		if err := ValidatePath(c); codeOf(err) != errs.InvalidInput {
			t.Errorf("ValidatePath(%q) = %v, want E_INVALID_INPUT", c, err)
		}
	}
}
