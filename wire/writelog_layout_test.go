package wire

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCurrentRowFileAfterLayoutMove (B4-10): a row a command wrote into a
// store's old layout and that the same command then moved is named where
// it is now, never at the old path that no longer exists.
func TestCurrentRowFileAfterLayoutMove(t *testing.T) {
	clone := t.TempDir()
	put := func(rel string) string {
		p := filepath.Join(clone, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := func(rel string) string { return filepath.Join(clone, filepath.FromSlash(rel)) }
	moved := put("org/actor_periods/apd_1.json")
	ownRow := put("_store/actors/act_1.json")
	personal := put("_store/memories/mem_1.json")
	kept := put("web/.lore/data/rules/rul_1.json")
	for _, c := range []struct{ written, want string }{
		{old("org/.lore/data/actor_periods/apd_1.json"), moved}, // the layout commit
		{old(".lore/data/actors/act_1.json"), ownRow},           // the store's own rows
		{old("acme/.lore/data/memories/mem_1.json"), personal},  // 0.1.17's per-org personal note
		{kept, kept}, // still there: unchanged
		{old("gone/.lore/data/rules/rul_2.json"), old("gone/.lore/data/rules/rul_2.json")}, // nowhere: as written
		{"", ""},
	} {
		if got := currentRowFile(c.written); got != c.want {
			t.Errorf("currentRowFile(%s) = %s, want %s", c.written, got, c.want)
		}
	}
	log := &WriteLog{seen: map[Written]bool{}}
	log.add(Written{Store: "acme/org", File: old("org/.lore/data/actor_periods/apd_1.json")})
	for _, l := range WrittenLines(log.Files()) {
		if strings.Contains(l, ".lore/data") {
			t.Errorf("the write line names the old layout: %s", l)
		}
	}
}

// TestOldLoreLockoutLines: the block names the store once, in its first
// line, and says what a 0.1.17 user sees (E_NOT_A_STORE, not E_OLD_LORE).
func TestOldLoreLockoutLines(t *testing.T) {
	all := strings.Join(OldLoreLockoutLines, "\n")
	if strings.Count(OldLoreLockoutLines[0], "%s") != 1 || strings.Count(all, "%s") != 1 {
		t.Fatalf("the store name goes in the first line, once: %q", OldLoreLockoutLines)
	}
	for _, want := range []string{"must upgrade lore", "E_NOT_A_STORE", "git commits"} {
		if !strings.Contains(all, want) {
			t.Errorf("the block does not say %q", want)
		}
	}
	if strings.Contains(all, "E_OLD_LORE") {
		t.Error("lore 0.1.17 never says E_OLD_LORE for a store")
	}
}

// TestOldLoreLockoutOncePerMachine (B4-21): the block is said before the
// first commit 0.1.17 cannot read and never again for that store on this
// machine, also from a later command (a new process: the record lives in
// the clone's .git/lore).
func TestOldLoreLockoutOncePerMachine(t *testing.T) {
	clone := t.TempDir()
	if err := os.MkdirAll(filepath.Join(clone, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	say := func() string {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		old := os.Stderr
		os.Stderr = w
		(&App{}).SayOldLoreLockout("lore-me", clone)
		os.Stderr = old
		_ = w.Close()
		b, _ := io.ReadAll(r)
		lockoutSaid.Delete(clone) // the next call stands for a later command
		return string(b)
	}
	if got := say(); !strings.Contains(got, "store lore-me moves to the 0.1.18 layout now") {
		t.Fatalf("first call said %q", got)
	}
	if got := say(); got != "" {
		t.Fatalf("a later command said the block again: %q", got)
	}
}
