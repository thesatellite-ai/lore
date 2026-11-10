package stores

// `lore store show|open|forget|doctor`: this machine's clones of the
// v0.1.17 org stores (orgs[].stores in ~/.lore/config.yaml), CLI_REFERENCE
// 3.21 ("the v0.1.17 store commands stay as they are"), LORE_TEAM_SPEC 1,
// F12 and 5.3. They read and delete local clones only and write no row,
// so they are Local (op.Spec.Local, P3-CFG-1): no store is opened for
// them and an unreachable one never blocks them. Flow:
//
//	store.show   ─► Cloner.ShowClones   (no network; --links adds the
//	                                     cached link index and this repo's links)
//	store.open   ─► Cloner.OpenClone    (fetch, detached worktree under
//	                                     ~/.lore/worktrees)
//	store.forget ─► Cloner.ForgetClone  (refused while unpushed work waits,
//	                                     side effect 20)
//	store.doctor ─► StoreChecker.CheckStores (every store this repo and
//	                                     machine use: reachable, unpushed
//	                                     commits and age, refused push, clone
//	                                     folders) + Cloner.DoctorClones (the
//	                                     v0.1.17 org stores; always asks the
//	                                     remote); a store with problems ends
//	                                     in E_INVALID_INPUT
//
// The clone layer is the composition root's (Manager upgrade Cloner); a
// build without it answers E_UNSUPPORTED.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// Operation names of the clone commands.
const (
	OpShow   = "store.show"
	OpOpen   = "store.open"
	OpForget = "store.forget"
	OpDoctor = "store.doctor"
)

// shortSHA is how much of a commit id store.open prints.
const shortSHA = 12

// fetchedNever is the last-fetch text of a clone never fetched.
const fetchedNever = "never"

// Cloner is the optional Manager upgrade behind the clone commands.
type Cloner interface {
	ShowClones(ctx context.Context, in ShowIn) (ClonesOut, error)
	OpenClone(ctx context.Context, in OpenIn) (OpenOut, error)
	ForgetClone(ctx context.Context, in ForgetIn) (ForgetOut, error)
	// DoctorClones runs every check; problems are in the views (the op
	// turns any into E_INVALID_INPUT).
	DoctorClones(ctx context.Context) (ClonesOut, error)
}

// ShowIn is the input of store.show.
type ShowIn struct {
	// Store, when given, shows that store's push mode and compaction
	// settings instead of the clone list (store push modes, 2026-11-09).
	Store string `json:"store,omitempty" arg:"0" desc:"one store (me, team, an org store or a .lore/stores.yaml store name): its push mode, compaction and unpushed commits"`
	Links bool   `json:"links,omitempty" flag:"links" desc:"also print the cached link index of every entry"`
}

// DoctorIn is the (empty) input of store.doctor.
type DoctorIn struct{}

// OpenIn is the input of store.open.
type OpenIn struct {
	URL    string `json:"url" arg:"0" desc:"the store url (any form a configured store uses) or a store name this repo attaches"`
	Branch string `json:"branch,omitempty" flag:"branch" desc:"store branch (default: the entry's branch)"`
}

// ForgetIn is the input of store.forget.
type ForgetIn struct {
	URL string `json:"url" arg:"0" desc:"the store url (or a store name this repo attaches) whose clone this machine drops"`
}

// CloneLink is one link of a store's cached link index (--links).
type CloneLink struct {
	Entry     string `json:"entry"`
	Folder    string `json:"folder"`
	Branch    string `json:"branch"`
	ProjectID string `json:"project_id"`
	Remote    string `json:"remote"`
	Own       bool   `json:"own,omitempty"`
	Default   bool   `json:"default,omitempty"`
}

// CloneView is one configured store as this machine knows it; entries on
// one url share one clone.
type CloneView struct {
	URL      string   `json:"url"`
	Entries  []string `json:"entries"`
	Branches []string `json:"branches"`
	Clone    string   `json:"clone"`
	Cloned   bool     `json:"cloned"`
	// LastFetch is the last fetch time (RFC 3339, local zone); "" = never.
	LastFetch     string      `json:"last_fetch,omitempty"`
	DefaultBranch string      `json:"default_branch,omitempty"`
	Unpushed      []string    `json:"unpushed"`
	Links         []CloneLink `json:"links,omitempty"`
	Problems      []string    `json:"problems"`
	// Shown is URL as printed (redacted when output must name nothing).
	Shown string `json:"-"`
}

// StoreChecker is the optional Manager upgrade behind the first half of
// store.doctor: every store this repo and machine use (stores.yaml, the
// org's stores, the personal store `me`), not only the v0.1.17 org stores
// of ~/.lore/config.yaml.
type StoreChecker interface {
	CheckStores(ctx context.Context) []StoreCheck
}

