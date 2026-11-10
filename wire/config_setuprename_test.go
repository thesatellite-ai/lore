package wire

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestRenameSetupConfig: the entry takes the new name with its unknown
// keys, its repo pins follow, and a taken or malformed name writes
// nothing.
func TestRenameSetupConfig(t *testing.T) {
	t.Parallel()
	base := setupsBase + "  - name: work\n    kind: solo\nrepo_setups:\n  rep_one: personal\n  rep_two: work\n"
	home := writeSetupsConfig(t, base)

	found, err := RenameSetup(home, "personal", "home")
	if err != nil || !found {
		t.Fatalf("rename = %v, %v", found, err)
	}
	got := readSetupsFile(t, home)
	for _, want := range []string{"name: home", "colour: blue", "future_key", "rep_one: home", "rep_two: work"} {
		if !strings.Contains(got, want) {
			t.Errorf("after rename, config.yaml lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "personal") {
		t.Errorf("the old name is still in config.yaml:\n%s", got)
	}

	before := readSetupsFile(t, home)
	for _, c := range []struct {
		to   string
		code errs.Code
	}{{"work", errs.SetupNameTaken}, {"Bad Name", errs.InvalidInput}} {
		if _, err := RenameSetup(home, "home", c.to); !errs.Has(err, c.code) {
			t.Errorf("rename to %q: %v, want %s", c.to, err, c.code)
		}
		if readSetupsFile(t, home) != before {
			t.Errorf("a refused rename to %q changed config.yaml", c.to)
		}
	}
	if found, err := RenameSetup(home, "nobody", "x"); found || err != nil {
		t.Errorf("rename of a missing setup = %v, %v, want not found", found, err)
	}
}

// TestNoOpEditKeepsFile: an edit of config.yaml that changes nothing (a
// rename or disable of a name that is not there, a disable of a setup
// already disabled) leaves the file byte for byte, flow style included.
func TestNoOpEditKeepsFile(t *testing.T) {
	t.Parallel()
	flow := "setups: [{name: main, kind: solo, default: true, folders: [~/code/**], disabled: true}]\nidentity:\npersonal:\n"
	home := writeSetupsConfig(t, flow)
	if found, _, err := SetSetupDisabled(home, "nope", true, time.Now()); found || err != nil {
		t.Fatalf("disable unknown = %v, %v", found, err)
	}
	if _, changed, err := SetSetupDisabled(home, "main", true, time.Now()); changed || err != nil {
		t.Fatalf("disable a disabled setup = %v, %v", changed, err)
	}
	if found, err := RenameSetup(home, "nope", "other"); found || err != nil {
		t.Fatalf("rename unknown = %v, %v", found, err)
	}
	if got := readSetupsFile(t, home); got != flow {
		t.Errorf("a no-op edit rewrote config.yaml:\n%s", got)
	}
}
