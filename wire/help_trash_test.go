package wire_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/link"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/ops/trash"
)

// TestRemoveHelpSaysTrash pins two help-text findings of the earlier rounds:
// `link remove` and `vars rm` said "a tombstone is kept" while both move the
// row to the trash (restorable), and `trash list --with-dependents` called
// comments and links dependents though they stay live and are never in the
// trash. Help is what a person and an agent read before acting.
func TestRemoveHelpSaysTrash(t *testing.T) {
	h := opstest.New(t)
	reg := h.App.Registry()
	for _, name := range []string{link.OpRemove, settings.OpVarsRm} {
		e, ok := reg.Lookup(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		if s := e.Spec().Summary; strings.Contains(s, "tombstone") || !strings.Contains(s, "to the trash") {
			t.Errorf("%s summary %q: want it to say the row goes to the trash, never a tombstone", name, s)
		}
	}
	e, ok := reg.Lookup(trash.OpList)
	if !ok {
		t.Fatalf("%s is not registered", trash.OpList)
	}
	found := false
	for _, f := range e.InputFields() {
		if f.Flag != "with-dependents" {
			continue
		}
		found = true
		deps, _, _ := strings.Cut(f.Desc, ";")
		if strings.Contains(deps, "comment") || strings.Contains(deps, "link") || !strings.Contains(f.Desc, "comments and links stay live") {
			t.Errorf("--with-dependents help %q: comments and links are not dependents", f.Desc)
		}
	}
	if !found {
		t.Errorf("%s has no --with-dependents flag", trash.OpList)
	}
}
