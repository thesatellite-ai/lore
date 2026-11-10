package errs

import (
	"errors"
	"testing"
)

// Prefix keeps a lore Error's reason, code and hint after the context it
// adds (the surfaces print Message and Hint, never Cause), and keeps a
// non-lore error's text hidden as the cause, like Wrap.
func TestPrefixKeepsReason(t *testing.T) {
	inner := New(InvalidInput, `"Istanbul time" is not an IANA zone name`).WithHint("give an IANA zone name such as Europe/Istanbul")
	for _, tc := range []struct {
		name      string
		err       error
		code      Code
		msg, hint string
	}{
		{"lore error", inner, InvalidInput, `actor edit --timezone: "Istanbul time" is not an IANA zone name`, "give an IANA zone name such as Europe/Istanbul"},
		{"wrapped lore error", Wrap(InvalidUTF8, "value: text is not valid UTF-8", errors.New("byte 3")), InvalidUTF8, "actor edit --timezone: value: text is not valid UTF-8", ""},
		{"lore error behind fmt", errors.Join(errors.New("ctx"), inner), InvalidInput, `actor edit --timezone: "Istanbul time" is not an IANA zone name`, "give an IANA zone name such as Europe/Istanbul"},
		{"empty inner message", New(URLUserinfo, ""), URLUserinfo, "actor edit --timezone", ""},
		{"plain error stays hidden", errors.New("driver: /secret/path"), Internal, "actor edit --timezone", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := EnvelopeOf(Prefix("actor edit --timezone", tc.err)).Error
			if b.Code != tc.code || b.Message != tc.msg || b.Hint != tc.hint {
				t.Fatalf("got %s %q hint %q, want %s %q hint %q", b.Code, b.Message, b.Hint, tc.code, tc.msg, tc.hint)
			}
		})
	}
	if inner.Message != `"Istanbul time" is not an IANA zone name` {
		t.Fatalf("Prefix changed the inner error: %q", inner.Message)
	}
}
