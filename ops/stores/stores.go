// Package stores defines the `lore store` operations (layer L5): make a
// 0.1.18 store (init), attach one to this repo (add), detach it (remove)
// and list what this repo sees (list); STORE_SERVER_SPEC 6-8, STORES
// DIRECT, STORE TARGETS, CFG2, CFG4, CFG7 and the owner note on
// `lore store init me [url]`.
//
// Where things are written (spec 8): an attached store is an entry of the
// committed <repo>/.lore/stores.yaml; the personal store `me` is the
// `personal.store` line of ~/.lore/config.yaml, never a committed file; a
// new store gets a lore-store.yaml marker (rowfs) with a permanent sto_ id.
//
// Sync is NOT a store verb (owner O12b): `lore sync` exchanges every
// attached store; SyncAll is the shared code it calls.
//
// The operations only validate and call Manager; the composition root
// (wire) owns the files, the backends and the network.
package stores

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names.
const (
	OpInit   = "store.init"
	OpAdd    = "store.add"
	OpRemove = "store.remove"
	OpList   = "store.list"
)

// PersonalName is the reserved name of the personal store (`--to me`).
const PersonalName = attach.ReservedMe

// Visibility values of lore-store.yaml (CFG4); the backend validates too.
const (
	VisibilityInternal  = "internal"
	VisibilityShareable = "shareable"
)

// Manager does the work; wire implements it.
type Manager interface {
	// InitStore makes url a 0.1.18 store (marker with a new sto_ id; a git
	// store gets one commit, pushed) unless it already is one, then records
	// it: `me` in config.yaml, any other name in stores.yaml. An empty url
	// is allowed only for `me` (a folder store at <home>/stores/me).
	InitStore(ctx context.Context, in InitIn) (InitOut, error)
	// AddStore attaches an existing store to this repo (stores.yaml).
	AddStore(ctx context.Context, in AddIn) (Entry, error)
	// RemoveStore detaches a store from this repo (stores.yaml).
	RemoveStore(ctx context.Context, name string) (Entry, error)
	// ListStores lists the stores this repo sees, in precedence order
	// (stores.yaml order, the personal store last; BATCH 15).
	ListStores(ctx context.Context) ([]Entry, error)
}

