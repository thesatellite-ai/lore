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
	"github.com/thesatellite-ai/lore/model/ids"
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

// Where a listed store comes from (Entry.Source).
const (
	// SourceStoresFile is the repo's committed .lore/stores.yaml.
	SourceStoresFile = "stores.yaml"
	// SourceOrg is the repo's org (its link or patterns in ~/.lore/config.yaml).
	SourceOrg = "org"
	// SourcePersonal is personal.store in ~/.lore/config.yaml.
	SourcePersonal = "personal"
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
	// Source says where the entry comes from (store.list only, CLI_REFERENCE
	// 3.21 "each named by its source"): SourceStoresFile, SourceOrg or
	// SourcePersonal.
	Source string `json:"source,omitempty"`
	// Visibility and Reach are filled by store.list only (StatusReader).
	Visibility string `json:"visibility,omitempty"`
	Reach      Reach  `json:"reach,omitempty"`
	// Address marks a store this machine reaches at more than one address
	// (SOURCE_OF_TRUTH row 7; store.list only, AddressNoter): "" when it
	// has one.
	Address string `json:"address,omitempty"`
}

// InitIn is the input of store.init.
type InitIn struct {
	Name       string `json:"name" arg:"0" desc:"the store's name in this repo (me = your personal store)"`
	URL        string `json:"url,omitempty" arg:"1" desc:"git url or file:// path (optional for me: default file://~/.lore/stores/me)"`
	Visibility string `json:"visibility,omitempty" flag:"visibility" desc:"internal (default) or shareable (CFG4)"`
	// RepoOptional (callers only, never the CLI): run outside a repo too.
	// The setup page's Start a team step makes the team store from wherever
	// `lore setup` runs; the org entry in config.yaml is what points at it,
	// and .lore/stores.yaml gets the store only when a repo is here.
	RepoOptional bool `json:"-"`
}

