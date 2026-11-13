package setup

// lifecycle.go — a setup's life after it is made (owner decision
// 2026-11-06), as registry ops so CLI, REST, the page and the docs follow:
//
//	lore setup disable <name>   setups[].disabled: true — kept, matches nothing, doctor --all skips it
//	lore setup enable <name>    back in every match
//	lore setup remove <name>    → trash.setups (remove.go), restorable
//	lore setup trash list       what the trash holds, newest first
//	lore setup trash restore <name> [--as <new>]   back into setups[] with its repo pins
//	lore setup trash purge <name> [--dry-run] [--clear-identity] [--clear-personal-store] --confirm
//	    gone for good; the preview (and the confirm) lists what stays:
//	    the identity, the personal store, every GitHub repo; the org's
//	    orgs[] entry goes too when no other setup uses it
//	lore setup import-dismiss <key>   hide Home's import banner for these values
//
// The trash mirrors `lore trash` for rows: remove moves, restore brings
// back, purge removes for good after a confirm. None of it touches a
// store or a repo; only purge touches orgs[] (the purged setup's own org,
// when no other setup uses it).

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Op names of the lifecycle ops (page allowlist).
const (
	OpDisable       = "setup.disable"
	OpEnable        = "setup.enable"
	OpTrashList     = "setup.trash.list"
	OpTrashRestore  = "setup.trash.restore"
	OpTrashPurge    = "setup.trash.purge"
	OpImportDismiss = "setup.import_dismiss"
)

// TrashStore is the optional Store upgrade the trash needs (wire's store
// has it). Invariants: every write keeps the entry's unknown keys; nothing
// touches a store or a repo (purge drops an org entry only it used).
type TrashStore interface {
	// TrashSetup moves the entry and its repo pins to trash.setups;
	// found=false when there is no such setup.
	TrashSetup(ctx context.Context, name string, at time.Time) (found bool, err error)
	// RestoreSetup brings the newest trashed entry named name back as as
	// ("" = name); E_SETUP_NOT_FOUND / E_SETUP_NAME_TAKEN.
	RestoreSetup(ctx context.Context, name, as string) (RestoreOut, error)
	// PurgeSetup deletes the newest trashed entry named name for good (and
	// its setup_progress rows), plus the top-level identity / personal.store
	// when asked.
	PurgeSetup(ctx context.Context, name string, clearIdentity, clearStore bool) error
	// SetDisabled sets or clears disabled; found=false: no such setup.
	SetDisabled(ctx context.Context, name string, disabled bool, at time.Time) (found, changed bool, err error)
	// MachineIdentity is the top-level identity's name (else key) of
	// config.yaml, "" when none (the purge preview names it).
	MachineIdentity(ctx context.Context) string
}

// ImportDismisser is the optional Onboard upgrade that remembers a
// dismissed import banner in machine state (~/.lore/state), keyed by
// onboard.ImportBannerKey.
type ImportDismisser interface {
	DismissImport(ctx context.Context, key string) error
	ImportDismissed(ctx context.Context, key string) bool
}

// trashStore is the env's Store and its TrashStore upgrade.
func (e engine) trashStore(ctx context.Context) (Store, TrashStore, error) {
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	ts, ok := st.(TrashStore)
	if !ok {
		return nil, nil, errs.New(errs.Unsupported, "this lore keeps no setup trash").WithHint("edit ~/.lore/config.yaml by hand")
	}
	return st, ts, nil
}

// ToggleIn is setup.disable's and setup.enable's input.
type ToggleIn struct {
	Name string `json:"name" arg:"0" desc:"the setup name"`
}

// ToggleOut is setup.disable's and setup.enable's output.
type ToggleOut struct {
	Name     string `json:"name"`
	Disabled bool   `json:"disabled"`
	// Changed is false when the setup already was so.
	Changed bool `json:"changed"`
}

