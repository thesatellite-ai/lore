package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
)

// TestPatchSetupKeepsAConcurrentWrite: an edit made from an earlier read
// writes only the keys it changed: an identity another process set on the
// same setup in between stays (the lost update of machines
// TestCampaignSetup_ConcurrentMixed), a key the edit removes goes, and a
// setup removed meanwhile is E_NOT_FOUND.
func TestPatchSetupKeepsAConcurrentWrite(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, "setups:\n  - name: main\n    kind: solo\n    default: true\n    note: old note\n    identity:\n      key: human:pat@example.test\n      name: Pat\n")
	cfg, err := ReadSetupsConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	before, ok := cfg.Setup("main")
	if !ok {
		t.Fatal("no setup main")
	}
	// another process sets the identity after the edit read the file
	if err = setSetupIdentity(home, "main", identity.Entry{Key: "human:alice@example.com", Name: "Alice"}); err != nil {
		t.Fatal(err)
	}
	after := before
	after.Note = "edited in parallel"
	if err = PatchSetup(home, before, after); err != nil {
		t.Fatal(err)
	}
	got := readSetupsFile(t, home)
	if !strings.Contains(got, "human:alice@example.com") || strings.Contains(got, "key: human:pat@example.test") {
		t.Fatalf("the concurrent identity was lost:\n%s", got)
	}
	if !strings.Contains(got, "edited in parallel") {
		t.Fatalf("the edit was not written:\n%s", got)
	}
	cleared := after
	cleared.Note = ""
	if err = PatchSetup(home, after, cleared); err != nil {
		t.Fatal(err)
	}
	if got = readSetupsFile(t, home); strings.Contains(got, "note:") || !strings.Contains(got, "human:alice@example.com") {
		t.Fatalf("removing the note:\n%s", got)
	}
	gone := after
	gone.Name = "nobody"
	if err = PatchSetup(home, gone, gone); err == nil || !strings.Contains(err.Error(), "no longer in") {
		t.Fatalf("a removed setup: %v, want E_NOT_FOUND", err)
	}
}