// InitOut is the output of store.init.
type InitOut struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Personal bool   `json:"personal,omitempty"`
	ID       string `json:"id"`
	// Already is true when the url was already a 0.1.18 store.
	Already bool `json:"already,omitempty"`
	// Following names the setups that were on the default local folder
	// ~/.lore/stores/me and now follow personal.store (store init me with
	// another url): a person has one personal store `me`.
	Following []string `json:"following,omitempty"`
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
	Name string `json:"name" arg:"0" desc:"the store to detach from this repo (with its folder entries), or one folder entry <store>/<folder>"`
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
	Name       string `json:"name" arg:"0" desc:"the store (team, me, an org's name, an org store's url, or a .lore/stores.yaml name)"`
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
	Store  string `json:"store" arg:"0" desc:"the store (team, me, an org's name, an org store's url, or a .lore/stores.yaml name)"`
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
				return InitOut{}, errs.New(errs.InvalidInput, "store init: --visibility must be internal or shareable, not "+quote(in.Visibility)).
					WithHint("internal (the default) or shareable")
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
			// A folder entry is named <store>/<folder> (the name --to and
			// store list use); only its store part is a store name.
			base, _, isFolder := strings.Cut(in.Name, folderSep)
			if err := CheckName(base, false); err != nil {
				return RemoveOut{}, err
			}
			list, err := m.ListStores(ctx)
			if err != nil {
				return RemoveOut{}, err
			}
			i := slices.IndexFunc(list, func(e Entry) bool { return e.Name == in.Name && !e.Personal })
			if i < 0 {
				return RemoveOut{}, errs.New(errs.NotFound, "store remove: no store named "+in.Name+" in "+storesFileShown).
					WithHint("run `lore store list`")
			}
			// BUGFIX-01: detaching a store detaches its folder entries
			// too; left behind, they kept the store's rows in this repo.
			var folders []string
			if !isFolder {
				for _, e := range list {
					if strings.HasPrefix(e.Name, in.Name+folderSep) && !e.Personal {
						folders = append(folders, e.Name)
					}
				}
			}
			if !engine.Confirmed(ctx) {
				q := "detach store " + in.Name + " (" + list[i].URL + ") from this repo? its rows stay in the store"
				if isFolder {
					q = "detach folder entry " + in.Name + " (" + list[i].URL + ") from this repo? its rows stay in the store"
				} else if len(folders) > 0 {
					q = "detach store " + in.Name + " (" + list[i].URL + ") and its folder entries " + strings.Join(folders, ", ") + " from this repo? their rows stay in the store"
				}
				return RemoveOut{}, errs.New(errs.NeedsConfirm, "store remove "+in.Name+": "+q).
					WithQuestion(q).WithHint("ask the person, then rerun with --yes")
			}
			e, err := m.RemoveStore(ctx, in.Name)
			if err != nil {
				return RemoveOut{}, err
			}
			out := RemoveOut{Name: e.Name, URL: e.URL, Folder: e.Folder, ReadOnly: e.ReadOnly, Branch: e.Branch}
			for _, f := range folders {
				if _, err = m.RemoveStore(ctx, f); err != nil {
					return RemoveOut{}, errs.Prefix("store remove "+in.Name+": detached the store; folder entry "+f+" is still attached", err).
						WithHint("rerun `lore store remove " + f + " --yes`")
				}
				out.Folders = append(out.Folders, f)
			}
			return out, nil
		}),
		op.Define(op.Spec{
			Name: OpList, Summary: "List the stores this repo sees, in precedence order (outside a repo: the stores of the setup)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: storeErrors,
			Examples: []op.Example{
				{Title: "list", CLI: "lore store list", Want: "stores:"},
				{Title: "outside any repo: the org's and the personal store of the setup this folder is in", CLI: "lore store list --setup agency", Want: "stores:"},
			},
		}, func(ctx context.Context, _ ListIn) (ListOut, error) {
			got, err := m.ListStores(ctx)
			if err != nil {
				return ListOut{}, err
			}
			if got == nil {
				got = []Entry{}
			}
			ol, hasOrg := upgrade.As[OrgLister](m)
			if hasOrg {
				if got, err = withOrgStores(ctx, ol, got); err != nil {
					return ListOut{}, err
				}
			}
			// BRANCH is the effective branch of every spelling (PinnedBranch,
			// shared with store.open), set before the status reads so a
			// `?branch=` entry is looked up in its own branch clone. A url
			// whose branch query is broken keeps its column empty here;
			// opening that store reports the cause.
			for i := range got {
				if b, berr := PinnedBranch(got[i].URL, got[i].Branch); berr == nil {
					got[i].Branch = b
				}
			}
			if sr, ok := upgrade.As[StatusReader](m); ok {
				for i := range got {
					st := sr.StoreStatus(ctx, got[i])
					got[i].Visibility, got[i].Reach = st.Visibility, st.Reach
				}
			}
			if an, ok := upgrade.As[AddressNoter](m); ok { // before the org urls are redacted
				for i := range got {
					got[i].Address = an.AddressNote(ctx, got[i].URL, got[i].Branch)
				}
			}
			// after the status reads: a default branch filled in earlier
			// would send them to a pinned-branch clone that is not there
			if db, ok := upgrade.As[DefaultBrancher](m); ok {
				for i := range got {
					if got[i].Branch == "" {
						got[i].Branch = db.CloneDefaultBranch(ctx, got[i])
					}
				}
			}
			// after the status reads, which need the real url
			if hasOrg && ol.QuietOutput() {
				for i := range got {
					if got[i].Source == SourceOrg {
						got[i].URL = redactedURL
					}
					if strings.HasPrefix(got[i].Address, addressSecond) {
						got[i].Address = addressRedacted // its note names the other url
					}
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
			in.Name = strings.TrimSpace(in.Name)
			if err := CheckStoreArg(in.Name); err != nil {
				return VisibilityOut{}, err
			}
			if in.Visibility != VisibilityInternal && in.Visibility != VisibilityShareable {
				return VisibilityOut{}, errs.New(errs.InvalidInput, "store visibility: the visibility must be internal or shareable, not "+quote(in.Visibility)).
					WithHint("internal (the default) or shareable: lore store visibility " + in.Name + " shareable")
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
			Examples: []op.Example{
				{Title: "a folder for one topic", CLI: "lore store folder add golang concurrency", Want: "created folder concurrency"},
				{Title: "a client folder in the team store, from any folder of the setup (no repo needed)", CLI: "lore store folder add team client_north", Want: "created folder client_north"},
			},
		}, func(ctx context.Context, in FolderAddIn) (FolderAddOut, error) {
			in.Store = strings.TrimSpace(in.Store)
			if err := CheckStoreArg(in.Store); err != nil {
				return FolderAddOut{}, err
			}
			if err := CheckFolderName(in.Folder); err != nil {
				return FolderAddOut{}, err
			}
			return m.AddFolder(ctx, in)
		}),
		folderListOp(m),
		setURLOp(m),
	}, append(cloneOps(m), pushOps(m)...)...)
}

