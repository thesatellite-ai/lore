package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/canonjson"

	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/gitremote"
	"saas/pkg/aicoder/live"
)

// A store branch's live session (lore-stores' seam): rows go to
// refs/lore/live/<branch> of the bare clone and land on THAT branch, never
// on main (TEAM-011).
func TestStoreLiveSessionLandsOnTheStoreBranch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	root := t.TempDir()
	origin := filepath.Join(root, "store.git")
	seed := filepath.Join(root, "seed")
	gitT(t, root, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, root, "init", "-q", "-b", "staging", seed)
	gitT(t, seed, "commit", "-q", "--allow-empty", "-m", "store")
	gitT(t, seed, "push", "-q", origin, "staging")
	clone := filepath.Join(root, "clone.git")
	gitT(t, root, "clone", "-q", "--bare", origin, clone)
	repo, err := gitref.Open(ctx, clone, nil)
	if err != nil {
		t.Fatal(err)
	}
	remote := &gitremote.Git{Repo: repo, Name: "origin"}
	s := newStoreLiveSession(ctx, repo, remote, "origin", filepath.Join(clone, "lore"), "staging", gitref.Signature{Name: "t", Email: "t@t"})
	if s.ref.Name != "refs/lore/live/staging" || s.defaultBranch(ctx) != "staging" {
		t.Fatalf("ref %s, land branch %s", s.ref.Name, s.defaultBranch(ctx))
	}
	body := canonjson.Object{"_v": canonjson.String("1"), "_table": canonjson.String("rules"), "id": canonjson.String("rul_1"), "body": canonjson.String("never deploy on Friday")}
	if _, err := s.ref.Write(ctx, live.WriteRequest{Folder: "acme", BinaryVersion: version,
		Rows: map[live.RowKey]live.File{{Table: "rules", ID: "rul_1"}: live.NewVersion(nil, body, s.clk.Now(), "act_1")}}); err != nil {
		t.Fatal(err)
	}
	s.wrote = 1
	// Every production caller (coord_store.go, stores_project.go) ends a
	// store session with withStoreWrite: the F16 leak check guards client
	// repos and is skipped for the org's own store. Without the mark the
	// check ran against the test's cwd and refused the landing.
	s.finishStore(withStoreWrite(ctx), "acme")
	got := gitT(t, origin, "show", "staging:acme/.lore/data/rules/rul_1.json")
	if !strings.Contains(got, "never deploy on Friday") || strings.Contains(got, "_live") {
		t.Fatalf("the rule lands on the store branch without its block: %s", got)
	}
	if out := gitT(t, origin, "for-each-ref", "--format=%(refname)", "refs/heads/main", "refs/lore/live/staging"); !strings.Contains(out, "refs/lore/live/staging") || strings.Contains(out, "refs/heads/main") {
		t.Fatalf("live on refs/lore/live/staging; main never created: %s", out)
	}
}
