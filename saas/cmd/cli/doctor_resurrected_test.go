package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorNamesResurrectedRows: a purged row whose live file comes back
// (an old clone) is hidden by reads and named by `lore doctor` with its
// path and the fix EXACTLY ONCE (two identical checks once printed it
// twice); doctor is not healthy while it is there and is again once the
// file is gone.
// resurrectedMark is the words of doctor's resurrected-row finding.
const resurrectedMark = "is a live copy of a purged row"

func TestDoctorNamesResurrectedRows(t *testing.T) {
	f := newCoreFixture(t)
	id := f.addID("memory", "add", "--body", "an old note to purge")
	rel := filepath.Join(".lore", "data", "memories", id+".json")
	live, err := os.ReadFile(filepath.Join(f.dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	f.lore("memory", "archive", id)
	f.lore("sync", "purge", "--archived-before", "0d", "--confirm")
	if r := f.run(e2eBin, "doctor"); strings.Contains(r.stdout+r.stderr, resurrectedMark) {
		t.Fatalf("control: nothing resurrected yet: %s%s", r.stdout, r.stderr)
	}
	if err := os.WriteFile(filepath.Join(f.dir, rel), live, 0o600); err != nil {
		t.Fatal(err)
	}
	r := f.run(e2eBin, "doctor")
	out := r.stdout + r.stderr
	line := "store repo: .lore/data/memories/" + id + ".json " + resurrectedMark
	if r.code == 0 || strings.Count(out, line) != 1 || !strings.Contains(out, "remove the file") {
		t.Fatalf("doctor must name the resurrected file once, with the fix: exit %d, %d time(s)\n%s", r.code, strings.Count(out, line), out)
	}
	if err := os.Remove(filepath.Join(f.dir, rel)); err != nil {
		t.Fatal(err)
	}
	if r := f.run(e2eBin, "doctor"); strings.Contains(r.stdout+r.stderr, resurrectedMark) {
		t.Fatalf("a removed file is no longer reported: %s%s", r.stdout, r.stderr)
	}
}
