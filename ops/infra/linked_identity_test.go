package infra_test

import (
	"context"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// linkedCheckout is the App of a checkout an org link names
// (infra.RepoLinkIdentity answers true), the way wire answers in a repo
// linked to a store folder.
type linkedCheckout struct{ *wire.App }

func (linkedCheckout) RepoIdentityInLink(context.Context) bool { return true }

// TestLinkedRepoIdentityWritesNoReposRow (devco:D-736, owner item 29: a
// linked repo's .lore/data holds no rows): in a linked checkout the first
// sync mints the repo id into _meta.json without a repos row, a new remote
// is named on every pass without writing one, and `lore repo set-url`
// keeps the confirmed address in _meta.json alone. An unlinked checkout
// still gets its repos row.
func TestLinkedRepoIdentityWritesNoReposRow(t *testing.T) {
	h := checkout(t, "git@github.com:agency/acmehub_backend.git")
	ctx := context.Background()
	env := linkedCheckout{App: h.App}
	reposRows := func() int {
		t.Helper()
		rows, err := h.App.List(ctx, store.SourceRepo, schema.TableRepos)
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}

	rep, err := infra.CheckAddresses(ctx, env, env)
	if err != nil || !rep.Minted {
		t.Fatalf("first sync: %+v %v", rep, err)
	}
	m := meta(t, h)
	if m.RepoID != rep.RepoID || !slices.Equal(m.URLs, []string{"github.com/agency/acmehub_backend"}) {
		t.Fatalf("_meta.json after the first sync: %+v", m)
	}
	if n := reposRows(); n != 0 {
		t.Fatalf("the first sync of a linked repo wrote %d repos rows into it, want 0", n)
	}

	setOrigin(t, h.Repo, "git@github.com:agency/acme-hub-api.git")
	for pass := 1; pass <= 2; pass++ {
		r, cerr := infra.CheckAddresses(ctx, env, env)
		if cerr != nil || r.Minted || r.RepoID != m.RepoID || len(r.Notices) != 1 {
			t.Fatalf("sync %d after the remote changed: %+v %v (want the same id and one notice)", pass, r, cerr)
		}
		if n := reposRows(); n != 0 {
			t.Fatalf("sync %d wrote %d repos rows into the linked repo", pass, n)
		}
	}

	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	entries := infra.Ops(env)
	i := slices.IndexFunc(entries, func(e op.Entry) bool { return e.Spec().Name == infra.OpRepoSetURL })
	if i < 0 {
		t.Fatal("no repo.set_url entry")
	}
	if _, rerr := h.App.Run(ctx, entries[i], op.JSONDecoder{Body: []byte(`{"url":"git@github.com:agency/acme-hub-api.git"}`)}, op.Caller{Actor: actor, Yes: true}); rerr != nil {
		t.Fatalf("repo set-url in a linked repo: %v", rerr)
	}
	if got := meta(t, h).URLs; len(got) == 0 || got[0] != "github.com/agency/acme-hub-api" {
		t.Fatalf("_meta.json after set-url: %v (want the new primary first)", got)
	}
	if n := reposRows(); n != 0 {
		t.Fatalf("set-url wrote %d repos rows into the linked repo", n)
	}
	if r, cerr := infra.CheckAddresses(ctx, env, env); cerr != nil || len(r.Notices) != 0 {
		t.Fatalf("sync after set-url: %+v %v (want no notice: the address is confirmed)", r, cerr)
	}

	// an unlinked checkout keeps today's repos row
	plain := checkout(t, "git@github.com:agency/other_backend.git")
	if _, err := plain.App.CheckRepoAddresses(ctx); err != nil {
		t.Fatal(err)
	}
	if rows, err := plain.App.List(ctx, store.SourceRepo, schema.TableRepos); err != nil || len(rows) != 1 {
		t.Fatalf("an unlinked checkout: %d repos rows (%v), want 1", len(rows), err)
	}
}
