package stores

// Blobless store clones (store growth SG-3b, devco:D-1001): with
// store.clone_filter: blob:none in ~/.lore/config.yaml a new store clone
// holds every commit and tree but only the blobs it checks out; old
// versions are fetched when a read needs them, and offline such a read
// stops with E_OFFLINE. Two commands go with it:
//
//	store.prefetch ─► CloneFilterer.PrefetchStore: fetch every old version
//	                  the clone lacks in one request (before going offline)
//	store.reclone  ─► CloneFilterer.RecloneStore: make the clone again,
//	                  blobless or full (--filter), keeping the old one aside
//
// Both are local to this machine's clone: no row changes, nothing pushed.

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// Operation names of the blobless clone commands.
const (
	OpPrefetch = "store.prefetch"
	OpReclone  = "store.reclone"
)

// Values of store.clone_filter and reclone's --filter.
const (
	FilterBlobless = "blob:none"
	FilterNone     = "none"
)

// CloneFilterer is the optional Manager upgrade behind store.prefetch and
// store.reclone. e is the resolved store (a name's entry, or a url in
// e.URL).
type CloneFilterer interface {
	PrefetchStore(ctx context.Context, e Entry) (PrefetchOut, error)
	RecloneStore(ctx context.Context, e Entry, filter string) (RecloneOut, error)
}

// PrefetchIn is the input of store.prefetch.
type PrefetchIn struct {
	Store string `json:"store" flag:"store" desc:"the store: its name or url"`
}

// PrefetchOut is the result of store.prefetch.
type PrefetchOut struct {
	Store string `json:"store"`
	Clone string `json:"clone"`
	// Blobless is false for a full clone, which holds every version.
	Blobless bool `json:"blobless"`
	// Missing is how many old objects the clone lacked; Fetched how many
	// of them it holds now.
	Missing int `json:"missing"`
	Fetched int `json:"fetched"`
}

// Text renders one line.
func (o PrefetchOut) Text() string {
	if !o.Blobless {
		return fmt.Sprintf("· %s: a full clone (%s), it holds every version already\n", o.Store, o.Clone)
	}
	return fmt.Sprintf("✓ %s: fetched %d old version(s) the blobless clone lacked; every history read now works offline\n", o.Store, o.Fetched)
}

// RecloneIn is the input of store.reclone.
type RecloneIn struct {
	Store  string `json:"store" flag:"store" desc:"the store: its name or url"`
	Filter string `json:"filter,omitempty" flag:"filter" desc:"blob:none (blobless: old versions fetched when needed) or none (every version); default store.clone_filter"`
}

// RecloneOut is the result of store.reclone.
type RecloneOut struct {
	Store string `json:"store"`
	Clone string `json:"clone"`
	// Old is where the previous clone was moved; lore store maintain
	// removes it a week later.
	Old         string `json:"old"`
	Blobless    bool   `json:"blobless"`
	BeforeBytes int64  `json:"before_bytes"`
	AfterBytes  int64  `json:"after_bytes"`
}

// Text renders what was made.
func (o RecloneOut) Text() string {
	kind := "full"
	if o.Blobless {
		kind = "blobless"
	}
	return fmt.Sprintf("✓ %s: made a new %s clone at %s (.git %s → %s); the old one is kept at %s until lore store maintain removes it a week from now\n",
		o.Store, kind, o.Clone, HumanBytes(o.BeforeBytes), HumanBytes(o.AfterBytes), o.Old)
}

// bloblessErrors are the codes of the two commands.
var bloblessErrors = append(append([]errs.Code{}, cloneErrors...), errs.Offline, errs.StoreLocked)

// filterer resolves m's CloneFilterer and the store named by raw.
func filterer(ctx context.Context, m Manager, cmd, raw string) (CloneFilterer, Entry, error) {
	f, ok := upgrade.As[CloneFilterer](m)
	if !ok {
		return nil, Entry{}, errs.New(errs.Unsupported, "store "+cmd+": this build keeps no store clones")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, Entry{}, errs.New(errs.InvalidInput, "store "+cmd+": name the store").
			WithHint("lore store " + cmd + " --store <name>")
	}
	e, named, err := storeNamed(ctx, m, cmd, raw)
	if err != nil {
		return nil, Entry{}, err
	}
	if !named {
		e = Entry{Name: raw, URL: raw}
	}
	return f, e, nil
}

// bloblessOps are store.prefetch and store.reclone bound to m.
func bloblessOps(m Manager) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name:    OpPrefetch,
			Summary: "Fetch every old version a blobless store clone lacks, in one request, so every history read works offline (a full clone has them all)",
			// Read: it adds objects to this machine's clone, never a row or a ref.
			Permission: op.PermRead, Access: op.AccessRead, Errors: bloblessErrors, Local: true,
			Examples: []op.Example{
				{Title: "before going offline", CLI: "lore store prefetch --store notes", Want: ""},
			},
		}, func(ctx context.Context, in PrefetchIn) (PrefetchOut, error) {
			f, e, err := filterer(ctx, m, "prefetch", in.Store)
			if err != nil {
				return PrefetchOut{}, err
			}
			return f.PrefetchStore(ctx, e)
		}),
		op.Define(op.Spec{
			Name:    OpReclone,
			Summary: "Make this machine's clone of a store again, blobless (--filter blob:none) or full (--filter none); the old clone is kept aside for a week",
			// Read: it replaces this machine's copy of what the remote holds;
			// a clone with commits the remote lacks is refused.
			Permission: op.PermRead, Access: op.AccessRead, Errors: bloblessErrors, Local: true,
			Examples: []op.Example{
				{Title: "blobless", CLI: "lore store reclone --store notes --filter blob:none", Want: ""},
				{Title: "full again", CLI: "lore store reclone --store notes --filter none", Want: ""},
			},
		}, func(ctx context.Context, in RecloneIn) (RecloneOut, error) {
			in.Filter = strings.TrimSpace(in.Filter)
			if in.Filter != "" && in.Filter != FilterBlobless && in.Filter != FilterNone {
				return RecloneOut{}, errs.New(errs.InvalidInput, "store reclone: --filter "+in.Filter+" is not blob:none or none").
					WithHint("lore store reclone --store " + strings.TrimSpace(in.Store) + " --filter blob:none")
			}
			f, e, err := filterer(ctx, m, "reclone", in.Store)
			if err != nil {
				return RecloneOut{}, err
			}
			return f.RecloneStore(ctx, e, in.Filter)
		}),
	}
}
