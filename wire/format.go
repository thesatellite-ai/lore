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

// MigrateCommand is the command that answers the O6 question.
const MigrateCommand = "lore migrate run"

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
		WithHint("once they have, run `" + MigrateCommand + "` (it rewrites the files as _v: 2), then retry").
		WithNext(MigrateCommand)
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
		WithQuestion("has everyone who shares store " + string(name) + " upgraded to lore 0.1.18?").
		WithHint("once they have, run `" + MigrateCommand + "` (it converts the org store), then retry").
		WithNext(MigrateCommand)
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
		if mb, isMarker := b.(markerBackend); !isMarker || mb.Marker().Version >= rowfs.MarkerVersion {
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
// write yet — because those teammates become read-only. Nothing else is
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
	if ask && !engine.Confirmed(ctx) {
		return opsmigrate.EnableOut{}, errs.New(errs.NeedsConfirm, "migrate: enabling "+strconvQuote(name)+" makes this repository need lore "+f.Since+
			" or later to write; teammates who wrote its rows with lore 0.1.17 become read-only until they upgrade").
			WithQuestion("has everyone who shares this repository upgraded to lore " + f.Since + "?").
			WithHint("ask the person, then rerun with --yes").
			WithNext("lore migrate enable " + name + " --yes")
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

// strconvQuote quotes a user-given name for a message.
func strconvQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