// StoreCheck is one store's health as store.doctor reports it.
type StoreCheck struct {
	Store string `json:"store"`
	// Source is SourceStoresFile, SourceOrg or SourcePersonal.
	Source string `json:"source"`
	// Kind is git or folder.
	Kind string `json:"kind"`
	URL  string `json:"url"`
	// Reachable is whether the remote (git) or the folder answered.
	Reachable bool `json:"reachable"`
	// Clone is the git clone's state on this machine (whole, no_worktree,
	// no_git_data, mislinked, orphan_worktree; not_cloned when there is
	// none); "" for a folder store.
	Clone string `json:"clone,omitempty"`
	// Unpushed counts the local commits the remote has not confirmed;
	// OldestUnpushed and LastPush are RFC 3339 UTC ("" = none).
	Unpushed       int    `json:"unpushed"`
	OldestUnpushed string `json:"oldest_unpushed,omitempty"`
	LastPush       string `json:"last_push,omitempty"`
	// Refused is the store's last refused push, when one waits.
	Refused  PushRefusal `json:"refused,omitzero"`
	Problems []string    `json:"problems"`
	// Notes are facts that need nothing done (where it stands).
	Notes []string `json:"notes"`
	// Shown is URL as printed (redacted when output must name nothing).
	Shown string `json:"-"`
}

// PushRefusal is a store push the remote refused; the commits wait in
// the local clone.
type PushRefusal struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
	At     string `json:"at"`
	Rows   int    `json:"rows"`
	// HeldBy is set when nothing of this store folder was refused: the
	// push waits on that other folder's refusal (owner decision 7).
	HeldBy string `json:"held_by,omitempty"`
}

// Store kinds of StoreCheck.
const (
	StoreKindGit    = "git"
	StoreKindFolder = "folder"
)

// ClonesOut is the output of store.show and store.doctor.
type ClonesOut struct {
	// Checked are the stores this repo and machine use (store.doctor
	// only; StoreChecker).
	Checked []StoreCheck `json:"checked,omitempty"`
	Stores  []CloneView  `json:"stores"`
	// Here are the debug lines of this repo's links (show --links).
	Here []string `json:"here,omitempty"`
	// Push is one store's settings (store show <store>).
	Push   PushView `json:"push,omitzero"`
	doctor bool
}

// OpenOut is the output of store.open.
type OpenOut struct {
	Dir    string `json:"dir"`
	Branch string `json:"branch"`
	Commit string `json:"commit"`
}

// Text is the v0.1.17 line.
func (o OpenOut) Text() string {
	c := o.Commit
	if len(c) > shortSHA {
		c = c[:shortSHA]
	}
	return fmt.Sprintf("✓ %s at %s (%s, detached; read-only by convention — lore writes the branch itself)\n", o.Dir, o.Branch, c)
}

// ForgetOut is the output of store.forget.
type ForgetOut struct {
	URL string `json:"url"`
	// Removed is the clone folder deleted ("" when there was none).
	Removed string `json:"removed,omitempty"`
	// Shown is URL as printed.
	Shown string `json:"-"`
}

// Text is the v0.1.17 line.
func (o ForgetOut) Text() string {
	if o.Removed == "" {
		return "no clone of " + o.Shown + " on this machine; nothing to forget\n"
	}
	return fmt.Sprintf("✓ forgot %s (removed %s; the next command that needs it clones it again)\n", o.Shown, o.Removed)
}

// Store doctor's section heads and empty lines. The v0.1.17 org stores
// (orgs[].stores in ~/.lore/config.yaml) are a second section after the
// stores this repo and machine use.
const (
	doctorUsedHead   = "stores this repo and machine use:"
	doctorNoneUsed   = "  none (no .lore/stores.yaml entry, no org store, no personal store)"
	doctorOrgHead    = "v0.1.17 org stores (orgs[].stores in ~/.lore/config.yaml):"
	doctorNoOrgStore = "  none"
)

