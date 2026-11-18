package engine

// narrow.go — a command run in a code repo whose --store names a store
// the repo attaches works on that store alone (devco D-617). Before it the
// row was found in any attached store: `resource kind edit --id code_repos
// --store acme/web` in a repo attaching acme/web and acme/org found the
// kind of acme/org and wrote it there. The caller (wire) names the stores
// --store covers with WithNarrow; Run then
//
//   - refuses a row named by id (the input's id arguments) that lives in
//     another attached store, read or write, before anything runs; and
//   - refuses the run's first row write when it lands outside those
//     stores: an operation that finds its row by name writes that row
//     first, so the refusal comes before anything is written. Later
//     writes of the same run (a rename relabelling the rows that use the
//     name) follow the row, as before.
//
//	--store <name> ─▶ id argument in another store ─▶ E_NOT_FOUND "not in <name>; it is in <other>"
//	              └▶ first write outside <name>   ─▶ the same, nothing written

import (
	"context"
	"slices"
	"sync"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// Narrow is what a command's --store covers inside a repo: the name typed
// and the attached stores it means. Shown names a store attached at a
// folder as <store>/<folder> in the refusal; a store missing there is
// shown by its own name.
type Narrow struct {
	Named   string
	Sources []store.Source
	Shown   map[store.Source]string
}

// narrowState is a Narrow in a run's context with the run's first-write
// decision: once a write landed in a named store, the run's later writes
// are not checked.
type narrowState struct {
	Narrow
	mu      sync.Mutex
	decided bool
}

// narrowKey keys the *narrowState in a context.
type narrowKey struct{}

// WithNarrow returns ctx under which Run keeps the command to n's stores
// (see the file comment). An n with no Sources leaves ctx as it is.
func WithNarrow(ctx context.Context, n Narrow) context.Context {
	if len(n.Sources) == 0 {
		return ctx
	}
	return context.WithValue(ctx, narrowKey{}, &narrowState{Narrow: n})
}

// narrowOf is ctx's narrowing, nil when the command named no store.
func narrowOf(ctx context.Context) *narrowState {
	n, _ := ctx.Value(narrowKey{}).(*narrowState)
	return n
}

// shown is how the refusal names src.
func (n *narrowState) shown(src store.Source) string {
	if s := n.Shown[src]; s != "" {
		return s
	}
	return string(src)
}

// elsewhere is D-617's refusal for the row id found in src.
func (n *narrowState) elsewhere(id string, src store.Source) error {
	other := n.shown(src)
	return errs.New(errs.NotFound, id+" is not in "+n.Named+"; it is in "+other+" (use --store "+other+"), so nothing was done").
		WithHint("run the same command with --store " + other + ", or without --store to find the row in every store this repo attaches")
}

// refuseNarrowedIDs is the by-id half: every id argument of the input that
// names a live row must be in a named store. The lookup searches the named
// stores first (locateRaw), so a row with copies in several stores (the
// caller's actor) passes when one copy is there. An id naming no row is
// left to the operation (its own not-found).
func (e *Engine) refuseNarrowedIDs(ctx context.Context, args []string) error {
	n := narrowOf(ctx)
	if n == nil {
		return nil
	}
	for _, a := range args {
		id, err := ids.ParseAny(a)
		if err != nil {
			continue
		}
		def, ok := e.reg.ByPrefix(id.Prefix())
		if !ok || e.IsMachineLocal(def.Name) {
			continue
		}
		src, r, gerr := e.Get(ctx, Ref{ID: id})
		if gerr != nil || r.Deleted || slices.Contains(n.Sources, src) {
			continue
		}
		return n.elsewhere(id.String(), src)
	}
	return nil
}

// refuseNarrowedWrite is the first-write half: the run's first change to
// a store (lore's own upkeep and the machine store aside) must land in a
// named store; later changes of the run are not checked.
func refuseNarrowedWrite(ctx context.Context, st Attached, changes []store.Change) error {
	n := narrowOf(ctx)
	if n == nil || st.Name == store.SourceMachine {
		return nil
	}
	i := slices.IndexFunc(changes, func(c store.Change) bool { return !c.Upkeep })
	if i < 0 {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.decided {
		return nil
	}
	if !slices.Contains(n.Sources, st.Name) {
		return n.elsewhere(string(changes[i].Row.Table)+" "+changes[i].Row.ID.String(), st.Name)
	}
	n.decided = true
	return nil
}
