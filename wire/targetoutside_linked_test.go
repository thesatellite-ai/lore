package wire

import (
	"context"
	"os/exec"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// TestOutsideRepoLeavesALinkedRepoAlone: a write naming its store in a git
// repo with no .lore/ runs on that store alone (F-15), except in a repo a
// store folder links by its remote. That repo's own open attaches the
// stores its link and its org name (the org roster an --assign @team needs,
// a folder of a second org store named alone), so the write takes the
// usual path there; on the store-only open it failed "no store named
// <org>/org is attached".
func TestOutsideRepoLeavesALinkedRepoAlone(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	write := func(a *App) bool {
		t.Helper()
		e, ok := a.Registry().Lookup(opsmemory.OpAdd)
		if !ok {
			t.Fatalf("no op %s", opsmemory.OpAdd)
		}
		_, handled, _ := a.runOutsideRepo(ctx, e, op.JSONDecoder{Body: []byte(`{"body":"a note","to":"acme"}`)}, op.Caller{Yes: true})
		return handled
	}

	if !write(newTestApp(t, repo)) {
		t.Fatal("a write --to acme in a git repo lore is not set up in did not run on the store alone (F-15)")
	}
	linked := newTestAppWith(t, repo, func(o *Options) {
		o.RepoLinkedByRemote = func(context.Context) string { return "github.com/acme/billing" }
	})
	if write(linked) {
		t.Fatal("a write --to acme in a repo linked by its remote ran on the store alone; it must take the repo's open")
	}
}
