package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
)

// TestSharedMachineFindings: one warning per shared id, the id cut to
// mch_ plus 8 hex (a short id kept whole), "(this machine)" only on this
// machine's id, hosts joined in order; none in, none out.
func TestSharedMachineFindings(t *testing.T) {
	if got := sharedMachineFindings(nil); len(got) != 0 {
		t.Fatalf("no shared ids: %+v", got)
	}
	got := sharedMachineFindings([]opsinfra.SharedMachine{
		{ID: "mch_0123456789abcdef0123456789abcdef", Hosts: []string{"alpha", "beta"}, This: true},
		{ID: "mch_ab", Hosts: []string{"a", "b", "c"}},
	})
	if len(got) != 2 {
		t.Fatalf("findings %+v", got)
	}
	for i, want := range []string{
		"machine mch_01234567 (this machine) is reported by hosts alpha, beta: two machines share an OS machine id",
		"machine mch_ab is reported by hosts a, b, c: two machines share",
	} {
		f := got[i]
		if f.Check != DoctorCheckSharedMachine || f.Level != DoctorWarning || !f.Plain || !strings.HasPrefix(f.Line(), want) {
			t.Errorf("finding %d = %+v (line %q)", i, f, f.Line())
		}
	}
	if strings.Contains(got[1].Line(), "this machine") {
		t.Errorf("another machine marked as this one: %q", got[1].Line())
	}
}

// TestTeamVersionFindings: the header note, one note per save (a save
// without a machine id named as a code repo save), and a warning after each
// save below the floor; no floor never warns; a dev build is never below.
func TestTeamVersionFindings(t *testing.T) {
	tvs := []sqlite.TeamVersion{
		{Actor: "ana", Machine: "mch_0123456789abcdef", Version: "0.1.18", LastSeen: "2026-11-01T00:00:00Z"},
		{Actor: "bob", Machine: "", Version: "0.1.17", LastSeen: "2026-10-01T00:00:00Z"},
		{Actor: "cy", Machine: "mch_ff", Version: "0.1.17-dev", LastSeen: "2026-11-02T00:00:00Z"},
	}
	cases := []struct {
		floor    string
		warnings []string
	}{
		{"", nil},
		{"0.1.18", []string{"bob on (code repo, no machine id) last wrote with lore 0.1.17, below min_lore_version 0.1.18: that lore is read-only here until it runs `lore upgrade`"}},
		{"0.1.19", []string{"ana on mch_01234567 last wrote with lore 0.1.18, below min_lore_version 0.1.19", "bob on (code repo, no machine id) last wrote with lore 0.1.17, below min_lore_version 0.1.19"}},
	}
	for _, c := range cases {
		t.Run("floor "+c.floor, func(t *testing.T) {
			got := teamVersionFindings(tvs, c.floor)
			var notes, warns []string
			for _, f := range got {
				if f.Check != DoctorCheckTeamVersions || !f.Plain {
					t.Fatalf("finding %+v", f)
				}
				switch f.Level {
				case DoctorNote:
					notes = append(notes, f.Line())
				case DoctorWarning:
					warns = append(warns, f.Line())
				case DoctorError:
					t.Fatalf("an error finding: %+v", f)
				}
			}
			wantNotes := []string{teamVersionsHeader,
				"  ana on mch_01234567: lore 0.1.18 (last save 2026-11-01T00:00:00Z)",
				"  bob on (code repo, no machine id): lore 0.1.17 (last save 2026-10-01T00:00:00Z)",
				"  cy on mch_ff: lore 0.1.17-dev (last save 2026-11-02T00:00:00Z)"}
			if strings.Join(notes, "|") != strings.Join(wantNotes, "|") {
				t.Fatalf("notes\n%q\nwant\n%q", notes, wantNotes)
			}
			if len(warns) != len(c.warnings) {
				t.Fatalf("warnings %q, want %d", warns, len(c.warnings))
			}
			for i, w := range c.warnings {
				if !strings.HasPrefix(warns[i], w) {
					t.Errorf("warning %d = %q, want prefix %q", i, warns[i], w)
				}
			}
		})
	}
}

// TestDoctorMachineRowsOnlyInCmdLore: the old binary's doctor host prints
// both rows itself, so the core checks are silent there; in the binary
// StandaloneOptionsOrg built they run: a saved row gives the Team versions
// header and its writer, and no shared machine id gives no warning.
func TestDoctorMachineRowsOnlyInCmdLore(t *testing.T) {
	repo := gitRepo(t)
	old := openWith(t, repo, Options{Version: "0.1.18"})
	withProject(t, old)
	histAddMemory(t, old, "", "stamped")
	for name, f := range map[string]func() ([]DoctorFinding, error){
		"shared": func() ([]DoctorFinding, error) { return old.DoctorSharedMachines(background()) },
		"team":   func() ([]DoctorFinding, error) { return old.DoctorTeamVersions(background()) },
	} {
		if got, err := f(); err != nil || len(got) != 0 {
			t.Fatalf("old binary %s: %+v %v", name, got, err)
		}
	}
	home := old.opts.Home
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	a := openWith(t, repo, Options{Home: home, Version: "0.1.18", OrgCommands: &standaloneOrg{home: home}})
	got, err := a.DoctorTeamVersions(background())
	if err != nil || len(got) < 2 || got[0].Message != teamVersionsHeader || !strings.Contains(got[1].Message, ": lore 0.1.18 (last save ") {
		t.Fatalf("cmd/lore team versions: %+v %v", got, err)
	}
	if got, err = a.DoctorSharedMachines(background()); err != nil || len(got) != 0 {
		t.Fatalf("cmd/lore shared machines: %+v %v", got, err)
	}
	names := map[string]bool{}
	for _, r := range a.DoctorChecks(background(), false) {
		names[r.Check] = true
	}
	if !names[string(DoctorCheckSharedMachine)] || !names[string(DoctorCheckTeamVersions)] {
		t.Fatalf("DoctorChecks misses the machine rows: %v", names)
	}
}
