package rule_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/rule"
)

// TestListHelpSaysArchivedAreAdded pins bug hunt B3-31: `lore rule list
// --help` said archived rules show "only with --archived", but --archived
// lists the live rules plus the archived ones (TestTextMatchesV017 pins that
// output). The help line and the flag text must say the archived ones are
// added, never that they are shown alone.
func TestListHelpSaysArchivedAreAdded(t *testing.T) {
	h := opstest.New(t)
	e, ok := h.App.Registry().Lookup(rule.OpList)
	if !ok {
		t.Fatalf("%s is not registered", rule.OpList)
	}
	sum := e.Spec().Summary
	if strings.Contains(sum, "only with --archived") || !strings.Contains(sum, "too with --archived") {
		t.Errorf("%s summary %q: want archived ones listed too with --archived, not only", rule.OpList, sum)
	}
	found := false
	for _, f := range e.InputFields() {
		if f.Flag != "archived" {
			continue
		}
		found = true
		if !strings.HasPrefix(f.Desc, "include ") {
			t.Errorf("--archived help %q: want it to say the archived rows are included", f.Desc)
		}
	}
	if !found {
		t.Errorf("%s has no --archived flag", rule.OpList)
	}
}
