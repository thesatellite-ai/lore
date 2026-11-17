package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCloneRecordStaysInItsHome (devco D-534): the clone record of a lore
// home never sends a copy of that home to the original's clones. A clone
// at its default path is not recorded (set-url back to the default drops
// the entry instead of pinning the absolute path), a path under the home
// is written relative to it, and an absolute entry that is another home's
// default place is left out (Foreign) while a person's own clone stays.
func TestCloneRecordStaysInItsHome(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home-a")
	r := Remote{Norm: "example.com/acme/notes"}
	key := CloneKey(r, "")
	def, err := DefaultCloneDir(home, r, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)

	// a moved clone elsewhere under the home, then set-url back to the default
	if err = RecordClone(home, key, CloneEntry{Path: def + "~2", Why: WhyMoved}, now); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(CloneRecordPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), home) || !strings.Contains(string(b), `"stores/acme/notes~2"`) {
		t.Fatalf("a path under the home must be written relative to it:\n%s", b)
	}
	if got := ReadCloneRecord(home).Clones[key].Path; got != def+"~2" {
		t.Fatalf("relative entry read back as %q, want %q", got, def+"~2")
	}
	if err = RecordClone(home, key, CloneEntry{Path: def, Why: WhyMoved}, now); err != nil {
		t.Fatal(err)
	}
	if e, ok := ReadCloneRecord(home).Clones[key]; ok {
		t.Fatalf("a clone at its default path must not be recorded, got %+v", e)
	}

	// a copy of the home whose record still names home-a's clones by absolute path (an older lore)
	copied := filepath.Join(root, "home-b")
	own := filepath.Join(root, "work", "notes")
	legacy := `{"clones":{"` + key + `":{"path":"` + filepath.ToSlash(def) + `","why":"moved"},"example.com/acme/mine":{"path":"` + filepath.ToSlash(filepath.Join(home, "stores", "acme", "mine")) + `","why":"own"},"example.com/acme/other":{"path":"` + filepath.ToSlash(own) + `","why":"own"}}}`
	if err = os.MkdirAll(filepath.Dir(CloneRecordPath(copied)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(CloneRecordPath(copied), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := ReadCloneRecord(copied)
	if _, ok := rec.Clones[key]; ok || len(rec.Foreign) != 1 || rec.Foreign[0].Key != key {
		t.Fatalf("another home's default clone must be left out and listed as foreign: %+v", rec)
	}
	if rec.Clones["example.com/acme/other"].Path != own || rec.Clones["example.com/acme/mine"].Path == "" {
		t.Fatalf("a person's own clones stay: %+v", rec.Clones)
	}
	dir, _, err := CloneDirFor(copied, r, "")
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := DefaultCloneDir(copied, r, ""); dir != want {
		t.Fatalf("the copied home must use its own clone %s, got %s", want, dir)
	}
	found, err := DropForeignClones(copied)
	if err != nil || len(found) != 1 {
		t.Fatalf("drop: %v %+v", err, found)
	}
	if b, _ = os.ReadFile(CloneRecordPath(copied)); strings.Contains(string(b), filepath.ToSlash(def)) {
		t.Fatalf("the dropped entry must be gone from the file:\n%s", b)
	}
}
