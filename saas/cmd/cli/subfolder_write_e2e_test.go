package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2E_CoreWriteFromSubfolder (cl-wire, coreAuditSink.projectRoot): a
// core write run from a folder deep inside the repo (repo/sub/deeper)
// stores exactly one row file in the repo's .lore/data and one audit
// entry in the repo's lore.db chain; nothing (.lore, lore.db) is created
// in the subfolders.
func TestE2E_CoreWriteFromSubfolder(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	deep := filepath.Join(c.dir, "sub", "deeper")
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	r := w.run(deep, c.env, e2eBin, "memory", "add", "--body", "written from a subfolder")
	if r.code != 0 {
		t.Fatalf("memory add from sub/deeper: %d %s %s", r.code, r.stdout, r.stderr)
	}
	id := idPattern.FindString(r.stdout)
	if !strings.HasPrefix(id, "mem_") {
		t.Fatalf("no memory id in %q", r.stdout)
	}
	files, err := filepath.Glob(filepath.Join(c.dir, ".lore", "data", "memories", "*.json"))
	if err != nil || len(files) != 1 || filepath.Base(files[0]) != id+".json" {
		t.Fatalf("repo memory files = %v (%v), want one %s.json", files, err, id)
	}
	for _, d := range []string{filepath.Join(c.dir, "sub"), deep} {
		if _, serr := os.Stat(filepath.Join(d, ".lore")); !os.IsNotExist(serr) {
			t.Fatalf("a .lore folder was created in %s: %v", d, serr)
		}
	}
	log := c.lore("audit", "log", "--json").stdout
	if n := strings.Count(log, id); n < 1 {
		t.Fatalf("the subfolder write is not in the repo's audit log: %s", log)
	}
	if n := strings.Count(log, `"memories.write"`); n != 1 {
		t.Fatalf("%d memories.write audit entries, want 1: %s", n, log)
	}
}
