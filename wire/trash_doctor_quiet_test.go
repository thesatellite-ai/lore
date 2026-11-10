package wire

import (
	"context"
	"testing"
)

// TestDoctorTrashPrintsNoLocalHeading: `lore doctor`'s trash row reads the
// safety copies without the "this machine only: …" heading (it was a stray
// first line above the doctor report on cmd/lore); the --local listing
// itself (App.LocalTrash) still prints it.
func TestDoctorTrashPrintsNoLocalHeading(t *testing.T) {
	t.Parallel()
	var notes []string
	home := t.TempDir()
	a := openWith(t, gitRepo(t), Options{Home: home, Version: "0.1.18", OrgCommands: &standaloneOrg{home: home},
		Notice:       func(m string) { notes = append(notes, m) },
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, a)
	notes = nil
	if _, err := a.DoctorTrash(background()); err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if n == localTrashNote {
			t.Fatalf("doctor printed the --local heading: %q", notes)
		}
	}
	notes = nil
	if _, err := a.LocalTrash(background()); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0] != localTrashNote {
		t.Fatalf("trash list --local heading = %q", notes)
	}
}