// Text implements op.Texter.
func (o ToggleOut) Text() string {
	state := "enabled"
	if o.Disabled {
		state = "disabled"
	}
	if !o.Changed {
		return "setup " + o.Name + " is already " + state + "\n"
	}
	if o.Disabled {
		return "disabled setup " + o.Name + ": it stays in ~/.lore/config.yaml but matches no folder, --setup or default; lore setup enable --name " + o.Name + " turns it back on\n"
	}
	return "enabled setup " + o.Name + "\n"
}

// Disable is setup.disable.
func (e engine) Disable(ctx context.Context, in ToggleIn) (ToggleOut, error) {
	return e.toggle(ctx, in, true)
}

// Enable is setup.enable.
func (e engine) Enable(ctx context.Context, in ToggleIn) (ToggleOut, error) {
	return e.toggle(ctx, in, false)
}

// toggle sets disabled on the named setup.
func (e engine) toggle(ctx context.Context, in ToggleIn, disabled bool) (ToggleOut, error) {
	name := setupName(ctx, in.Name)
	if err := requireName(name); err != nil {
		return ToggleOut{}, err
	}
	st, ts, err := e.trashStore(ctx)
	if err != nil {
		return ToggleOut{}, err
	}
	found, changed, err := ts.SetDisabled(ctx, name, disabled, e.now())
	if err != nil {
		return ToggleOut{}, err
	}
	if !found {
		cfg, cerr := st.Config(ctx)
		if cerr != nil {
			return ToggleOut{}, cerr
		}
		return ToggleOut{}, notFound(name, cfg)
	}
	return ToggleOut{Name: name, Disabled: disabled, Changed: changed}, nil
}

// TrashListIn is setup.trash.list's input (none).
type TrashListIn struct{}

// TrashItem is one trashed setup.
type TrashItem struct {
	Setup     onboard.SetupEntry `json:"setup"`
	RemovedAt string             `json:"removed_at"`
	RepoPins  []string           `json:"repo_pins,omitempty"`
	// NameTaken is set when a setup of the same name exists now: restore
	// needs --as.
	NameTaken bool `json:"name_taken,omitempty"`
}

// TrashListOut is setup.trash.list's output, newest first.
type TrashListOut struct {
	Setups []TrashItem `json:"setups"`
}

