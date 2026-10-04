package lsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSamplePaths(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry()
	got := SamplePaths(reg)
	if len(got) != len(reg.Names())+2 || got[0] != MetaFileName || got[1] != PurgedFileName {
		t.Fatalf("want both control files then one per table: %v", got)
	}
	seen := map[string]bool{}
	for _, p := range got[2:] {
		table := strings.SplitN(p, "/", 2)[0]
		if _, ok := reg.Table(table); !ok || seen[table] {
			t.Fatalf("unexpected or repeated table folder %q", p)
		}
		seen[table] = true
		// The sample name must never be mistaken for a real row.
		if _, _, ok := parseRelPath(p); ok {
			t.Fatalf("%s parses as a row file", p)
		}
	}
}

// Finder and Explorer drop clutter into any folder someone browses; it must
// never produce a warning or an error, at the top or inside a table folder.
func TestScanSkipsOSJunkSilently(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.addMemory("x")
	e.reconcile()
	for _, rel := range []string{".DS_Store", "memories/.DS_Store", "memories/._mem", "Thumbs.db", "rules/desktop.ini"} {
		p := filepath.Join(e.data, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rep := e.reconcile()
	if len(rep.Warnings) != 0 || len(rep.Errors) != 0 || rep.Changed() {
		t.Fatalf("OS clutter must be invisible: %+v", rep)
	}
	for _, name := range []string{".DS_Store", "Thumbs.db", "desktop.ini", "._x"} {
		if !IsOSJunk(name) {
			t.Fatalf("%s must be junk", name)
		}
	}
	for _, name := range []string{MetaFileName, PurgedFileName, "memories", "mem_1.json", ".tmp-x"} {
		if IsOSJunk(name) {
			t.Fatalf("%s is not junk", name)
		}
	}
}
