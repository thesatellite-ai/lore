package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestUnscopedNameHidesTheOSLogin (devco J-12): this machine's setup-time
// stamp from the OS user (or a machine id) is listed in Team versions as
// "you on this machine, before the identity step", never as the OS login;
// a person's identity outside any setup keeps its name and step.
func TestUnscopedNameHidesTheOSLogin(t *testing.T) {
	t.Parallel()
	for _, step := range []identity.Step{identity.StepOSUser, identity.StepMachine, identity.StepSalt} {
		got := unscopedName(identity.Resolved{Step: step, StableKey: "human:hostuser@box.example", DisplayName: "hostuser"})
		if got != beforeIdentityName || strings.Contains(got, "hostuser") {
			t.Errorf("step %q: %q, want %q", step, got, beforeIdentityName)
		}
	}
	got := unscopedName(identity.Resolved{Step: identity.StepGit, StableKey: "human:kwame@example.test", DisplayName: "Kwame"})
	if got != "Kwame ("+string(identity.StepGit)+unscopedNote+")" {
		t.Errorf("git identity: %q", got)
	}
}

// TestDoctorPushModesSkipsAFolderStore (devco J-12): a personal store that
// is a folder has no push mode, so doctor prints no "store me: " line with
// nothing after the colon; a git store keeps its line.
func TestDoctorPushModesSkipsAFolderStore(t *testing.T) {
	t.Parallel()
	views := []opsstores.PushView{
		{Store: "me", Place: opsstores.PlacePersonal, Folder: true, URL: "folder:///home/kwame/notes"},
		{Store: "umbrella", Place: opsstores.PlaceOrg, Summary: "push manual, compact off"},
	}
	var lines []string
	add := func(s string) { lines = append(lines, s) }
	pushModeNotes(views, add, add, map[string]string{})
	if len(lines) != 1 || lines[0] != "store umbrella: push manual, compact off" {
		t.Fatalf("push-mode lines %q, want only the git store's", lines)
	}
}