// Text implements op.Texter.
func (o TrashListOut) Text() string {
	if len(o.Setups) == 0 {
		return "the setup trash is empty\n"
	}
	var b strings.Builder
	b.WriteString("setups in the trash (lore setup trash restore --name <name> brings one back):\n")
	for _, t := range o.Setups {
		b.WriteString("  " + t.Setup.Name + "  " + string(t.Setup.Kind) + "  removed " + t.RemovedAt)
		if t.NameTaken {
			b.WriteString("  (name taken: restore with --as)")
		}
		if t.Setup.Note != "" {
			b.WriteString("  — " + t.Setup.Note)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// TrashList is setup.trash.list.
func (e engine) TrashList(ctx context.Context, _ TrashListIn) (TrashListOut, error) {
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return TrashListOut{}, err
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return TrashListOut{}, err
	}
	out := TrashListOut{Setups: make([]TrashItem, 0, len(cfg.Trash))}
	for _, t := range cfg.Trash {
		_, taken := cfg.Setup(t.Setup.Name)
		out.Setups = append(out.Setups, TrashItem{Setup: t.Setup, RemovedAt: t.RemovedAt, RepoPins: t.RepoPins, NameTaken: taken})
	}
	slices.SortStableFunc(out.Setups, func(a, b TrashItem) int { return strings.Compare(b.RemovedAt, a.RemovedAt) })
	return out, nil
}

// RestoreIn is setup.trash.restore's input.
type RestoreIn struct {
	Name string `json:"name" arg:"0" desc:"the trashed setup's name (the newest of that name)"`
	As   string `json:"as,omitempty" flag:"as" desc:"restore under this name (when a setup of the old name exists)"`
}

// RestoreOut is setup.trash.restore's output.
type RestoreOut struct {
	Name string `json:"name"`
	// Pins are the repo pins put back; PinsSkipped those pinned to another
	// setup since (left as they are).
	Pins        []string `json:"pins,omitempty"`
	PinsSkipped []string `json:"pins_skipped,omitempty"`
	// DefaultCleared: it was the default, another setup is now; that one stays.
	DefaultCleared bool `json:"default_cleared,omitempty"`
}

// Text implements op.Texter.
func (o RestoreOut) Text() string {
	s := "restored setup " + o.Name + " from the trash\n"
	if o.DefaultCleared {
		s += "  it is no longer the default: another setup is the default now\n"
	}
	if len(o.PinsSkipped) > 0 {
		s += "  repo pins left on their current setup: " + strings.Join(o.PinsSkipped, ", ") + "\n"
	}
	return s
}

// TrashRestore is setup.trash.restore.
func (e engine) TrashRestore(ctx context.Context, in RestoreIn) (RestoreOut, error) {
	name := strings.TrimSpace(in.Name)
	if err := requireName(name); err != nil {
		return RestoreOut{}, err
	}
	_, ts, err := e.trashStore(ctx)
	if err != nil {
		return RestoreOut{}, err
	}
	return ts.RestoreSetup(ctx, name, strings.TrimSpace(in.As))
}

// PurgeIn is setup.trash.purge's input.
type PurgeIn struct {
	Name   string `json:"name" arg:"0" desc:"the trashed setup's name (the newest of that name)"`
	DryRun bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"show what is deleted and what stays; delete nothing"`
	// Confirm is required to delete (like lore trash purge, DQ2: --yes
	// never purges).
	Confirm bool `json:"confirm,omitempty" flag:"confirm" desc:"required: the setup is deleted for good"`
	// ClearIdentity also removes the top-level identity of config.yaml.
	ClearIdentity bool `json:"clear_identity,omitempty" flag:"clear-identity" desc:"also remove this machine's lore identity from ~/.lore/config.yaml"`
	// ClearPersonalStore also removes personal.store.
	ClearPersonalStore bool `json:"clear_personal_store,omitempty" flag:"clear-personal-store" desc:"also remove personal.store from ~/.lore/config.yaml (the store repo itself stays)"`
}

// PurgeOut is setup.trash.purge's output (and its --dry-run preview).
type PurgeOut struct {
	Name   string `json:"name"`
	DryRun bool   `json:"dry_run,omitempty"`
	// Deletes lists what the purge removes; Stays what it leaves in place.
	Deletes []string `json:"deletes"`
	Stays   []string `json:"stays"`
	// Identity and PersonalStore are the machine values the two optional
	// clears would remove ("" when unset).
	Identity      string `json:"identity,omitempty"`
	PersonalStore string `json:"personal_store,omitempty"`
}

// Text implements op.Texter.
func (o PurgeOut) Text() string {
	var b strings.Builder
	if o.DryRun {
		b.WriteString("would delete for good:\n")
	} else {
		b.WriteString("deleted for good:\n")
	}
	for _, d := range o.Deletes {
		b.WriteString("  - " + d + "\n")
	}
	b.WriteString("stays:\n")
	for _, s := range o.Stays {
		b.WriteString("  - " + s + "\n")
	}
	return b.String()
}

// TrashPurge is setup.trash.purge: a preview with --dry-run, else
// E_NEEDS_CONFIRM naming --confirm (and what stays) until --confirm is
// given — never on --yes, like lore trash purge (DQ2). The refusal comes
// before the lookup, so a refused purge reads nothing.
func (e engine) TrashPurge(ctx context.Context, in PurgeIn) (PurgeOut, error) {
	name := strings.TrimSpace(in.Name)
	if err := requireName(name); err != nil {
		return PurgeOut{}, err
	}
	if !in.DryRun && !in.Confirm {
		return PurgeOut{}, e.purgeRefusal(ctx, name, in)
	}
	st, ts, err := e.trashStore(ctx)
	if err != nil {
		return PurgeOut{}, err
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return PurgeOut{}, err
	}
	at := onboard.NewestTrashed(cfg.Trash, name)
	if at < 0 {
		return PurgeOut{}, errs.New(errs.SetupNotFound, "no setup named "+quote(name)+" in the trash").WithHint("lore setup trash list")
	}
	pre := purgePreview(cfg, cfg.Trash[at], ts.MachineIdentity(ctx), in)
	if in.DryRun {
		pre.DryRun = true
		return pre, nil
	}
	if err := ts.PurgeSetup(ctx, name, in.ClearIdentity, in.ClearPersonalStore); err != nil {
		return PurgeOut{}, err
	}
	return pre, nil
}

// purgeRefusal is the E_NEEDS_CONFIRM of a purge without --confirm: the
// preview's lists when the entry can be read, the plain question otherwise.
func (e engine) purgeRefusal(ctx context.Context, name string, in PurgeIn) error {
	q := "Delete setup " + name + " for good?"
	what := q
	if st, ts, err := e.trashStore(ctx); err == nil {
		if cfg, cerr := st.Config(ctx); cerr == nil {
			if at := onboard.NewestTrashed(cfg.Trash, name); at >= 0 {
				pre := purgePreview(cfg, cfg.Trash[at], ts.MachineIdentity(ctx), in)
				what = "deletes: " + strings.Join(pre.Deletes, "; ") + "\nstays: " + strings.Join(pre.Stays, "; ") + "\n" + q
			}
		}
	}
	next := []string{"setup", "trash", "purge", name, "--confirm"}
	if in.ClearIdentity {
		next = append(next, "--clear-identity")
	}
	if in.ClearPersonalStore {
		next = append(next, "--clear-personal-store")
	}
	return errs.New(errs.NeedsConfirm, what).WithQuestion(q).
		WithHint("rerun with --confirm to delete it for good (--yes never purges); --dry-run shows what stays").WithNext("lore", next...)
}

// purgePreview lists what a purge of t deletes and what stays.
func purgePreview(cfg onboard.Config, t onboard.TrashedSetup, identity string, in PurgeIn) PurgeOut {
	out := PurgeOut{Name: t.Setup.Name, Identity: identity, PersonalStore: cfg.SharedStore,
		Deletes: []string{"the trashed setup " + t.Setup.Name + " (removed " + t.RemovedAt + ") and its unfinished-step progress"}}
	keepID, keepStore := identity, cfg.SharedStore
	if in.ClearIdentity && identity != "" {
		out.Deletes = append(out.Deletes, "your lore identity ("+identity+") from ~/.lore/config.yaml")
		keepID = ""
	}
	if in.ClearPersonalStore && cfg.SharedStore != "" {
		out.Deletes = append(out.Deletes, "personal.store ("+cfg.SharedStore+") from ~/.lore/config.yaml; the store repo itself stays")
		keepStore = ""
	}
	if at := onboard.NewestTrashed(cfg.Trash, t.Setup.Name); onboard.OrgFreedByPurge(cfg, at) {
		out.Deletes = append(out.Deletes, "the org "+t.Setup.Org+"'s entry (orgs[] in ~/.lore/config.yaml): no other setup uses it, so the team name is free again")
		if u := cfg.OrgStores[t.Setup.Org]; u != "" {
			// The team's notes stay readable on this machine in its clone
			// and caches until the person drops them: say so and how
			// (journey joiner/month3/leave-job-a: leaving a job left its
			// notes behind without a word).
			out.Stays = append(out.Stays, "this machine's copy of the org "+t.Setup.Org+"'s store, with the team's notes: lore store forget --url "+u+" deletes it")
		}
	} else if t.Setup.Org != "" {
		out.Stays = append(out.Stays, "the org "+t.Setup.Org+" (orgs[] in ~/.lore/config.yaml): another setup uses it")
	}
	out.Stays = append(out.Stays, onboard.PurgeKeeps(t, keepID, keepStore)...)
	return out
}

// ImportDismissIn is setup.import_dismiss's input.
type ImportDismissIn struct {
	Key string `json:"key" arg:"0" desc:"the banner key setup.import_scan returned (banner_key)"`
}

// ImportDismissOut is setup.import_dismiss's output.
type ImportDismissOut struct {
	Key string `json:"key"`
}

// ImportDismiss is setup.import_dismiss.
func (e engine) ImportDismiss(ctx context.Context, in ImportDismissIn) (ImportDismissOut, error) {
	key := strings.TrimSpace(in.Key)
	if key == "" || len(key) > 64 || strings.ContainsFunc(key, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') }) {
		return ImportDismissOut{}, errs.New(errs.InvalidInput, "setup import-dismiss: key must be the hex banner_key of setup.import_scan").
			WithHint("lore setup import-scan --json shows banner_key")
	}
	d, ok := e.env.(ImportDismisser)
	if !ok {
		return ImportDismissOut{}, errs.New(errs.Unsupported, "this lore cannot remember a dismissed banner")
	}
	if err := d.DismissImport(ctx, key); err != nil {
		return ImportDismissOut{}, err
	}
	return ImportDismissOut{Key: key}, nil
}

// trashRESTNoun is the REST collection of the setup trash ops, under
// /v1/setup: GET /v1/setup/trash/setups lists, POST …/restore and …/purge.
// It is two segments on purpose: a setup name is one segment (a-z, 0-9,
// _ or -), so no setup name can ever equal it, and GET /v1/setup/{id}
// (setup.show) reaches every setup. With the "setup" collection noun the
// list sat at GET /v1/setup/setup and hid a setup named "setup"; a bare
// /v1/setup/trash would hide one named "trash" the same way.
const trashRESTNoun = "trash/setups"

// lifecycleOps are the lifecycle ops over e (registered by guidedOps).
func lifecycleOps(e engine, local func(op.Spec) op.Spec) []op.Entry {
	trash := func(s op.Spec) op.Spec {
		s = local(s)
		s.RESTNoun = trashRESTNoun
		return s
	}
	return []op.Entry{
		op.Define(local(op.Spec{Name: OpDisable, Summary: "Disable a setup: it stays in config.yaml but matches no folder, --setup or default, and doctor --all skips it",
			Permission: op.PermWrite, Errors: with(),
			Examples: []op.Example{{Title: "disable a setup", CLI: "lore setup disable --name old-client", Want: ""}}}),
			e.Disable),
		op.Define(local(op.Spec{Name: OpEnable, Summary: "Enable a disabled setup again",
			Permission: op.PermWrite, Errors: with(),
			Examples: []op.Example{{Title: "enable a setup", CLI: "lore setup enable --name old-client", Want: ""}}}),
			e.Enable),
		op.Define(trash(op.Spec{Name: OpTrashList, Summary: "List the setups in the trash (lore setup remove moved them there), newest first",
			Errors:   errsPort,
			Examples: []op.Example{{Title: "what is in the setup trash", CLI: "lore setup trash list", Want: ""}}}),
			e.TrashList),
		op.Define(trash(op.Spec{Name: OpTrashRestore, Summary: "Bring a setup back from the trash, with its repo pins",
			Permission: op.PermWrite, Errors: with(errs.SetupNameTaken),
			Examples: []op.Example{{Title: "restore a setup", CLI: "lore setup trash restore --name old-client", Want: ""}}}),
			e.TrashRestore),
		op.Define(trash(op.Spec{Name: OpTrashPurge, Summary: "Delete a trashed setup for good (never a store or a repo); --dry-run lists what stays",
			Permission: op.PermWrite, Errors: with(errs.NeedsConfirm),
			Examples: []op.Example{{Title: "preview a purge", CLI: "lore setup trash purge --name old-client --dry-run", Want: ""}}}),
			e.TrashPurge),
		op.Define(local(op.Spec{Name: OpRename, Summary: "Rename a setup: its entry, repo pins and saved progress take the new name",
			Permission: op.PermWrite, Errors: with(errs.SetupNameTaken),
			Examples: []op.Example{{Title: "rename a setup", CLI: "lore setup rename --name old-client --to client-archive", Want: ""}}}),
			e.Rename),
		op.Define(local(op.Spec{Name: OpImportDismiss, Summary: "Hide Home's import banner for the values it names (remembered in ~/.lore/state)",
			Permission: op.PermWrite, Errors: errsPort,
			Examples: []op.Example{{Title: "dismiss the import banner", CLI: "lore setup import-dismiss --key 0123456789abcdef", Want: ""}}}),
			e.ImportDismiss),
	}
}