// Text prints store.show (or store.doctor) as v0.1.17 did.
func (o ClonesOut) Text() string {
	var b strings.Builder
	if o.doctor {
		b.WriteString(doctorUsedHead + "\n")
		if len(o.Checked) == 0 {
			b.WriteString(doctorNoneUsed + "\n")
		}
		for _, c := range o.Checked {
			mark := "✓"
			if len(c.Problems) > 0 {
				mark = "!"
			}
			fmt.Fprintf(&b, "%s %s (%s, %s) %s\n", mark, c.Store, c.Source, c.Kind, c.Shown)
			for _, p := range c.Problems {
				b.WriteString("    " + p + "\n")
			}
			for _, n := range c.Notes {
				b.WriteString("    " + n + "\n")
			}
		}
		b.WriteString(doctorOrgHead + "\n")
		if len(o.Stores) == 0 {
			b.WriteString(doctorNoOrgStore + "\n")
		}
		for _, v := range o.Stores {
			mark := "✓"
			if len(v.Problems) > 0 {
				mark = "!"
			}
			fmt.Fprintf(&b, "%s %s  (entries %s; branches %s)\n", mark, v.Shown, strings.Join(v.Entries, ", "), strings.Join(v.Branches, ", "))
			for _, p := range v.Problems {
				b.WriteString("    " + p + "\n")
			}
		}
		return b.String()
	}
	if o.Push.Store != "" {
		return o.Push.Text()
	}
	for _, h := range o.Here {
		b.WriteString(h + "\n")
	}
	if len(o.Stores) == 0 {
		b.WriteString("no stores configured (orgs[].stores in ~/.lore/config.yaml)\n")
		return b.String()
	}
	for _, v := range o.Stores {
		b.WriteString(v.Shown + "\n")
		fmt.Fprintf(&b, "  entries   %s   branches %s\n", strings.Join(v.Entries, ", "), strings.Join(v.Branches, ", "))
		for _, p := range v.Problems {
			b.WriteString("  problem   " + p + "\n")
		}
		if !v.Cloned {
			b.WriteString("  clone     not cloned yet (the next command that needs it clones it)\n")
			continue
		}
		fetched := fetchedNever
		if v.LastFetch != "" {
			fetched = v.LastFetch
		}
		fmt.Fprintf(&b, "  clone     %s (bare)   default branch %s   last fetch %s\n", v.Clone, v.DefaultBranch, fetched)
		if len(v.Unpushed) > 0 {
			fmt.Fprintf(&b, "  unpushed  %s\n", strings.Join(v.Unpushed, ", "))
		}
		for _, l := range v.Links {
			d, own := "", ""
			if l.Default {
				d = " (default)"
			}
			if l.Own {
				own = " own"
			}
			fmt.Fprintf(&b, "  link      %s/ on %s ← %s %s%s%s\n", l.Folder, l.Branch, l.Remote, l.ProjectID, own, d)
		}
	}
	return b.String()
}

// cloneErrors are the codes of the clone commands.
var cloneErrors = []errs.Code{errs.InvalidInput, errs.NotFound, errs.ConfigInvalid, errs.StoreUnreachable,
	errs.NotAStore, errs.Unsupported, errs.Refused, errs.StoreReadOnly, errs.StoreScheme, errs.Internal}

// cloner is m's Cloner, or E_UNSUPPORTED naming the command.
func cloner(m Manager, cmd string) (Cloner, error) {
	c, ok := upgrade.As[Cloner](m)
	if !ok {
		return nil, errs.New(errs.Unsupported, "store "+cmd+": this build has no v0.1.17 store clones")
	}
	return c, nil
}

// storeNamed resolves a store NAME given to store open|forget (a
// stores.yaml name or me, as `lore store remove` tells the person to run
// `lore store forget <name>`; G-CORE-5) to its entry. A url (anything that
// is not a snake_case name) is left as it is (named false). A name this
// repo does not see is E_NOT_FOUND; outside a repo there are no names, so
// a name is refused the same way with the url form in the hint.
func storeNamed(ctx context.Context, m Manager, cmd, raw string) (Entry, bool, error) {
	name := strings.TrimSpace(raw)
	if !row.ValidName(name) {
		return Entry{}, false, nil
	}
	list, err := m.ListStores(ctx)
	if err == nil {
		for _, e := range list {
			if e.Name == name {
				return e, true, nil
			}
		}
	}
	return Entry{}, false, errs.New(errs.NotFound, "store "+cmd+": no store named "+name+" in this repo").
		WithHint("run `lore store list`, or give the store url (lore store " + cmd + " <url>)")
}

