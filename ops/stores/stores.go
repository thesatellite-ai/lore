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

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names.
const (
	OpInit   = "store.init"
	OpAdd    = "store.add"
	OpRemove = "store.remove"
	OpList   = "store.list"
	// OpVisibility changes the visibility a store declares (CFG4).
	OpVisibility = "store.visibility"
	// OpFolderAdd creates a folder in a store on purpose (STORE TARGETS).
	OpFolderAdd = "store.folder.add"
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
	// StoreVisibility reads the visibility the store name declares in its
	// lore-store.yaml (CFG4); it refuses a read-only attachment.
	StoreVisibility(ctx context.Context, name string) (VisibilityOut, error)
	// SetVisibility writes in.Visibility into the store's lore-store.yaml
	// (a git store gets one commit, pushed). The op has already asked.
	SetVisibility(ctx context.Context, in VisibilityIn) (VisibilityOut, error)
	// AddFolder creates <folder>/.lore/data/_meta.json in the store (a git
	// store gets one commit, pushed); an existing folder is left as it is.
	AddFolder(ctx context.Context, in FolderAddIn) (FolderAddOut, error)
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
	// Visibility and Reach are filled by store.list only (StatusReader).
	Visibility string `json:"visibility,omitempty"`
	Reach      Reach  `json:"reach,omitempty"`
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

// VisibilityIn is the input of store.visibility.
type VisibilityIn struct {
	Name       string `json:"name" arg:"0" desc:"the store (a stores.yaml name or me)"`
	Visibility string `json:"visibility" arg:"1" desc:"internal or shareable (CFG4)"`
}

// VisibilityOut is the output of store.visibility.
type VisibilityOut struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	// Previous is the visibility before the change.
	Previous string `json:"previous"`
	// Changed is false when the store already declared it.
	Changed bool `json:"changed"`
	// Repos are set when a shareable store goes back to internal: repos on
	// this machine that attach it, whose committed files (LORE.md, pointer
	// files) may already hold its rows (RepoUser). Re-render them.
	Repos []string `json:"repos_holding_rows,omitempty"`
	// Note says why Repos could not be read (the change itself is done).
	Note string `json:"note,omitempty"`
}

// FolderAddIn is the input of store.folder.add.
type FolderAddIn struct {
	Store  string `json:"store" arg:"0" desc:"the store (a stores.yaml name, me or team)"`
	Folder string `json:"folder" arg:"1" desc:"the new folder's name (snake_case; not org)"`
}

// FolderAddOut is the output of store.folder.add.
type FolderAddOut struct {
	Store  string `json:"store"`
	Folder string `json:"folder"`
	// ProjectID is the folder's permanent prj_ id (its _meta.json).
	ProjectID string `json:"project_id"`
	// Already is true when the store already had the folder.
	Already bool `json:"already,omitempty"`
}

// CheckFolderName refuses a folder name store.folder.add cannot create:
// not snake_case, or `org`, the org folder's name (CLI_REFERENCE 3.21).
func CheckFolderName(name string) error {
	if !row.ValidName(name) {
		return errs.New(errs.InvalidInput, "store folder add: the folder name "+quote(name)+" must be snake_case")
	}
	if name == rowfs.FolderOrg {
		return errs.New(errs.InvalidInput, "store folder add: "+quote(name)+" is the org folder's name").
			WithHint("write to the company folder with --to company (or --to <store>/org)")
	}
	return nil
}

// shareableQuestion is what store.visibility asks before `shareable`.
const shareableQuestion = "rows of this store will render into committed files and show in client repos; make it shareable?"

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

