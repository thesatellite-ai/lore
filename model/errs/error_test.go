package errs

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestEnvelopeContractFields pins the CLI_REFERENCE 1.4 fields of the --json
// error object: class defaults for retryable/needs_person, and next/question
// passing through.

func TestEnvelopeContractFields(t *testing.T) {
	t.Parallel()
	confirm := New(NeedsConfirm, "approve key").
		WithHint("ask the person, then re-run with --yes").
		WithQuestion("Approve customer_tier?").
		WithNext("lore", "meta", "describe", "customer_tier", "--yes")
	b := EnvelopeOf(confirm).Error
	if !b.Retryable || !b.NeedsPerson {
		t.Fatalf("needs_confirmation must default retryable+needs_person, got %+v", b)
	}
	if b.Question != "Approve customer_tier?" || b.Next == nil || b.Next.Command != "lore" || len(b.Next.Args) != 4 {
		t.Fatalf("question/next lost: %+v", b)
	}
	raw, err := json.Marshal(EnvelopeOf(confirm))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"retryable":true`, `"needs_person":true`, `"next":{"command":"lore"`, `"question":`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("missing %s in %s", key, raw)
		}
	}
	plain := EnvelopeOf(New(InvalidInput, "bad")).Error
	if plain.Retryable || plain.NeedsPerson || plain.Next != nil {
		t.Fatalf("bad_input must not default retryable/needs_person: %+v", plain)
	}
	explicit := &Error{Code: InvalidInput, Message: "x", Retryable: true}
	if !EnvelopeOf(explicit).Error.Retryable {
		t.Fatal("explicit Retryable must survive")
	}
}

// TestErrorNamesCauseOnce: a message that already carries its cause's text
// prints it once; any other message is followed by its cause.
func TestErrorNamesCauseOnce(t *testing.T) {
	t.Parallel()
	cause := errors.New("line 2: indented with a tab")
	if got := Wrap(ConfigInvalid, "config.yaml is not valid YAML: "+cause.Error(), cause).Error(); got != "[E_CONFIG_INVALID] config.yaml is not valid YAML: line 2: indented with a tab" {
		t.Errorf("cause in message: %q", got)
	}
	if got := Wrap(ConfigInvalid, "config.yaml is not valid YAML", cause).Error(); got != "[E_CONFIG_INVALID] config.yaml is not valid YAML: line 2: indented with a tab" {
		t.Errorf("cause apart: %q", got)
	}
}