// OrgLister is the optional Manager upgrade behind store.list's org rows
// (CLI_REFERENCE 3.21 "the stores that come from an org", G-CORE-8). It
// is kept out of Manager.ListStores, whose other callers (migrate, the
// private render, the repos-using-a-store record) read only stores.yaml
// and me.
type OrgLister interface {
	// OrgStoreEntries lists the stores the repo's org brings, with their
	// real urls and Source SourceOrg.
	OrgStoreEntries(ctx context.Context) ([]Entry, error)
	// QuietOutput reports output that must name nothing (no terminal,
	// CI): store.list then prints an org store's url as redactedURL.
	QuietOutput() bool
}

// redactedURL replaces an org store's url in quiet output.
const redactedURL = "<redacted>"

// withOrgStores appends the org's stores to got, skipping a name got
// already holds (stores.yaml wins a name), before the personal store so
// `me` stays last (BATCH 15).
func withOrgStores(ctx context.Context, ol OrgLister, got []Entry) ([]Entry, error) {
	org, err := ol.OrgStoreEntries(ctx)
	if err != nil || len(org) == 0 {
		return got, err
	}
	seen := map[string]bool{}
	var head, personal []Entry
	for _, e := range got {
		seen[e.Name] = true
		if e.Personal {
			personal = append(personal, e)
			continue
		}
		head = append(head, e)
	}
	for _, e := range org {
		if !seen[e.Name] {
			seen[e.Name] = true
			head = append(head, e)
		}
	}
	return append(head, personal...), nil
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
	// A row is counted once, by the store folder that holds it.
	Refused  int      `json:"refused"`
	Problems []string `json:"problems,omitempty"`
	Offline  bool     `json:"offline,omitempty"`
	// HeldBy is set when this folder's push did not run because of a
	// refusal of another folder of the same store (owner decision 7): that
	// folder's attached name; Held is the line that says so (HeldLine).
	HeldBy string `json:"held_by,omitempty"`
	Held   string `json:"held,omitempty"`
}

// SyncAll syncs every attached store except the repo store (the person
// pushes their code), or only the store named only. Refused rows end in
// E_STALE so scripts see a non-zero exit; both sides are kept. A refusal
// is reported once, by the folder that holds the row; every other folder
// of that store says it is held (HeldLine), since its push waited too.
func SyncAll(ctx context.Context, s Syncer, only string) (SyncOut, error) {
	names := s.Stores()
	if only != "" {
		if !slices.Contains(names, store.Source(only)) {
			return SyncOut{}, errs.New(errs.NotFound, "sync: no store named "+quote(only)+" is attached").
				WithHint("run `lore store list`")
		}
		names = []store.Source{store.Source(only)}
	}
	type synced struct {
		name store.Source
		rep  store.SyncReport
	}
	var done []synced
	var serr error
	for _, n := range names {
		if n == store.SourceRepo {
			continue
		}
		rep, err := s.Sync(ctx, n)
		if err != nil {
			serr = errs.Prefix("sync: "+string(n), err)
			break
		}
		done = append(done, synced{n, rep})
	}
	// The attached name of the folder that reports each refused row.
	owner := map[ids.AnyID]string{}
	for _, d := range done {
		for _, x := range d.rep.Refused {
			if !x.Held && !x.ID.IsZero() {
				owner[x.ID] = string(d.name)
			}
		}
	}
	out := SyncOut{Stores: []Report{}}
	var problems []string
	pushRefused := true // every refusal is a push the remote refused (not a merge refusal)
	for _, d := range done {
		n, rep := d.name, d.rep
		r := Report{Store: string(n), Sent: rep.Sent, Received: rep.Received, Merged: len(rep.Merged), Offline: rep.Offline}
		var held []store.Refusal
		for _, x := range rep.Refused {
			if x.Held {
				held = append(held, x)
				continue
			}
			r.Refused++
			pushRefused = pushRefused && x.Code == errs.PolicyDenied
			p := string(x.Code) + " " + x.ID.String() + ": " + x.Reason
			r.Problems = append(r.Problems, p)
			problems = append(problems, string(n)+": "+p)
		}
		if r.Refused == 0 && len(held) > 0 {
			r.HeldBy, r.Held = heldReport(held, owner)
			unreported := false
			for _, x := range held {
				if _, reported := owner[x.ID]; !reported {
					// a folder that holds a row is not synced here: the
					// refusal is this line's to report
					unreported = true
					pushRefused = pushRefused && x.Code == errs.PolicyDenied
				}
			}
			if unreported {
				problems = append(problems, string(n)+": "+r.Held)
			}
		}
		out.Stores = append(out.Stores, r)
	}
	if serr != nil {
		return out, serr
	}
	if len(problems) > 0 && pushRefused {
		return out, errs.New(errs.Stale, "sync: "+strings.Join(problems, "; ")).
			WithHint("the store refused the push; the commits wait in the local clone: `lore sync status` shows the rows and the next step")
	}
	if len(problems) > 0 {
		return out, errs.New(errs.Stale, "sync: "+strings.Join(problems, "; ")).
			WithHint("both edits are kept; settle the row on one side (lore memory show <id> on each), then sync again")
	}
	return out, nil
}

