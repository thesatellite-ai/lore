package engine

import (
	"context"
	"maps"
	"strings"

	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// Provenance supplies the facts the engine cannot know itself for a row's
// `_o` and `_w` (PROVENANCE_DESIGN 5.2, 6.2): where the process runs and
// which lore on which machine writes. wire implements it (wire/origin.go)
// from the work dir, the repo's git HEAD, the repo id and the machine id.
// Without one (WithProvenance unset) `_o` holds only the command and the
// store, and no `_w` is written.
type Provenance interface {
	// Place returns the facts of the checkout the command runs in: repo
	// id, Dir (PROV6) and Git. Cmd and Store are the engine's to fill.
	Place(ctx context.Context) origin.Facts
	// Upstream reports whether branch has an upstream on one of the
	// repo's remotes (PROV8: a client repo keeps a branch name only then).
	Upstream(ctx context.Context, branch string) bool
	// Writer returns the lore version and this machine's id for `_w`
	// (PROV4); either may be empty / zero when unknown.
	Writer() (version string, machine ids.MachineID)
}

// WithProvenance sets the provenance facts source (see Provenance).
func WithProvenance(p Provenance) Option {
	return func(e *Engine) error { e.prov = p; return nil }
}

// originOf builds the _o block written once at a create into st.
func (e *Engine) originOf(ctx context.Context, opName string, st Attached) (value.JSON, error) {
	var f origin.Facts
	if e.prov != nil {
		f = e.prov.Place(ctx)
	}
	if imp, ok := importOf(ctx); ok {
		f.Import = imp // XQ9 / PROV9: the importer's source and external id
	}
	f.Cmd = strings.ReplaceAll(opName, ".", " ")
	f.Store = string(st.Name)
	o, err := origin.Build(f)
	if err != nil || !st.Client {
		return o, err
	}
	// PROVENANCE_DESIGN 8.1 / PROV8: a row created in a client repo gets
	// the reduced block at write time; it was born here, so dir and git
	// (minus a local-only branch) are the client's own facts.
	return origin.ClientFilter(o, e.clientOf(ctx, st, f.Repo, true))
}

// movedIntoClient returns the `_o` a row moved into the client repo st
// carries: only the keys of its own `_o` the client may see (8.1), no
// `moves` entries. PROV7: nothing of the dropped keys is copied anywhere
// (no sidecar); the full block stays in the source place's history and
// the audit log.
func (e *Engine) movedIntoClient(ctx context.Context, st Attached, o value.JSON) (value.JSON, error) {
	var here string
	if e.prov != nil {
		here = e.prov.Place(ctx).Repo
	}
	born := here != "" && originRepo(o) == here
	got, err := origin.ClientFilter(o, e.clientOf(ctx, st, here, born))
	if err != nil {
		return value.JSON{}, err
	}
	// The client file still says it lives in this repo store: store is the
	// repo store's own name, which names nothing internal, and the v0.1.17
	// leak check (leakcheck originIsRepo) reads `_o.store == repo` to accept
	// a 0.1.18 row as belonging in the client repo.
	return withStore(got, string(st.Name))
}

// clientOf is the 8.1 test for the client repo st.
func (e *Engine) clientOf(ctx context.Context, st Attached, repoID string, born bool) origin.Client {
	c := origin.Client{OwnStore: string(st.Name), RepoID: repoID, BornHere: born}
	if e.prov != nil {
		c.Upstream = func(b string) bool { return e.prov.Upstream(ctx, b) }
	}
	return c
}

// originRepo reads `_o.repo` ("" when absent).
func originRepo(o value.JSON) string {
	return origin.Text(o, origin.KeyRepo)
}

// withStore sets `_o.store`.
func withStore(o value.JSON, name string) (value.JSON, error) {
	return origin.With(o, origin.KeyStore, name)
}

// stampWrite sets r's `_w` for a content-changing save into st (OQ7,
// PROVENANCE_DESIGN 6.2: every save that changes content; client repos
// get `{v}` only, and so does every code repo: Q-R3-9). A tombstone carries no stamp. r.Reserved is copied
// before the change so the caller's row is not mutated.
func (e *Engine) stampWrite(st Attached, r row.Row) (row.Row, error) {
	if e.prov == nil || r.Deleted {
		return r, nil
	}
	v, m := e.prov.Writer()
	// Q-R3-9 (coordinator, PROV4): no machine id in any code repo — the
	// engine cannot tell our own repo from a client's own repo, and a code
	// repo is where another org may read; machine ids stay in stores. So
	// the repo store (and any client repo) gets `{v}` only.
	w, err := origin.Stamp(v, m, st.Client || st.Name == store.SourceRepo)
	if err != nil {
		return r, err
	}
	r.Reserved = maps.Clone(r.Reserved)
	if w.IsZero() {
		delete(r.Reserved, origin.KeyWrite)
		return r, nil
	}
	if r.Reserved == nil {
		r.Reserved = map[string]value.Value{}
	}
	r.Reserved[origin.KeyWrite] = w
	return r, nil
}
