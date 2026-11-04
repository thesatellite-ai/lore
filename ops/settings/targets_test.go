package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestPolicySetContextTargets (Q-R2-3): context.targets takes known tool
// profiles (written as a repo row that policy show reads back) and refuses
// an unknown one with E_INVALID_INPUT naming it, writing nothing; the
// fields init writes are the same row shape.
func TestPolicySetContextTargets(t *testing.T) {
	h := opstest.Bare(t)
	h.MustYes(settings.OpPolicySet, setIn("context.targets", "cursor,codex", ""))
	r := showRules(t, h, "context.targets")["context.targets"]
	if r.Value != "cursor, codex" {
		t.Fatalf("show value %q, want the row as set", r.Value)
	}
	if r.Level != "repo" {
		t.Errorf("level %q, want repo", r.Level)
	}
	_, rerr := h.RunYes(settings.OpPolicySet, setIn("context.targets", "codex,claud", ""))
	if !errs.Has(rerr, errs.InvalidInput) || !strings.Contains(rerr.Error(), `"claud"`) {
		t.Errorf("unknown profile: %v, want E_INVALID_INPUT naming \"claud\"", rerr)
	}
	if got := showRules(t, h, "context.targets")["context.targets"].Value; got != r.Value {
		t.Errorf("a refused set changed the row: %q → %q", r.Value, got)
	}
	fields, err := settings.ContextTargetsFields([]string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"key", "value", "scope"} {
		if fields[k] == nil {
			t.Errorf("init row has no %s", k)
		}
	}
}
