package wire

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/store"
)

// MigrateCommand is the command that answers the O6 question and every
// "this project has v0.1.17 rows" message (owner item 11: lore fix runs
// the migration with the project's other repairs).
const MigrateCommand = "lore fix"

// formatState is the DOCS O6 answer for one open App (FORMAT_EVOLUTION
// rule 2, F5): the first 0.1.18 write in a repo whose rows were written by
// someone else with v0.1.17 asks first, because the `_v: 2` rows it writes
// cannot be read by a teammate still on v0.1.17.
//
// No hidden state: "first" is read from the rows themselves. A repo that
// already holds a `_v: 2` row has had its first write; a repo with only
// the caller's own (or unattributed) v1 rows never asks.
type formatState struct {
	// ask: v1 rows by another writer and no v2 row yet.
	ask bool
	// written: a write landed since open (no more asking).
	written bool
}

// check refuses a write while the question is open. The answer is running
// MigrateCommand (the explicit "the team has upgraded"), after which every
// row is v2 and nobody is asked again.
func (f formatState) check() error {
	if !f.ask || f.written {
		return nil
	}
	return errs.New(errs.NeedsConfirm, "wire: this repository's rows were written by teammates with lore 0.1.17; "+
		"the first 0.1.18 write changes the file format they would need 0.1.18 to read").
		WithQuestion("has everyone who shares this repository upgraded to lore 0.1.18?").
		WithHint("once they have, run `"+MigrateCommand+"` (it rewrites the files as _v: 2), then retry").
		WithNext("lore", "fix")
}

// checkStore is check for a write into an attached v0.1.17 org store
// (O6 per store): the question names the store, because its teammates are
// the org's, not this repository's.
func (f formatState) checkStore(name store.Source) error {
	if !f.ask || f.written {
		return nil
	}
	return errs.New(errs.NeedsConfirm, "wire: store "+string(name)+" is still a lore 0.1.17 store whose rows teammates wrote; "+
		"the first 0.1.18 write there changes the file format they would need 0.1.18 to read").
		WithQuestion("has everyone who shares store "+string(name)+" upgraded to lore 0.1.18?").
		WithHint("once they have, run `"+MigrateCommand+"` (it converts the org store), then retry").
		WithNext("lore", "fix")
}

// readStoreFormats computes the O6 state of every attached org store
// (Options.OrgStores) still marked v0.1.17 (lore-store.yaml version 1);
// a 0.1.18 store, or one whose rows only the caller wrote, never asks.
func readStoreFormats(ctx context.Context, opts Options, top string, byName map[store.Source]store.Backend, caller ids.ID[ids.Actor]) (map[store.Source]formatState, error) {
	out := map[store.Source]formatState{}
	for _, s := range orgStores(ctx, opts, top) {
		b, ok := byName[s.Name]
		if !ok {
			continue
		}
		if mb, isMarker := b.(markerBackend); !isMarker || mb.Marker().Version >= rowfs.MarkerFirstStores {
			continue
		}
		f, err := readFormat(ctx, b, caller)
		if err != nil {
			return nil, err
		}
		if f.ask {
			out[s.Name] = f
		}
	}
	return out, nil
}

// readFormat computes the format state from the repo store's rows. A
// _meta.json that already declares hlc (the 0.1.18 row format, written by
// the first 0.1.18 write or by `lore migrate enable hlc --yes`) has had
// the question answered: the file records it, so every clone agrees.
func readFormat(ctx context.Context, repo store.Backend, caller ids.ID[ids.Actor]) (formatState, error) {
	if fr, ok := concrete(repo).(engine.FormatReader); ok {
		d, err := fr.Format(ctx)
		if err == nil && slices.Contains(d.Features, formatgate.HLC) {
			return formatState{}, nil
		}
	}
	rows, _, err := repo.Read(ctx, "")
	if err != nil {
		return formatState{}, err
	}
	var v1 []row.Row
	for _, sr := range rows {
		r, err := sr.Decode()
		if err != nil {
			return formatState{}, err
		}
		if r.SourceVersion == row.FormatVersion {
			return formatState{}, nil // a 0.1.18 write already happened
		}
		v1 = append(v1, r)
	}
	return formatState{ask: engine.HasOtherWriter(v1, caller)}, nil
}

