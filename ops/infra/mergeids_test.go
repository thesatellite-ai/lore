package infra_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/infra"
)

// TestRepoMergeIDs pins PROVENANCE 11.9: rows stamped with an alias id get
// the winner after a confirmed run; --dry-run and an unconfirmed run write
// nothing; the aliases are cleared.
func TestRepoMergeIDs(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := checkout(t, "git@github.com:agency/acmehub_backend.git")
	ctx := context.Background()
	if _, err := h.App.CheckRepoAddresses(ctx); err != nil {
		t.Fatal(err)
	}
	loser := meta(t, h).RepoID
	mem := h.ID(h.Must("memory.add", `{"body":"stamped with the loser"}`))
	m := meta(t, h)
	m.RepoID = must(ids.New[ids.Repo]()).String()
	m.Aliases = []string{loser}
	if err := rowfs.WriteRepoMeta(h.Repo, m); err != nil {
		t.Fatal(err)
	}
	dry := h.Must(infra.OpRepoMergeIDs, `{"dry_run":true}`)
	if !strings.Contains(h.JSON(dry), mem) {
		t.Fatalf("dry run does not list %s: %s", mem, h.JSON(dry))
	}
	h.Fails(infra.OpRepoMergeIDs, `{}`, errs.NeedsConfirm)
	if got := meta(t, h).Aliases; len(got) != 1 {
		t.Fatalf("unconfirmed run cleared aliases: %v", got)
	}
	h.MustYes(infra.OpRepoMergeIDs, `{}`)
	if got := meta(t, h).Aliases; len(got) != 0 {
		t.Fatalf("aliases kept: %v", got)
	}
	_, r, err := h.App.Get(ctx, engine.Ref{ID: must(ids.ParseAny(mem))})
	if err != nil {
		t.Fatal(err)
	}
	if o := r.Origin.String(); !strings.Contains(o, `"repo":"`+m.RepoID+`"`) || strings.Contains(o, loser) {
		t.Fatalf("_o not rewritten to the winner: %s", o)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