// Entry is one store as this repo sees it. Url never carries credentials
// (stores.yaml refuses them).
type Entry struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Folder   string `json:"folder,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
	// Branch is the pinned store branch (empty = the default branch).
	Branch string `json:"branch,omitempty"`
	// Personal marks `me` (from ~/.lore/config.yaml, not stores.yaml).
	Personal bool `json:"personal,omitempty"`
}

// InitIn is the input of store.init.
type InitIn struct {
	Name       string `json:"name" arg:"0" desc:"the store's name in this repo (me = your personal store)"`
	URL        string `json:"url,omitempty" arg:"1" desc:"git url or file:// path (optional for me: default file://~/.lore/stores/me)"`
	Visibility string `json:"visibility,omitempty" flag:"visibility" desc:"internal (default) or shareable (CFG4)"`
}

// InitOut is the output of store.init.
type InitOut struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Personal bool   `json:"personal,omitempty"`
	ID       string `json:"id"`
	// Already is true when the url was already a 0.1.18 store.
	Already bool `json:"already,omitempty"`
}

// AddIn is the input of store.add.
type AddIn struct {
	Name     string `json:"name" arg:"0" desc:"the store's name in this repo (snake_case; not repo, me, team, company)"`
	URL      string `json:"url" arg:"1" desc:"git url (git@host:owner/repo, ssh://, https://) or file:// path"`
	Folder   string `json:"folder,omitempty" flag:"folder" desc:"attach only this folder of the store"`
	ReadOnly bool   `json:"read_only,omitempty" flag:"read-only" desc:"refuse every write to this store"`
	Branch   string `json:"branch,omitempty" flag:"branch" desc:"attach this store branch (e.g. staging) instead of the default one"`
}

// RemoveIn is the input of store.remove.
type RemoveIn struct {
	Name string `json:"name" arg:"0" desc:"the store to detach from this repo"`
}

// ListIn is the (empty) input of store.list.
type ListIn struct{}

// ListOut is the output of store.list.
type ListOut struct {
	Stores []Entry `json:"stores"`
	Count  int     `json:"count"`
}

// CheckName refuses a store name that is not snake_case or is reserved
// (CFG2 as narrowed by STORE TARGETS), with E_INVALID_INPUT. allowMe lets
// `me` through (store init me).
func CheckName(name string, allowMe bool) error {
	if allowMe && name == PersonalName {
		return nil
	}
	if !row.ValidName(name) {
		return errs.New(errs.InvalidInput, "store: the name "+quote(name)+" must be snake_case")
	}
	if slices.Contains(attach.Reserved(), name) {
		return errs.New(errs.InvalidInput, "store: "+quote(name)+" is reserved ("+strings.Join(attach.Reserved(), ", ")+")").
			WithHint("pick another name; `me` is made with `lore store init me`")
	}
	return nil
}

// quote wraps s in backticks for messages.
func quote(s string) string { return "`" + s + "`" }

// storeErrors are the codes the store operations return.
var storeErrors = []errs.Code{errs.InvalidInput, errs.NotFound, errs.ConfigInvalid, errs.StoreScheme, errs.URLUserinfo,
	errs.NotAStore, errs.StoreUnreachable, errs.OldLore, errs.NeedsConfirm, errs.Internal}

// MissingInitURL is the refusal of `lore store init <name>` without a url.
// BATCH2 #9: every store but `me` needs a url (me keeps its default,
// file://~/.lore/stores/me, per O7), and the error names exactly what is
// missing and the usage (owner rule: exact errors), exit 20.
func MissingInitURL(name string) error {
	return errs.New(errs.InvalidInput, "store init "+name+": the store url is missing; usage: lore store init <name> <url> (only `"+PersonalName+"` has a default url)").
		WithHint("e.g. lore store init " + name + " git@github.com:<owner>/<repo>, or a file:///path/to/folder")
}

// Ops returns the store operations bound to m.
func Ops(m Manager) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpInit, Summary: "Make a git repo or folder a lore store and attach it (me = your personal store)",
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: storeErrors,
			// CFG7: attaching in a client repo writes the url into a file
			// the client reads, so it asks first (the composition root).
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "your personal store, a local folder", CLI: "lore store init me", Want: "id:"},
				{Title: "a team store on git", CLI: "lore store init agency git@github.com:agency/notes", Want: "id:"},
			},
		}, func(ctx context.Context, in InitIn) (InitOut, error) {
			if err := CheckName(in.Name, true); err != nil {
				return InitOut{}, err
			}
			if in.URL == "" && in.Name != PersonalName {
				return InitOut{}, MissingInitURL(in.Name)
			}
			if in.Visibility != "" && in.Visibility != VisibilityInternal && in.Visibility != VisibilityShareable {
				return InitOut{}, errs.New(errs.InvalidInput, "store init: --visibility must be internal or shareable")
			}
			return m.InitStore(ctx, in)
		}),
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Attach an existing lore store to this repo (.lore/stores.yaml)",
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: storeErrors,
			Confirm: op.ConfirmRule{MayAsk: true}, // CFG7, as for init
			Examples: []op.Example{
				{Title: "attach a store", CLI: "lore store add golang git@github.com:mira/lore-golang", Want: "name:"},
				{Title: "attach one folder, read-only", CLI: "lore store add tips git@github.com:agency/notes --folder tips --read-only", Want: "name:"},
			},
		}, func(ctx context.Context, in AddIn) (Entry, error) {
			if err := CheckName(in.Name, false); err != nil {
				return Entry{}, err
			}
			if in.URL == "" {
				return Entry{}, errs.New(errs.InvalidInput, "store add: a url is required")
			}
			return m.AddStore(ctx, in)
		}),
		op.Define(op.Spec{
			Name: OpRemove, Summary: "Detach a store from this repo (its rows stay in the store)",
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: storeErrors,
			Examples: []op.Example{{Title: "detach", CLI: "lore store remove golang", Want: "name:"}},
		}, func(ctx context.Context, in RemoveIn) (Entry, error) {
			if in.Name == PersonalName {
				return Entry{}, errs.New(errs.InvalidInput, "store remove: `me` is not attached by this repo").
					WithHint("it is personal.store in ~/.lore/config.yaml; edit that line to change it")
			}
			if err := CheckName(in.Name, false); err != nil {
				return Entry{}, err
			}
			return m.RemoveStore(ctx, in.Name)
		}),
		op.Define(op.Spec{
			Name: OpList, Summary: "List the stores this repo sees, in precedence order",
			Permission: op.PermRead, Access: op.AccessRead, Errors: storeErrors,
			Examples: []op.Example{{Title: "list", CLI: "lore store list", Want: "stores:"}},
		}, func(ctx context.Context, _ ListIn) (ListOut, error) {
			got, err := m.ListStores(ctx)
			if err != nil {
				return ListOut{}, err
			}
			if got == nil {
				got = []Entry{}
			}
			return ListOut{Stores: got, Count: len(got)}, nil
		}),
	}
}

// Syncer is what SyncAll needs; the engine provides both methods.
type Syncer interface {
	// Stores lists the attached stores in precedence order.
	Stores() []store.Source
	// Sync exchanges one store's changes with its remote and refreshes its
	// cache (engine.Engine.Sync).
	Sync(ctx context.Context, source store.Source) (store.SyncReport, error)
}

// SyncOut is what `lore sync` reports for the 0.1.18 stores.
type SyncOut struct {
	Stores []Report `json:"stores"`
}

// Report is one store's sync result.
type Report struct {
	Store    string `json:"store"`
	Sent     int    `json:"sent"`
	Received int    `json:"received"`
	// Merged counts rows merged field by field because both sides changed.
	Merged int `json:"merged"`
	// Refused counts rows the sync could not apply (each is listed in
	// Problems with its code); nothing was lost, the local side is kept.
	Refused  int      `json:"refused"`
	Problems []string `json:"problems,omitempty"`
	Offline  bool     `json:"offline,omitempty"`
}

// SyncAll syncs every attached store except the repo store (the person
// pushes their code), or only the store named only. Refused rows end in
// E_STALE so scripts see a non-zero exit; both sides are kept.
func SyncAll(ctx context.Context, s Syncer, only string) (SyncOut, error) {
	names := s.Stores()
	if only != "" {
		if !slices.Contains(names, store.Source(only)) {
			return SyncOut{}, errs.New(errs.NotFound, "sync: no store named "+quote(only)+" is attached").
				WithHint("run `lore store list`")
		}
		names = []store.Source{store.Source(only)}
	}
	out := SyncOut{Stores: []Report{}}
	var problems []string
	for _, n := range names {
		if n == store.SourceRepo {
			continue
		}
		rep, err := s.Sync(ctx, n)
		if err != nil {
			return out, errs.Wrap(errs.CodeOf(err), "sync: "+string(n), err)
		}
		r := Report{Store: string(n), Sent: rep.Sent, Received: rep.Received, Merged: len(rep.Merged),
			Refused: len(rep.Refused), Offline: rep.Offline}
		for _, x := range rep.Refused {
			p := string(x.Code) + " " + x.ID.String() + ": " + x.Reason
			r.Problems = append(r.Problems, p)
			problems = append(problems, string(n)+": "+p)
		}
		out.Stores = append(out.Stores, r)
	}
	if len(problems) > 0 {
		return out, errs.New(errs.Stale, "sync: "+strings.Join(problems, "; ")).
			WithHint("both edits are kept; settle the row on one side (lore memory show <id> on each), then sync again")
	}
	return out, nil
}

// WritesStoresFile reports whether the operation name records a store in
// the repo's committed stores.yaml (init of a store other than `me`, add):
// in a client repo that puts the store url in a file the client reads, so
// the composition root asks first (CFG7).
func WritesStoresFile(name string) bool { return name == OpInit || name == OpAdd }

// IsStoreOp reports whether name is one of the store operations: they edit
// configuration files, not rows, so the composition root runs them without
// opening the attached stores (one unreachable store must not block
// `lore store remove`).
func IsStoreOp(name string) bool {
	return slices.Contains([]string{OpInit, OpAdd, OpRemove, OpList}, name)
}