// EnableFeature implements opsmigrate.Rewriter (`lore migrate enable`,
// FORMAT_EVOLUTION rule 2): declare a known feature in the checkout's
// .lore/data/_meta.json before its first use, raising min_lore_version to
// its Since. It asks first (E_NEEDS_CONFIRM unless --yes) while the O6
// question is open — rows written by teammates on v0.1.17 and no 0.1.18
// write yet — or while the team versions (`_w` stamps) show a writer below
// the feature's Since, listing them (rule 2 step 4, FQ2, OV48); a feature
// already declared asks nobody. Nothing else is
// written; the person commits the file with their code.
func (a *App) EnableFeature(ctx context.Context, name string) (opsmigrate.EnableOut, error) {
	f, ok := formatgate.Lookup(name)
	if !ok {
		names := make([]string, 0, len(formatgate.Known()))
		for _, k := range formatgate.Known() {
			names = append(names, k.Name)
		}
		return opsmigrate.EnableOut{}, errs.New(errs.InvalidInput, "migrate: "+strconvQuote(name)+" is not a feature this lore knows").
			WithHint("known features: " + strings.Join(names, ", "))
	}
	st, err := a.state(ctx)
	if err != nil {
		return opsmigrate.EnableOut{}, err
	}
	a.mu.Lock()
	ask := st.format.ask && !st.format.written
	a.mu.Unlock()
	if !engine.Confirmed(ctx) && !declared(st.repoTop, name) { // already declared: nobody's state changes
		below, berr := writersBelow(ctx, st.caches, f.Since)
		if berr != nil {
			return opsmigrate.EnableOut{}, berr
		}
		if ask || len(below) > 0 {
			return opsmigrate.EnableOut{}, enableConfirm(name, f.Since, ask, below)
		}
	}
	added, err := rowfs.DeclareRepo(st.repoTop, []string{name})
	if err != nil {
		return opsmigrate.EnableOut{}, err
	}
	d, _, err := rowfs.ReadRepoFormat(st.repoTop)
	if err != nil {
		return opsmigrate.EnableOut{}, err
	}
	if len(added) > 0 && name == formatgate.HLC {
		a.mu.Lock()
		st.format.written = true // the question is answered for this App too
		a.mu.Unlock()
	}
	return opsmigrate.EnableOut{Feature: name, Added: len(added) > 0, Features: d.Features, MinLoreVersion: d.MinLoreVersion}, nil
}

// declared reports whether the checkout's _meta.json already lists name
// (an unreadable file is "no": DeclareRepo then fails closed itself).
func declared(top, name string) bool {
	d, exists, err := rowfs.ReadRepoFormat(top)
	return err == nil && exists && slices.Contains(d.Features, name)
}

// writersBelow lists the writers of the repo store whose newest stamped
// save (`_w`, team versions, FQ3/OQ7) is below since, as "actor on
// mch_xxxxxxxx (lore v)". FMT-DEV: a dev-build stamp is never below. The
// view shows each machine's newest WRITE, so the list errs only towards
// asking once too often (FORMAT_EVOLUTION rule 2 "safe-direction limit").
func writersBelow(ctx context.Context, c *caches, since string) ([]string, error) {
	c.mu.Lock()
	cache := c.by[store.SourceRepo]
	if cache == nil {
		cache = c.combined // past MaxStoreCaches every store shares one file
	}
	c.mu.Unlock()
	if cache == nil {
		return nil, nil
	}
	tvs, err := cache.TeamVersions(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, tv := range tvs {
		if !formatgate.VersionBelow(tv.Version, since) {
			continue
		}
		machine := tv.Machine
		if machine == "" {
			machine = "a code repo save"
		} else if n := len("mch_") + machineShortHex; len(machine) > n {
			machine = machine[:n]
		}
		out = append(out, tv.Actor+" on "+machine+" (lore "+tv.Version+")")
	}
	return out, nil
}

// machineShortHex is how many hex digits of a machine id a message shows
// (the machine profile decision: mch_ plus 8 hex).
const machineShortHex = 8

// enableConfirm is migrate.enable's E_NEEDS_CONFIRM (rule 2 step 4, FQ2):
// who becomes read-only, the question, and the re-run with --yes.
func enableConfirm(name, since string, v017 bool, below []string) error {
	var who []string
	if v017 {
		who = append(who, "teammates who wrote this repository's rows with lore 0.1.17")
	}
	who = append(who, below...)
	return errs.New(errs.NeedsConfirm, "migrate: enabling "+strconvQuote(name)+" makes this repository need lore "+since+
		" or later to write; these writers become read-only until they upgrade: "+strings.Join(who, "; ")).
		WithQuestion("enable " + strconvQuote(name) + " for everyone (has everyone who shares this repository upgraded to lore " + since + ")?").
		WithHint("ask the person, then rerun with --yes").
		WithNext("lore migrate enable --feature " + name + " --yes")
}

// strconvQuote quotes a user-given name for a message.
func strconvQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
