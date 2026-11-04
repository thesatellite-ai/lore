package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorNamesResurrectedRows: a purged row whose live file comes back
// (an old clone) is hidden by reads and named by `lore doctor` with its
// path and the fix; doctor is not healthy while it is there and is again
// once the file is gone.
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
	if r := f.run(e2eBin, "doctor"); strings.Contains(r.stdout+r.stderr, "resurrected") {
		t.Fatalf("control: nothing resurrected yet: %s%s", r.stdout, r.stderr)
	}
	if err := os.WriteFile(filepath.Join(f.dir, rel), live, 0o600); err != nil {
		t.Fatal(err)
	}
	r := f.run(e2eBin, "doctor")
	out := r.stdout + r.stderr
	if r.code == 0 || !strings.Contains(out, "resurrected row .lore/data/memories/"+id+".json") || !strings.Contains(out, "remove the file") {
		t.Fatalf("doctor must name the resurrected file and the fix: exit %d\n%s", r.code, out)
	}
	if err := os.Remove(filepath.Join(f.dir, rel)); err != nil {
		t.Fatal(err)
	}
	if r := f.run(e2eBin, "doctor"); strings.Contains(r.stdout+r.stderr, "resurrected") {
		t.Fatalf("a removed file is no longer reported: %s%s", r.stdout, r.stderr)
	}
}
