package errs

import (
	"encoding/json"
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
