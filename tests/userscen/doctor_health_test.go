//go:build userscen

package userscen

// Doctor on a healthy project, the way a person or a monitoring script
// reads it (docs/operations/doctor.md steps 1-3):
//
//   - text: Status line, Binary line, [db], [sync], [identity] blocks
//   - --json: schema_version 1, ok, status, exit_status, the [sync] keys a
//     binary without lore.db fills, no lore.db-only keys
//   - text and JSON agree on the status
//   - uncommitted files are counted, never a problem
//   - outside any git repo doctor still answers (no [sync])
//   - running it twice changes nothing (it only reads)

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDoctorHealthy_TextJSONAndExit(t *testing.T) {
	p := newDoctorPerson(t)

	text := p.c.MustLore("doctor")
	for _, want := range []string{"Status: HEALTHY\n", "Binary: ", "[db]\n  ok:       true", "[sync]\n  in git repo:      true",
		"  open conflicts:   0\n", "  file errors:      0\n", "  merge driver:     true\n", "  data gitignored:  false\n",
		"  uncommitted:      0\n", "[identity]\n  resolved: human:person@example.com", "  stable:   yes"} {
		if !strings.Contains(text.Stdout, want) {
			t.Fatalf("healthy doctor text lacks %q:\n%s", want, text)
		}
	}
	for _, never := range []string{"Warnings:", "Errors:", "baselined", "pending exports"} {
		if strings.Contains(text.Stdout, never) {
			t.Fatalf("healthy doctor text shows %q:\n%s", never, text)
		}
	}

	rep, res := p.doctor(t)
	if rep.Status != "healthy" || !rep.DBOK || rep.BinaryVersion == "" || len(rep.Warnings) != 0 || len(rep.Errors) != 0 {
		t.Fatalf("healthy report: %s", res)
	}
	if rep.Identity.Resolved != "human:person@example.com" || !rep.Identity.Stable || rep.Identity.Source == "" {
		t.Fatalf("identity of a person with a git email: %+v", rep.Identity)
	}
	s := rep.Sync
	if s == nil || !s.DataDirPresent || !s.InGitRepo || !s.MergeDriverOK || s.DataIgnored || s.TrackedFiles == 0 ||
		s.OpenConflicts != 0 || s.Uncommitted != 0 || len(s.FileErrors) != 0 {
		t.Fatalf("healthy [sync]: %s", res)
	}
	var raw struct {
		Sync map[string]json.RawMessage `json:"sync"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"baselined", "pending_exports", "trash_rows", "errors", "dangling_references", "duplicate_groups"} {
		if _, ok := raw.Sync[k]; ok {
			t.Fatalf("[sync] shows lore.db key %q this binary has no source for: %s", k, res.Stdout)
		}
	}

	// Twice: doctor only reads, so the second report is the same and the
	// working tree did not change.
	before := p.c.Status()
	rep2, _ := p.doctor(t)
	if rep2.Status != rep.Status || len(rep2.Warnings) != 0 || p.c.Status() != before {
		t.Fatalf("a second doctor run changed something: %+v, git status %q → %q", rep2, before, p.c.Status())
	}
}

func TestDoctorHealthy_UncommittedIsInformationOnly(t *testing.T) {
	p := newDoctorPerson(t)
	p.c.MustLore("memory", "add", "--body", "an uncommitted note")
	rep, res := p.doctor(t)
	if rep.Status != "healthy" || rep.Sync == nil || rep.Sync.Uncommitted == 0 {
		t.Fatalf("uncommitted row files are counted, not a problem: %s", res)
	}
	text := p.c.MustLore("doctor")
	if !strings.Contains(text.Stdout, "Status: HEALTHY") || strings.Contains(text.Stdout, "  uncommitted:      0\n") {
		t.Fatalf("text shows the uncommitted count and stays HEALTHY:\n%s", text)
	}
}

func TestDoctorHealthy_OutsideAProject(t *testing.T) {
	p := newDoctorPerson(t)
	dir := p.w.Root + "/plain-folder"
	res := p.a.Lore(dir, "doctor", "--json")
	rep := doctorDecode(t, res)
	if rep.Sync != nil {
		t.Fatalf("no [sync] outside a git repo: %s", res)
	}
	// Like a git repo without lore (one "skipped" note), a folder in no
	// repo is not a degraded machine: the repo store checks are skipped
	// with one note, never one "could not run" warning per check.
	if rep.Status != "healthy" || len(rep.Warnings) != 0 ||
		!hasLine(rep.Notes, "not inside a git repo, so the repo store checks were skipped") {
		t.Fatalf("doctor outside a project on a clean machine is healthy with one note: %s", res)
	}
	// A git repo with no lore project: one note, nothing created.
	gitOnly := p.w.Root + "/git-only"
	p.a.Lore(gitOnly, "version").OK(t)
	p.w.Git(gitOnly, "init", "-q")
	res = p.a.Lore(gitOnly, "doctor", "--json")
	rep = doctorDecode(t, res)
	if rep.Status != "healthy" || !hasLine(rep.Notes, "no lore project in this repo, so the store checks were skipped") {
		t.Fatalf("a git repo without lore: %s", res)
	}
	if _, err := os.Stat(gitOnly + "/.lore"); err == nil {
		t.Fatalf("doctor created .lore/ in a repo lore is not set up in")
	}
}

func TestDoctorHealthy_HelpNamesExitContract(t *testing.T) {
	p := newDoctorPerson(t)
	h := p.c.MustLore("doctor", "--help")
	for _, want := range []string{"exit 0 healthy, 1 degraded, 2 broken", "--fix", "--json", "--all", "--fresh"} {
		if !strings.Contains(h.Stdout, want) {
			t.Fatalf("doctor --help lacks %q:\n%s", want, h)
		}
	}
	// An unknown flag is a usage error, not a report.
	res := p.c.Lore("doctor", "--no-such-flag")
	if res.Code == 0 || !strings.Contains(res.Out(), "--no-such-flag") {
		t.Fatalf("unknown flag: %s", res)
	}
}
