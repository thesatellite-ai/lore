package main

// The v0.1.17 TestUC_MiscSetupStampsFingerprint (saas/cmd/cli
// usecases_misc_test.go), ported before phase E onto cmd/lore: `lore setup`
// reports "setup complete", can be run again, and search works after it.
// The registry fingerprint stamped in lore.db's config table is retired
// with that table (p3-local); ops/setup TestSchemaModes covers the modes.
//
import (
	"strings"
	"testing"
)

func TestSetupIsRerunnableAndSearchWorks(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t)
	dir := w.initRepo(t)
	for pass := range 2 {
		code, out, errOut := w.lore(t, dir, "setup")
		if code != 0 || !strings.Contains(out+errOut, "setup complete") {
			t.Fatalf("setup pass %d: exit %d\n%s\n%s", pass+1, code, out, errOut)
		}
	}
	if code, out, errOut := w.lore(t, dir, "memory", "add", "--body", "searchable after setup"); code != 0 {
		t.Fatalf("memory add: exit %d\n%s\n%s", code, out, errOut)
	}
	code, out, errOut := w.lore(t, dir, "search", "searchable")
	if code != 0 || !strings.Contains(out, `Found 1 hit for "searchable"`) || !strings.Contains(out, "searchable after setup") {
		t.Fatalf("search after setup: exit %d\n%s\n%s", code, out, errOut)
	}
}