// visibilityErrors are the codes of store.visibility and store.folder.add.
var visibilityErrors = []errs.Code{errs.InvalidInput, errs.NotFound, errs.ConfigInvalid, errs.StoreReadOnly,
	errs.NotAStore, errs.StoreUnreachable, errs.StoreScheme, errs.OldLore, errs.NeedsConfirm, errs.Cancelled, errs.Internal}

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
	// The v0.1.17 clone commands (show, open, forget, doctor) come last (clones.go).
	return append([]op.Entry{
		op.Define(op.Spec{
			Name: OpInit, Summary: "Make a git repo or folder a lore store and attach it (me = your personal store)",
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: storeErrors,
			// CFG7: attaching in a client repo writes the url into a file
			// the client reads, so it asks first (the composition root).
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "your personal store, a local folder", CLI: "lore store init me", Want: "sto_"},
				{Title: "a team store on git", CLI: "lore store init agency git@github.com:agency/notes", Want: "sto_"},
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
			if in.URL != "" {
				if err := CheckScheme(in.URL); err != nil {
					return InitOut{}, err
				}
			}
			return m.InitStore(ctx, in)
		}),
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Attach an existing lore store to this repo (.lore/stores.yaml)",
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: storeErrors,
			Confirm: op.ConfirmRule{MayAsk: true}, // CFG7, as for init
			Examples: []op.Example{
				{Title: "attach a store", CLI: "lore store add golang git@github.com:mira/lore-golang", Want: "attached store golang"},
				{Title: "attach one folder, read-only", CLI: "lore store add tips git@github.com:agency/notes --folder tips --read-only", Want: "attached store tips"},
			},
		}, func(ctx context.Context, in AddIn) (AddOut, error) {
			if err := CheckName(in.Name, false); err != nil {
				return AddOut{}, err
			}
			if in.URL == "" {
				return AddOut{}, errs.New(errs.InvalidInput, "store add: a url is required")
			}
			if err := CheckScheme(in.URL); err != nil {
				return AddOut{}, err
			}
			e, err := m.AddStore(ctx, in)
			if err != nil {
				return AddOut{}, err
			}
			out := AddOut{Name: e.Name, URL: e.URL, Folder: e.Folder, ReadOnly: e.ReadOnly, Branch: e.Branch}
			if sr, ok := upgrade.As[StatusReader](m); ok {
				out.Declared = sr.StoreStatus(ctx, e).Visibility
			}
			return out, nil
		}),
		op.Define(op.Spec{
			Name: OpRemove, Summary: "Detach a store from this repo (its rows stay in the store); asks first",
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: storeErrors,
			// CLI_REFERENCE 3.21: remove asks first (E_NEEDS_CONFIRM, then
			// --yes), after the name is known to be attached: an unknown
			// name is E_NOT_FOUND without a question (exact errors).
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "detach", CLI: "lore store remove golang --yes", Want: "detached store golang"}},
		}, func(ctx context.Context, in RemoveIn) (RemoveOut, error) {
			if in.Name == PersonalName {
				return RemoveOut{}, errs.New(errs.InvalidInput, "store remove: `me` is not attached by this repo").
					WithHint("it is personal.store in ~/.lore/config.yaml; edit that line to change it")
			}
			if err := CheckName(in.Name, false); err != nil {
				return RemoveOut{}, err
			}
			if !engine.Confirmed(ctx) {
				list, err := m.ListStores(ctx)
				if err != nil {
					return RemoveOut{}, err
				}
				i := slices.IndexFunc(list, func(e Entry) bool { return e.Name == in.Name && !e.Personal })
				if i < 0 {
					return RemoveOut{}, errs.New(errs.NotFound, "store remove: no store named "+in.Name+" in "+storesFileShown).
						WithHint("run `lore store list`")
				}
				q := "detach store " + in.Name + " (" + list[i].URL + ") from this repo? its rows stay in the store"
				return RemoveOut{}, errs.New(errs.NeedsConfirm, "store remove "+in.Name+": "+q).
					WithQuestion(q).WithHint("ask the person, then rerun with --yes")
			}
			e, err := m.RemoveStore(ctx, in.Name)
			return RemoveOut(attachedOf(e)), err
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
			if sr, ok := upgrade.As[StatusReader](m); ok {
				for i := range got {
					st := sr.StoreStatus(ctx, got[i])
					got[i].Visibility, got[i].Reach = st.Visibility, st.Reach
				}
			}
			return ListOut{Stores: got, Count: len(got)}, nil
		}),
		// Flow (CFG4): validate → read the current visibility → same: done
		// → shareable and not yet confirmed: E_NEEDS_CONFIRM (going back
		// to internal never asks) → Manager writes and pushes the marker.
		op.Define(op.Spec{
			Name: OpVisibility, Summary: "Set whether a store's rows may reach client repos and committed files (internal or shareable)",
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: visibilityErrors,
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "make a store shareable", CLI: "lore store visibility docs_kit shareable --yes", Want: "docs_kit is shareable"}},
		}, func(ctx context.Context, in VisibilityIn) (VisibilityOut, error) {
			if err := CheckName(in.Name, true); err != nil {
				return VisibilityOut{}, err
			}
			if in.Visibility != VisibilityInternal && in.Visibility != VisibilityShareable {
				return VisibilityOut{}, errs.New(errs.InvalidInput, "store visibility: the visibility must be internal or shareable, not "+quote(in.Visibility))
			}
			cur, err := m.StoreVisibility(ctx, in.Name)
			if err != nil {
				return VisibilityOut{}, err
			}
			if cur.Visibility == in.Visibility {
				return VisibilityOut{Name: in.Name, Visibility: in.Visibility, Previous: cur.Visibility}, nil
			}
			// CFG4: shareable asks first; internal (the safe way) never does.
			if in.Visibility == VisibilityShareable && !engine.Confirmed(ctx) {
				return VisibilityOut{}, errs.New(errs.NeedsConfirm, "store visibility "+in.Name+": "+shareableQuestion).
					WithQuestion("make store " + in.Name + " shareable? " + shareableQuestion).
					WithHint("ask the person, then rerun with --yes")
			}
			out, err := m.SetVisibility(ctx, in)
			if err != nil || in.Visibility != VisibilityInternal || cur.Visibility != VisibilityShareable {
				return out, err
			}
			if ru, ok := upgrade.As[RepoUser](m); ok {
				if out.Repos, err = ru.ReposUsingStore(ctx, in.Name); err != nil {
					out.Repos, out.Note = nil, "the repos that attach this store could not be listed: "+err.Error()
				}
			}
			return out, nil
		}),
		// Flow (STORE TARGETS): validate names → Manager finds the store
		// (stores.yaml, me, team), refuses read-only, writes the folder's
		// _meta.json (natural prj_ id) and pushes. On purpose only, so it
		// does not ask; a first `--to <store>/<folder>` write does.
		op.Define(op.Spec{
			Name: OpFolderAdd, Summary: "Create a folder in a store (shown only in repos that attach it)",
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: visibilityErrors,
			Examples: []op.Example{{Title: "a folder for one topic", CLI: "lore store folder add golang concurrency", Want: "created folder concurrency"}},
		}, func(ctx context.Context, in FolderAddIn) (FolderAddOut, error) {
			if in.Store != attach.ReservedTeam {
				if err := CheckName(in.Store, true); err != nil {
					return FolderAddOut{}, err
				}
			}
			if err := CheckFolderName(in.Folder); err != nil {
				return FolderAddOut{}, err
			}
			return m.AddFolder(ctx, in)
		}),
		setURLOp(m),
	}, cloneOps(m)...)
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
// the repo's committed stores.yaml (init of a store other than `me`, add,
// set-url):
// in a client repo that puts the store url in a file the client reads, so
// the composition root asks first (CFG7).
func WritesStoresFile(name string) bool { return name == OpInit || name == OpAdd || name == OpSetURL }

// IsStoreOp reports whether name is one of the store operations: they edit
// configuration files, not rows, so the composition root runs them without
// opening the attached stores (one unreachable store must not block
// `lore store remove`).
func IsStoreOp(name string) bool {
	return slices.Contains([]string{OpInit, OpAdd, OpRemove, OpList, OpVisibility, OpFolderAdd, OpSetURL}, name)
}
