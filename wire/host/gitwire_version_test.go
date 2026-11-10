package host

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWiringFingerprintIncludesLoreVersion (E-FATES-2, port of v0.1.17
// sync_action_refresh_test.go TestWiringVersionIncludesLoreVersion): the
// wiring stamp changes when the lore version changes, so a binary replaced
// at the same path re-wires the clone once instead of keeping hooks and
// driver config written for an older lore; the same version, path and
// files give the same stamp (re-wiring stays a no-op).
func TestWiringFingerprintIncludesLoreVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, gitAttributesFile)
	if err := os.WriteFile(f, []byte("x\n"), gitAttributesMode); err != nil {
		t.Fatal(err)
	}
	paths := []string{f, filepath.Join(dir, "missing")}
	const bin = "'/usr/local/bin/lore'"
	for _, tc := range []struct{ name, a, b string }{
		{"patch upgrade", "v0.1.18", "v0.1.19"},
		{"dev build vs release", "v0.1.18", "v0.1.18-3-gabc1234"},
		{"unknown vs known", "", "v0.1.18"},
	} {
		if wiringFingerprint(tc.a, bin, "", paths) == wiringFingerprint(tc.b, bin, "", paths) {
			t.Errorf("%s: the wiring stamp ignores the lore version (%q vs %q)", tc.name, tc.a, tc.b)
		}
	}
	first := wiringFingerprint("v0.1.18", bin, "svc", paths)
	if again := wiringFingerprint("v0.1.18", bin, "svc", paths); again != first {
		t.Fatalf("same version, binary, project and files must give the same stamp: %s then %s", first, again)
	}
}
