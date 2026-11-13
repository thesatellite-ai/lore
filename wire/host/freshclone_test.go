package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/op"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
)

// TestDoctorHooksFreshCloneIsANote (owner item M-5): in a clone lore never
// set up (no merge driver, no lore hook block) the missing ride-along
// blocks are a note naming what sets them up, so a teammate's fresh clone
// does not read DEGRADED; once lore wired the clone (its merge driver is
// set) the same missing blocks are a warning again (they were removed).
func TestDoctorHooksFreshCloneIsANote(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	ctx := context.Background()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "main")
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &liveSession{repo: repo, top: dir}
	check := func() (warns, notes []string) {
		s.doctorHooks(ctx, "main", false, func(w string) { warns = append(warns, w) }, func(n string) { notes = append(notes, n) })
		return warns, notes
	}
	warns, notes := check()
	if len(warns) != 0 || len(notes) != 1 || !strings.Contains(notes[0], "git hook block missing in post-commit, pre-push") || !strings.Contains(notes[0], "not set up for lore yet") {
		t.Fatalf("fresh clone: warnings %q notes %q, want one note", warns, notes)
	}

	gitT(t, dir, "config", "merge."+mergeDriverLore+".driver", "lore merge-driver %O %A %B %P")
	if warns, _ = check(); len(warns) != 1 || !strings.Contains(warns[0], "run `lore fix` (or `lore sync`)") {
		t.Fatalf("a wired clone whose blocks are gone: warnings %q, want the warning", warns)
	}

	gitT(t, dir, "config", "--unset", "merge."+mergeDriverLore+".driver")
	hooks := filepath.Join(dir, ".git", "hooks")
	if err := os.WriteFile(filepath.Join(hooks, "commit-msg"), []byte("#!/bin/sh\n# >>> "+gitBlockMarker+"\nlore check\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if warns, _ = check(); len(warns) != 1 {
		t.Fatalf("a clone holding a lore hook block was wired: warnings %q, want the warning", warns)
	}
}

// TestMigrateEditsNoTrackedFile (owner item M-6): a migration op in a repo
// holding .lore/data keeps lore's local files out of git status through
// .git/info/exclude, never the tracked .gitignore, and wires git like a
// read (no .gitattributes, no hook block); another write still adds the
// .gitignore lines.
func TestMigrateEditsNoTrackedFile(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{opsmigrate.OpRun, opsmigrate.OpEnable, opsmigrate.OpRollback} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			gitInit(t, root)
			if err := os.MkdirAll(filepath.Join(root, ".lore", "data"), 0o700); err != nil {
				t.Fatal(err)
			}
			run := &Run{Entry: specEntry{spec: op.Spec{Name: name, Access: op.AccessWrite}}}
			if mode := runWireMode(run); mode != wireRead {
				t.Errorf("%s wires git as mode %d, want wireRead", name, mode)
			}
			if err := GitignoreHook(root).Before(ctx, run); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, gitignoreName)); err == nil {
				t.Fatalf("%s wrote the tracked .gitignore", name)
			}
			if b, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude")); err != nil || !strings.Contains(string(b), ".lore/lock") {
				t.Fatalf("%s: lore's files not kept out of git status through .git/info/exclude: %q %v", name, b, err)
			}
		})
	}
	root := t.TempDir()
	gitInit(t, root)
	if err := os.MkdirAll(filepath.Join(root, ".lore", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	write := &Run{Entry: specEntry{spec: op.Spec{Name: "memory.add", Access: op.AccessWrite}}}
	if err := GitignoreHook(root).Before(ctx, write); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(root, gitignoreName)); err != nil || !strings.Contains(string(b), ".lore/lock") {
		t.Fatalf("an ordinary write no longer adds the .gitignore lines: %q %v", b, err)
	}
}

// TestReadsWriteNoExclude (C-8): a read, `lore migrate status` included,
// writes nothing inside .git, not even info/exclude, in a repo whose
// .gitignore lacks lore's lines (a CI clone's checks once appended them):
// doctor names the lines, and a write or a migration adds them.
func TestReadsWriteNoExclude(t *testing.T) {
	ctx := context.Background()
	for _, spec := range []op.Spec{
		{Name: opsmigrate.OpStatus, Permission: op.PermRead, Access: op.AccessRead},
		{Name: "task.list", Permission: op.PermRead, Access: op.AccessRead},
	} {
		t.Run(spec.Name, func(t *testing.T) {
			root := t.TempDir()
			gitInit(t, root)
			if err := os.MkdirAll(filepath.Join(root, ".lore", "data"), 0o700); err != nil {
				t.Fatal(err)
			}
			excl := filepath.Join(root, ".git", "info", "exclude")
			before, _ := os.ReadFile(excl)
			if err := GitignoreHook(root).Before(ctx, &Run{Entry: specEntry{spec: spec}}); err != nil {
				t.Fatal(err)
			}
			if after, _ := os.ReadFile(excl); string(after) != string(before) {
				t.Fatalf("%s wrote .git/info/exclude:\n%s", spec.Name, after)
			}
			if _, err := os.Stat(filepath.Join(root, gitignoreName)); err == nil {
				t.Fatalf("%s wrote the tracked .gitignore", spec.Name)
			}
		})
	}
}
