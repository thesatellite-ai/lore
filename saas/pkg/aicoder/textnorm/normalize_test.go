package textnorm

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalize_NFC(t *testing.T) {
	nfd := "cafe\u0301" // 'e' followed by combining acute U+0301
	nfc := "caf\u00e9"  // pre-composed e-acute U+00E9
	got, err := Normalize(nfd)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got != nfc {
		t.Errorf("expected %q (NFC), got %q", nfc, got)
	}
}

func TestNormalize_StripBOM(t *testing.T) {
	got, err := Normalize("\ufeffhello")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestNormalize_CRLF(t *testing.T) {
	got, err := Normalize("a\r\nb\r\nc")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got != "a\nb\nc" {
		t.Errorf("expected LF, got %q", got)
	}
}

func TestNormalize_StripBareCR(t *testing.T) {
	got, err := Normalize("a\rb\rc")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got != "a\nb\nc" {
		t.Errorf("expected bare CR -> LF, got %q", got)
	}
}

func TestNormalize_StripBidiOverride(t *testing.T) {
	// U+202E RIGHT-TO-LEFT OVERRIDE (Trojan Source attack)
	in := "visible\u202e hidden"
	got, err := Normalize(in)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if strings.ContainsRune(got, '\u202e') {
		t.Errorf("bidi char not stripped: %q", got)
	}
	if got != "visible hidden" {
		t.Errorf("got %q", got)
	}
}

func TestNormalize_StripAllBidiAndZW(t *testing.T) {
	cases := []rune{
		'\u200b', '\u200c', '\u200d', '\ufeff',
		'\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
		'\u2066', '\u2067', '\u2068', '\u2069',
	}
	for _, r := range cases {
		in := "a" + string(r) + "b"
		got, err := Normalize(in)
		if err != nil {
			t.Errorf("Normalize(%U): %v", r, err)
			continue
		}
		if strings.ContainsRune(got, r) {
			t.Errorf("U+%04X not stripped: %q", r, got)
		}
	}
}

func TestNormalize_RejectEmpty(t *testing.T) {
	cases := []string{"", " ", "\t", "\n", "  \t \n  "}
	for _, c := range cases {
		_, err := Normalize(c)
		if !errors.Is(err, ErrEmpty) {
			t.Errorf("Normalize(%q) expected ErrEmpty, got %v", c, err)
		}
	}
}

func TestNormalize_RejectNullByte(t *testing.T) {
	_, err := Normalize("hello\x00world")
	if !errors.Is(err, ErrNullByte) {
		t.Errorf("expected ErrNullByte, got %v", err)
	}
}

func TestNormalize_RejectInvalidUTF8(t *testing.T) {
	_, err := Normalize(string([]byte{0xff, 0xfe, 0xfd}))
	if !errors.Is(err, ErrInvalidUTF8) {
		t.Errorf("expected ErrInvalidUTF8, got %v", err)
	}
}

func TestNormalize_PreservesEmoji(t *testing.T) {
	in := "hello \u1f44b world"
	got, err := Normalize(in)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got != in {
		t.Errorf("expected emoji preserved, got %q", got)
	}
}

func TestNormalize_PreservesCJK(t *testing.T) {
	in := "\u4f60\u597d\u4e16\u754c"
	got, err := Normalize(in)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got != in {
		t.Errorf("expected CJK preserved, got %q", got)
	}
}

func TestNormalize_PreservesRTL(t *testing.T) {
	in := "\u0645\u0631\u062d\u0628\u0627" // Arabic 'hello'
	got, err := Normalize(in)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got != in {
		t.Errorf("expected RTL preserved, got %q", got)
	}
}

func TestNormalize_PreservesEmbeddedTabs(t *testing.T) {
	in := "code\tblock\there"
	got, err := Normalize(in)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got != in {
		t.Errorf("expected tabs preserved, got %q", got)
	}
}

func TestValidateIdentifier_GoodIDs(t *testing.T) {
	cases := []string{"chatbot", "task", "web1", "shared-frontend", "api.v2", "my_project_name"}
	for _, c := range cases {
		got, err := ValidateIdentifier(c)
		if err != nil {
			t.Errorf("ValidateIdentifier(%q): %v", c, err)
		}
		if got != c {
			t.Errorf("ValidateIdentifier(%q) = %q, want unchanged", c, got)
		}
	}
}

func TestValidateIdentifier_RejectsBadIDs(t *testing.T) {
	cases := []string{
		"WithCaps", "with space", "with/slash", "with$dollar",
		"ch\u00e0rbot",                         // non-ASCII
		"\u043f\u0440\u043e\u0435\u043a\u0442", // Cyrillic
		"", " ",
	}
	for _, c := range cases {
		if _, err := ValidateIdentifier(c); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestValidatePath_GoodPaths(t *testing.T) {
	cases := []string{
		"/usr/local/bin",
		"./relative/path",
		"file with spaces.txt",
		"~/dir",
		"with/tab\there",
	}
	for _, c := range cases {
		if err := ValidatePath(c); err != nil {
			t.Errorf("ValidatePath(%q): %v", c, err)
		}
	}
}

func TestValidatePath_RejectsControlChars(t *testing.T) {
	cases := []string{
		"with\nnewline",
		"with\rCR",
		"with\x00null",
		"with\x07bell",
	}
	for _, c := range cases {
		if err := ValidatePath(c); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}
