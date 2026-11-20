package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	"github.com/thesatellite-ai/lore/wire"
)

// migratedProject makes a git repo at a temp folder whose .lore/data/_meta.json
// records a finished migration by someone (format 2, migrated_by), as the
// migrator's commit brings it to every teammate's clone.
func migratedProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	data := filepath.Join(root, ".lore", "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := `{"format": 2, "migrated_by": "Ada Ray", "migrated_at": "2026-01-02T03:04:05Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(data, "_meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// plainWrite is an ordinary write's run (lore memory add).
func plainWrite() *Run {
	return &Run{Entry: specEntry{spec: op.Spec{Name: "memory.add", Access: op.AccessWrite}}}
}

// doctorFix is `lore doctor --fix` as the REST body gives it: an install
// step (runWireMode wireInstall).
func doctorFix() *Run {
	return &Run{Entry: specEntry{spec: op.Spec{Name: opsdoctor.OpRun, Access: op.AccessWrite}}, Decoder: op.JSONDecoder{Body: []byte(`{"fix": true}`)}}
}

// TestPlainWriteKeepsWiringLocal (devco:D-753, stories A-1): no plain write
// adds a line to the tracked .gitignore, in the clone that ran lore migrate
// run (H-12: its next `lore mission add` edited .gitignore right after the
// migration said only `lore doctor --fix` does), a teammate's clone, or a
// project no migration recorded; the rules go to the clone's own
// .git/info/exclude. An install step (lore doctor --fix) writes the
// tracked file in any clone.
func TestPlainWriteKeepsWiringLocal(t *testing.T) {
	ctx := context.Background()
	migrator := migratedProject(t)
	gitT(t, migrator, "config", "--local", wire.MigratedHereGitKey, "true")
	fresh := t.TempDir()
	gitInit(t, fresh)
	if err := os.MkdirAll(filepath.Join(fresh, ".lore", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, root := range map[string]string{"migrator": migrator, "teammate": migratedProject(t), "no migration": fresh} {
		if mode := runWireMode(plainWrite()); mode == wireInstall {
			t.Fatalf("%s: a plain write wires as an install step", name)
		}
		if err := GitignoreHook(root).Before(ctx, plainWrite()); err != nil {
			t.Fatal(err)
		}
		if b, err := os.ReadFile(filepath.Join(root, gitignoreName)); err == nil {
			t.Errorf("%s: a plain write edited the tracked .gitignore:\n%s", name, b)
		}
		if b, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude")); err != nil || !strings.Contains(string(b), ".lore/lock") {
			t.Errorf("%s: a plain write left lore's lock in git status (no .git/info/exclude line): %q %v", name, b, err)
		}
		if why, local := excludeReason(ctx, root, false, false, nil); !local || why != excludeWrite {
			t.Errorf("%s: excludeReason for a plain write = %v %v, want excludeWrite", name, why, local)
		}
		if _, local := excludeReason(ctx, root, false, true, nil); local {
			t.Errorf("%s: lore doctor --fix keeps the .gitignore lines local", name)
		}
	}
	if mode := runWireMode(doctorFix()); mode != wireInstall {
		t.Fatalf("lore doctor --fix wires as mode %d, want wireInstall", mode)
	}
	teammate := migratedProject(t)
	if err := GitignoreHook(teammate).Before(ctx, doctorFix()); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(teammate, gitignoreName)); err != nil || !strings.Contains(string(b), ".lore/lock") {
		t.Fatalf("lore doctor --fix did not add the .gitignore lines: %q %v", b, err)
	}

	// The .gitattributes rules: in the clone's own attributes file while
	// the tracked file lacks them, dropped there once it holds them (git
	// reads info/attributes first, so a stale block would outrank it).
	attrs := filepath.Join(teammate, gitAttributesFile)
	local := filepath.Join(teammate, ".git", "info", "attributes")
	if err := ensureLocalAttributes(attrs, local); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(attrs); err == nil {
		t.Fatal("the local wiring wrote the tracked .gitattributes")
	}
	if !attributesHeldLocally(local) {
		t.Fatal("the local wiring left lore's merge rules out of .git/info/attributes")
	}
	if err := ensureFileBlock(attrs, gitBlockMarker, gitAttributeLines); err != nil { // an install step's commit arrives
		t.Fatal(err)
	}
	if err := ensureLocalAttributes(attrs, local); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(local); strings.Contains(string(b), gitBlockMarker) {
		t.Fatalf("lore's block stayed in .git/info/attributes after the tracked .gitattributes got it:\n%s", b)
	}
}

// TestIgnoredDataByWriteNamesTheStep (devco:D-753): a plain write in a repo
// whose .gitignore hides row files leaves the file alone and names the
// install step that re-includes them.
func TestIgnoredDataByWriteNamesTheStep(t *testing.T) {
	t.Parallel()
	got := ignoredDataByWrite([]string{".lore/data/memories/a.json"})
	for _, want := range []string{"changes no tracked file outside .lore/", "`lore doctor --fix` re-includes", ".lore/data/memories/a.json"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the warning %q lacks %q", got, want)
		}
	}
}
