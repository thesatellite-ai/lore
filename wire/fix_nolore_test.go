package wire

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
)

// TestFixLeavesRepoWithoutLoreAlone (FP-7): in a git repo with no .lore/,
// lore fix (dry or not) refuses with "no lore data" before any step, and
// doctor and doctor --fix run as a read (the host's WithNoCreate): none of
// them creates anything in the repo, .lore/ or any other folder or file.
func TestFixLeavesRepoWithoutLoreAlone(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	gitInitIn(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := treeHash(t, repo)
	a, err := New(Options{RepoDir: repo, Home: t.TempDir(), FlagActor: "human:pat@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ctx := context.Background()

	if !a.FixNoLoreData(ctx) {
		t.Fatal("a git repo with no .lore has lore data")
	}
	for _, in := range []opsfix.RunIn{{}, {DryRun: true}} {
		// a plain ctx: the gate alone keeps the run from the open that
		// creates .lore/ (the host adds WithNoCreate on top)
		_, err := opsfix.Run(ctx, a, in)
		sameTree(t, "lore fix", repo, before)
		if !errs.Has(err, errs.NotAStore) {
			t.Fatalf("lore fix %+v in a repo with no lore data: %v", in, err)
		}
	}
	for _, in := range []opsdoctor.RunIn{{}, {Fix: true}} {
		_, _ = opsdoctor.Run(WithNoCreate(ctx), a, in) // its verdict is not the point: the tree is
		sameTree(t, "lore doctor", repo, before)
	}
}

// sameTree fails when dir's tree differs from before.
func sameTree(t *testing.T, cmd, dir string, before map[string]string) {
	t.Helper()
	after := treeHash(t, dir)
	for p, h := range after {
		if before[p] != h {
			t.Fatalf("%s changed a repo with no lore data: %s is new or changed", cmd, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			t.Fatalf("%s changed a repo with no lore data: %s is gone", cmd, p)
		}
	}
}

// treeHash maps every folder and file under dir (outside .git's objects,
// refs and logs, which git rewrites itself) to its mode and content hash.
func treeHash(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, _ := filepath.Rel(dir, p)
		switch filepath.ToSlash(rel) {
		case ".git/objects", ".git/refs", ".git/logs":
			return filepath.SkipDir
		case ".git/index":
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		h := fi.Mode().String()
		if fi.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			h += " " + hex.EncodeToString(sum[:])
		}
		out[rel] = h
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
