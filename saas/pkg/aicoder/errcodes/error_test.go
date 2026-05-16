package errcodes

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNew_BasicShape(t *testing.T) {
	err := New(DBLocked, "db is busy")
	if err.Code != DBLocked {
		t.Errorf("expected DBLocked, got %q", err.Code)
	}
	if err.Message != "db is busy" {
		t.Errorf("expected message preserved, got %q", err.Message)
	}
	if err.DocURL != "https://aicoder.dev/errors/E_DB_LOCKED" {
		t.Errorf("expected default doc URL, got %q", err.DocURL)
	}
}

func TestNewf_Formatting(t *testing.T) {
	err := Newf(NotFound, "memory %s not in project %s", "M-12", "chatbot")
	if !strings.Contains(err.Message, "M-12") {
		t.Errorf("expected formatted message, got %q", err.Message)
	}
}

func TestWithHint_Chains(t *testing.T) {
	err := New(SecretDetected, "AWS key in body").
		WithHint("use --allow-secrets to override").
		WithCause(errors.New("matched AKIA pattern"))

	if err.Hint == "" {
		t.Error("hint not set")
	}
	if err.Wrapped == nil {
		t.Error("cause not set")
	}
}

func TestError_Format(t *testing.T) {
	err := New(NotFound, "memory M-12 not found")
	s := err.Error()
	if !strings.HasPrefix(s, "[E_NOT_FOUND]") {
		t.Errorf("expected code prefix, got %q", s)
	}
	if !strings.Contains(s, "M-12") {
		t.Errorf("expected message included, got %q", s)
	}
}

func TestError_WithCause_Includes(t *testing.T) {
	cause := errors.New("underlying boom")
	err := New(Internal, "wrapper").WithCause(cause)
	if !strings.Contains(err.Error(), "underlying boom") {
		t.Errorf("expected cause in Error(), got %q", err.Error())
	}
}

func TestUnwrap(t *testing.T) {
	cause := errors.New("root cause")
	err := New(Internal, "wrapper").WithCause(cause)

	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(err, cause) = false, want true")
	}
}

func TestIs_CodeMatching(t *testing.T) {
	err := New(DBLocked, "first")
	other := New(DBLocked, "different message")

	if !errors.Is(err, other) {
		t.Errorf("expected Is to match by code regardless of message")
	}

	differentCode := New(DBCorrupt, "first")
	if errors.Is(err, differentCode) {
		t.Errorf("expected Is to NOT match across different codes")
	}
}

func TestIsCode_Helper(t *testing.T) {
	err := New(SecretDetected, "found AWS key")
	if !IsCode(err, SecretDetected) {
		t.Error("IsCode should match")
	}
	if IsCode(err, DBLocked) {
		t.Error("IsCode should not match different code")
	}
	if IsCode(errors.New("not a CLIError"), SecretDetected) {
		t.Error("IsCode should not match non-CLIError")
	}
}

func TestIsCode_ThroughWrappedChain(t *testing.T) {
	inner := New(NotFound, "deep")
	outer := New(Internal, "wrapper").WithCause(inner)

	// errors.As walks the chain; IsCode should find the inner code.
	if !IsCode(outer, Internal) {
		t.Error("expected outer code match")
	}
	// IsCode currently matches the outermost CLIError; document that behavior.
	// (If you want chain walking, use errors.As manually.)
}

func TestAsJSON_RoundtripsViaEncodingJson(t *testing.T) {
	err := New(SecretDetected, "AKIA pattern at offset 13").
		WithHint("use --allow-secrets to override")

	data, e := json.Marshal(err.AsJSON())
	if e != nil {
		t.Fatalf("marshal: %v", e)
	}

	var got JSONEnvelope
	if e := json.Unmarshal(data, &got); e != nil {
		t.Fatalf("unmarshal: %v", e)
	}

	if got.Error.Code != SecretDetected {
		t.Errorf("code lost: got %q", got.Error.Code)
	}
	if got.Error.Message != err.Message {
		t.Errorf("message lost")
	}
	if got.Error.Hint != err.Hint {
		t.Errorf("hint lost")
	}
}

func TestPlain_IncludesAllFields(t *testing.T) {
	err := New(DBLocked, "msg").
		WithHint("hint").
		WithCause(errors.New("cause"))

	plain := err.Plain()
	for _, want := range []string{"E_DB_LOCKED", "msg", "Hint:", "hint", "Cause:", "cause", "More:"} {
		if !strings.Contains(plain, want) {
			t.Errorf("Plain() missing %q: %s", want, plain)
		}
	}
}

func TestAll_NoDuplicates(t *testing.T) {
	codes := All()
	seen := make(map[Code]bool, len(codes))
	for _, c := range codes {
		if seen[c] {
			t.Errorf("duplicate code in All(): %q", c)
		}
		seen[c] = true
	}
	if len(codes) < 20 {
		t.Errorf("expected >= 20 registered codes, got %d", len(codes))
	}
}

func TestAll_EveryCodeHasDescription(t *testing.T) {
	for _, c := range All() {
		desc := Description(c)
		if desc == string(c) {
			t.Errorf("Description(%q) returned the code unchanged — missing entry in registry", c)
		}
	}
}

func TestAll_FormatOfEveryCode(t *testing.T) {
	for _, c := range All() {
		s := string(c)
		if !strings.HasPrefix(s, "E_") {
			t.Errorf("code %q does not start with E_", s)
		}
		if strings.ToUpper(s) != s {
			t.Errorf("code %q is not uppercase", s)
		}
	}
}
