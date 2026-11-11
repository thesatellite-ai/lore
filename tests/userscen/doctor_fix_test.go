//go:build userscen

package userscen

// `lore doctor --fix` (docs/operations/doctor.md step 4): reversed
// --supersedes links written by an older lore.
//
//   - doctor lists the reversed link as a warning naming `lore doctor
//     --fix` (DEGRADED, nothing written: a dry run)
//   - --fix without a terminal and without --yes: E_NEEDS_CONFIRM, exit
//     26, the question names the link, nothing written
//   - --fix --yes: the link is turned around (the old row records its
//     successor, the new row's backward link cleared), a "repaired" note,
//     HEALTHY
//   - --fix --yes again: nothing left to change, still HEALTHY, files
//     unchanged ("safe to re-run")

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorFix_ReversedSupersedesLink(t *testing.T) {
	p := newDoctorPerson(t)
	oldID := ruleIDRe.FindString(p.c.MustLore("rule", "list").Stdout)
	p.a.SetEnv("LORE_NOW", "2030-01-02T03:04:05Z") // strictly later than the first rule
	newID := ruleIDRe.FindString(p.c.MustLore("rule", "add", "--body", "Run the full suite before you push").Stdout)
	if oldID == "" || newID == "" || oldID == newID {
		t.Fatalf("two rules: %q %q", oldID, newID)
	}
	// An older lore wrote `rule add --supersedes <old>` on the NEW row.
	newFile := ".lore/data/rules/" + newID + ".json"
	oldFile := ".lore/data/rules/" + oldID + ".json"
	body, ok := p.c.ReadFile(newFile)
	if !ok {
		t.Fatal("new rule file")
	}
	p.c.WriteFile(newFile, strings.Replace(body, `"severity"`, `"superseded_by_id": "`+oldID+`",`+"\n  "+`"severity"`, 1))
	p.c.CommitAll("old lore wrote a reversed link")

	link := "rule " + oldID + " → " + newID
	rep, res := p.doctor(t)
	if rep.Status != "degraded" || !hasLine(rep.Warnings, "1 --supersedes link(s) written by lore 0.1.16 or older point from the new row to the old one ("+link+"; each read as old → new, the row that replaces it); run `lore doctor --fix`") {
		t.Fatalf("doctor lists the reversed link: %s", res)
	}
	before, _ := p.c.ReadFile(oldFile)

	ask := p.c.Lore("doctor", "--fix")
	ask.Fails(t, 26, "E_NEEDS_CONFIRM", "Repair 1 --supersedes link(s) written by an older lore ("+link+")?", "`lore doctor --fix --yes` repairs them")
	if after, _ := p.c.ReadFile(oldFile); after != before {
		t.Fatalf("an unconfirmed --fix wrote the old row")
	}

	rep, res = p.doctor(t, "--fix", "--yes")
	if rep.Status != "healthy" || !hasLine(rep.Notes, "repaired 1 --supersedes link(s): "+link) {
		t.Fatalf("--fix --yes repairs the link: %s", res)
	}
	oldBody, _ := p.c.ReadFile(oldFile)
	newBody, _ := p.c.ReadFile(newFile)
	if !strings.Contains(oldBody, `"superseded_by_id": "`+newID+`"`) || strings.Contains(newBody, `"superseded_by_id": "`+oldID+`"`) {
		t.Fatalf("after repair the old row names its successor and the new row's backward link is gone:\nold %s\nnew %s", oldBody, newBody)
	}

	// Twice: nothing left, files unchanged.
	snapshot := map[string]string{}
	files, err := filepath.Glob(p.file(".lore/data/rules/*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, rerr := os.ReadFile(filepath.Clean(f))
		if rerr != nil {
			t.Fatal(rerr)
		}
		snapshot[f] = string(b)
	}
	rep, res = p.doctor(t, "--fix", "--yes")
	if rep.Status != "healthy" || hasLine(rep.Notes, "repaired") || hasLine(rep.Warnings, "--supersedes") {
		t.Fatalf("a second --fix finds nothing left to change: %s", res)
	}
	for f, want := range snapshot {
		b, rerr := os.ReadFile(filepath.Clean(f))
		if rerr != nil || string(b) != want {
			t.Fatalf("a second --fix rewrote %s", f)
		}
	}
	// Plain doctor afterwards: clean.
	if rep, res = p.doctor(t); rep.Status != "healthy" {
		t.Fatalf("doctor after the repair: %s", res)
	}
}

func TestDoctorFix_OnAHealthyRepoChangesNothing(t *testing.T) {
	p := newDoctorPerson(t)
	before := p.c.Status()
	for i := 0; i < 2; i++ {
		rep, res := p.doctor(t, "--fix", "--yes")
		if rep.Status != "healthy" || hasLine(rep.Notes, "repaired") {
			t.Fatalf("run %d: --fix on a healthy repo: %s", i+1, res)
		}
	}
	if after := p.c.Status(); after != before {
		t.Fatalf("--fix on a healthy repo changed the working tree: %q → %q", before, after)
	}
}