// cloneOps are the clone commands bound to m.
func cloneOps(m Manager) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpShow, Summary: "List the configured org stores, their clones and (with --links) the link index; with a store name, its push mode and compaction",
			Permission: op.PermRead, Access: op.AccessRead, Errors: cloneErrors, Local: true,
			Examples: []op.Example{{Title: "every store", CLI: "lore store show", Want: ""}, {Title: "the personal store's push mode", CLI: "lore store show me", Want: "push"}},
		}, func(ctx context.Context, in ShowIn) (ClonesOut, error) {
			if name := strings.TrimSpace(in.Store); name != "" {
				p, err := pusher(m, "show")
				if err != nil {
					return ClonesOut{}, err
				}
				v, err := p.StorePush(ctx, name)
				if err != nil {
					return ClonesOut{}, err
				}
				return withEmptyLists(ClonesOut{Push: v}), nil
			}
			c, err := cloner(m, "show")
			if err != nil {
				return ClonesOut{}, err
			}
			out, err := c.ShowClones(ctx, in)
			return withEmptyLists(out), err
		}),
		op.Define(op.Spec{
			Name: OpOpen, Summary: "Check out a store branch for browsing (detached, under ~/.lore/worktrees)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: cloneErrors, Local: true,
			Examples: []op.Example{{Title: "browse a store", CLI: "lore store open git@github.com:agency/notes", Want: "✓"}},
		}, func(ctx context.Context, in OpenIn) (OpenOut, error) {
			c, err := cloner(m, "open")
			if err != nil {
				return OpenOut{}, err
			}
			e, named, err := storeNamed(ctx, m, "open", in.URL)
			if err != nil {
				return OpenOut{}, err
			}
			if named {
				in.URL = e.URL
				if in.Branch == "" {
					in.Branch = e.Branch
				}
			}
			return c.OpenClone(ctx, in)
		}),
		op.Define(op.Spec{
			Name: OpForget, Summary: "Delete this machine's clone of a store (refused while it holds unpushed work)",
			// Admin: it deletes a local folder, never a row; the unpushed-work
			// refusal is the guard (side effect 20), so it does not ask.
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: cloneErrors, Local: true,
			Examples: []op.Example{{Title: "drop a clone", CLI: "lore store forget git@github.com:agency/notes", Want: ""}},
		}, func(ctx context.Context, in ForgetIn) (ForgetOut, error) {
			c, err := cloner(m, "forget")
			if err != nil {
				return ForgetOut{}, err
			}
			e, named, err := storeNamed(ctx, m, "forget", in.URL)
			if err != nil {
				return ForgetOut{}, err
			}
			if named {
				in.URL = e.URL
			}
			return c.ForgetClone(ctx, in)
		}),
		op.Define(op.Spec{
			Name: OpDoctor, Summary: "Check every store this repo and machine use (stores.yaml, the org, the personal store) and the v0.1.17 org stores: reachable, unpushed commits and their age, refused pushes, the clone folders",
			Permission: op.PermRead, Access: op.AccessRead, Errors: cloneErrors, Local: true,
			Examples: []op.Example{{Title: "check", CLI: "lore store doctor", Want: ""}},
		}, func(ctx context.Context, _ DoctorIn) (ClonesOut, error) {
			c, err := cloner(m, "doctor")
			if err != nil {
				return ClonesOut{}, err
			}
			out, err := c.DoctorClones(ctx)
			if err != nil {
				return ClonesOut{}, err
			}
			if sc, ok := upgrade.As[StoreChecker](m); ok {
				out.Checked = sc.CheckStores(ctx)
			}
			out = withEmptyLists(out)
			out.doctor = true
			return out, doctorVerdict(out)
		}),
	}
}

// doctorVerdict is nil when no store has a problem, else E_INVALID_INPUT
// naming each problem (P3-CFG-2: the op error carries what v0.1.17 printed
// before failing, so --json still shows every problem).
func doctorVerdict(out ClonesOut) error {
	var bad []string
	n := 0
	for _, c := range out.Checked {
		for _, p := range c.Problems {
			bad = append(bad, c.Store+": "+p)
		}
		if len(c.Problems) > 0 {
			n++
		}
	}
	for _, v := range out.Stores {
		for _, p := range v.Problems {
			bad = append(bad, v.Shown+": "+p)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	for _, v := range out.Stores {
		if len(v.Problems) > 0 {
			n++
		}
	}
	return errs.New(errs.InvalidInput, strconv.Itoa(n)+" store(s) need attention: "+strings.Join(bad, "; ")).
		WithHint("fix each problem, then run `lore store doctor` again")
}

// withEmptyLists replaces nil lists with empty ones (stable JSON).
func withEmptyLists(o ClonesOut) ClonesOut {
	if o.Stores == nil {
		o.Stores = []CloneView{}
	}
	for i := range o.Checked {
		c := &o.Checked[i]
		if c.Problems == nil {
			c.Problems = []string{}
		}
		if c.Notes == nil {
			c.Notes = []string{}
		}
		if c.Shown == "" {
			c.Shown = c.URL
		}
	}
	for i := range o.Stores {
		v := &o.Stores[i]
		if v.Unpushed == nil {
			v.Unpushed = []string{}
		}
		if v.Problems == nil {
			v.Problems = []string{}
		}
		if v.Shown == "" {
			v.Shown = v.URL
		}
	}
	return o
}
