package host

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDriverConfigAction: whether git wiring writes one merge driver key,
// over (what the key holds) × (ordinary command or install step). A lore
// driver that runs is never replaced; a missing key or a lore driver whose
// absolute program is gone is always written; a value that is not a lore
// driver, and a custom .name label, only by an install step. The
// end-to-end matrix is tests/userscen/sync_mergedriver_test.go.
func TestDriverConfigAction(t *testing.T) {
	t.Parallel()
	const want = "lore merge-driver %O %A %B %P"
	gone := "'" + filepath.ToSlash(filepath.Join(t.TempDir(), "gone", "lore")) + "' merge-driver %O %A %B %P"
	self, err := os.Executable() // a program that exists on every OS
	if err != nil {
		t.Fatal(err)
	}
	runs := "'" + filepath.ToSlash(self) + "' merge-driver %O %A %B %P"
	driverKey := "merge." + mergeDriverLore + ".driver"
	nameKey := "merge." + mergeDriverLore + ".name"
	for _, tc := range []struct {
		name, key, cur        string
		found                 bool
		wantAuto, wantInstall bool
	}{
		{"unset", driverKey, "", false, true, true},
		{"empty", driverKey, "", true, true, true},
		{"same value", driverKey, want, true, false, false},
		{"bare lore, other form", driverKey, "lore merge-driver %O %A %B", true, false, false},
		{"abs lore that runs elsewhere", driverKey, runs, true, false, false},
		{"abs lore that is gone", driverKey, gone, true, true, true},
		{"not a lore driver", driverKey, "cat %A", true, false, true},
		{"custom name label", nameKey, "my label", true, false, true},
		{"unset name", nameKey, "", false, true, true},
	} {
		if got := driverConfigAction(wireAuto, tc.key, want, tc.cur, tc.found); got != tc.wantAuto {
			t.Errorf("%s, ordinary command: write = %v, want %v", tc.name, got, tc.wantAuto)
		}
		if got := driverConfigAction(wireInstall, tc.key, want, tc.cur, tc.found); got != tc.wantInstall {
			t.Errorf("%s, install step: write = %v, want %v", tc.name, got, tc.wantInstall)
		}
	}
}
