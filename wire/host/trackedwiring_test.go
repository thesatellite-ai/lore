package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
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
	meta := `{"format": 2, "migrated_by": "Ada Ray <ada@example.com>", "migrated_at": "2026-01-02T03:04:05Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(data, "_meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestTeammateWriteKeepsWiringLocal (stories A-1): in a migrated project a
// teammate's write (a clone that did not run lore migrate run) adds no line
// to the tracked .gitignore and no tracked .gitattributes: the rules go to
// the clone's own .git/info files, so the git pull of the migrator's commit
// carrying the same lines never aborts. The migrator's clone (marked by the
// run) and an install step still write the tracked files.
func TestTeammateWriteKeepsWiringLocal(t *testing.T) {
	ctx := context.Background()
	write := &Run{Entry: specEntry{spec: op.Spec{Name: "memory.add", Access: op.AccessWrite}}}

	teammate := migratedProject(t)
	if wire.TrackedWiringHere(ctx, teammate) {
		t.Fatal("a clone that did not run the migration owns the tracked wiring")
	}
	if err := GitignoreHook(teammate).Before(ctx, write); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(teammate, gitignoreName)); err == nil {
		t.Fatalf("a teammate's write edited the tracked .gitignore:\n%s", b)
	}
	if b, err := os.ReadFile(filepath.Join(teammate, ".git", "info", "exclude")); err != nil || !strings.Contains(string(b), ".lore/lock") {
		t.Fatalf("a teammate's write left lore's lock in git status (no .git/info/exclude line): %q %v", b, err)
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
		t.Fatal("a teammate's wiring wrote the tracked .gitattributes")
	}
	if !attributesHeldLocally(local) {
		t.Fatal("a teammate's wiring left lore's merge rules out of .git/info/attributes")
	}
	if err := ensureFileBlock(attrs, gitBlockMarker, gitAttributeLines); err != nil { // the migrator's commit arrives
		t.Fatal(err)
	}
	if err := ensureLocalAttributes(attrs, local); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(local); strings.Contains(string(b), gitBlockMarker) {
		t.Fatalf("lore's block stayed in .git/info/attributes after the tracked .gitattributes got it:\n%s", b)
	}

	// The clone that ran lore migrate run writes the tracked lines.
	migrator := migratedProject(t)
	gitT(t, migrator, "config", "--local", wire.MigratedHereGitKey, "true")
	if !wire.TrackedWiringHere(ctx, migrator) {
		t.Fatal("the migrator's clone does not own the tracked wiring")
	}
	if err := GitignoreHook(migrator).Before(ctx, write); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(migrator, gitignoreName)); err != nil || !strings.Contains(string(b), ".lore/lock") {
		t.Fatalf("the migrator's write did not add the .gitignore lines: %q %v", b, err)
	}

	// An install step writes them in a teammate's clone too.
	if _, local := excludeReason(ctx, teammate, false, true); local {
		t.Error("lore doctor --fix in a teammate's clone keeps the .gitignore lines local")
	}
	// A project no migration recorded (made by this lore) keeps the old rule.
	fresh := t.TempDir()
	gitInit(t, fresh)
	if err := os.MkdirAll(filepath.Join(fresh, ".lore", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !wire.TrackedWiringHere(ctx, fresh) {
		t.Error("a project with no recorded migration no longer lets a write add the tracked wiring")
	}
}