// heldReport is the held names and line of a folder whose refusals are
// all held: every holding folder (its attached name when a folder synced
// in this run reports the row, else HeldFolderName), in folder order, and
// their rows. by joins the names with ", " (Report.HeldBy).
func heldReport(held []store.Refusal, owner map[ids.AnyID]string) (by, line string) {
	var folders []string
	for _, x := range held {
		if !slices.Contains(folders, x.Folder) {
			folders = append(folders, x.Folder)
		}
	}
	slices.Sort(folders)
	on := make([]HeldOn, 0, len(folders))
	for _, folder := range folders {
		h := HeldOn{By: HeldFolderName(folder)}
		for _, x := range held {
			if x.Folder != folder {
				continue
			}
			if h.Code == "" {
				h.Code = string(x.Code)
			}
			if name, ok := owner[x.ID]; ok {
				h.By = name
			}
			if !x.ID.IsZero() && !slices.Contains(h.Rows, x.ID.String()) {
				h.Rows = append(h.Rows, x.ID.String())
			}
		}
		slices.Sort(h.Rows)
		on = append(on, h)
	}
	return HeldNames(on), HeldLines(on)
}

// HeldOn is one store folder whose refusal holds another folder's push:
// its name, the refusal's code and the rows it refused.
type HeldOn struct {
	By   string
	Code string
	Rows []string
}

// HeldNames joins the holding folders' names with ", " (the held_by of a
// push held by two or more folders).
func HeldNames(on []HeldOn) string {
	names := make([]string, 0, len(on))
	for _, h := range on {
		names = append(names, h.By)
	}
	return strings.Join(names, ", ")
}

// HeldLine is the one line of a store folder whose push waits on a
// refusal of another folder of the same store (owner decision 7: the
// refusal is reported once, by the folder that holds the row): "held:
// waiting on <folder>'s conflict (<rows>)", or "refused push" for a
// refusal that is not a both-sides edit.
func HeldLine(by, code string, rows []string) string {
	return HeldLines([]HeldOn{{By: by, Code: code, Rows: rows}})
}

// HeldLines is HeldLine for a push held by refusals in several folders:
// every one is named ("held: waiting on a's conflict (r1) and b's refused
// push (r2)"), never only the first.
func HeldLines(on []HeldOn) string {
	parts := make([]string, 0, len(on))
	for _, h := range on {
		what := "refused push"
		if c := errs.Code(h.Code); c == errs.Stale || c == errs.ResourceConflict {
			what = "conflict"
		}
		p := h.By + "'s " + what
		if len(h.Rows) > 0 {
			p += " (" + strings.Join(h.Rows, ", ") + ")"
		}
		parts = append(parts, p)
	}
	return "held: waiting on " + JoinAnd(parts)
}

// JoinAnd is "a", "a and b", "a, b and c".
func JoinAnd(parts []string) string {
	if len(parts) <= 1 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// HeldFolderName names a store folder that no attached name stands for:
// "the store root" or "folder <name>".
func HeldFolderName(folder string) string {
	if folder == rowfs.FolderRoot {
		return "the store root"
	}
	return "folder " + folder
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
	return slices.Contains([]string{OpInit, OpAdd, OpRemove, OpList, OpVisibility, OpFolderAdd, OpFolderList, OpSetURL}, name)
}
