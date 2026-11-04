package wire

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// targetSep splits `--to <store>/<folder>` (STORE TARGETS).
const targetSep = "/"

// Shortcut targets STORE TARGETS reserves for the repo's org store; the org
// noun is still v0.1.17's, so the core names the folder explicitly instead.
var orgShortcuts = []string{attach.ReservedTeam, attach.ReservedCompany}

// prepareTarget looks at the operation's `to` before the engine runs it:
// `--to <store>/<folder>` attaches that folder of an attached store (or of
// `me`) as its own target, named "<store>/<folder>" (O1: one backend per
// data folder; `--to agency/org` is the company folder). A folder the store
// does not have yet is created only after a yes (E_NEEDS_CONFIRM).
// `team` and `company` are refused with the explicit form to use.
func (a *App) prepareTarget(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) error {
	in, err := e.Decode(d)
	if err != nil {
		return nil // the engine's Run reports the decode error itself
	}
	to := ""
	for _, tv := range in.Texts() {
		if tv.Field == engine.InputTarget {
			to = tv.Text
		}
	}
	if slices.Contains(orgShortcuts, to) {
		return errs.New(errs.Unsupported, "--to "+to+" needs the repo's org, which the 0.1.18 core does not resolve yet").
			WithHint("name the store and folder: --to <store>/org for the company folder, --to <store> for the linked one")
	}
	base, folderName, ok := strings.Cut(to, targetSep)
	if !ok || base == "" || folderName == "" {
		return nil
	}
	name := store.Source(to)
	a.mu.Lock()
	for _, x := range a.extra {
		if x.Name == name {
			a.mu.Unlock()
			return nil
		}
	}
	a.mu.Unlock()
	src, found, err := a.baseStore(ctx, base)
	if err != nil || !found {
		return err // not found: the engine answers "no store named … is attached"
	}
	exists, err := a.folderExists(ctx, src.URL, folderName)
	if err != nil {
		return err
	}
	if !exists && !c.Yes {
		return errs.New(errs.NeedsConfirm, "store "+base+" has no folder "+folderName+" yet").
			WithQuestion("create the folder " + folderName + " in store " + base + "?").
			WithHint("rerun with --yes to create it with this write")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.extra = append(a.extra, attach.Store{Name: name, URL: src.URL, Folder: folderName, ReadOnly: src.ReadOnly})
	if a.st != nil { // already open (a long-lived `lore api`): reopen with the new target
		cerr := a.st.close()
		a.st, a.opened, a.err = nil, false, nil
		if cerr != nil {
			return cerr
		}
	} else if a.opened {
		a.opened, a.err = false, nil
	}
	return nil
}

// baseStore finds the stores.yaml entry (or `me`) a folder target names.
func (a *App) baseStore(ctx context.Context, name string) (attach.Store, bool, error) {
	if name == attach.ReservedMe {
		mc, err := readMachineConfig(a.opts.Home)
		if err != nil {
			return attach.Store{}, false, err
		}
		p := mc.personalURL()
		return attach.Store{Name: store.Source(name), URL: p}, p != "", nil
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		return attach.Store{}, false, err
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return attach.Store{}, false, err
	}
	for _, s := range cfg.Stores {
		if string(s.Name) == name {
			return s, true, nil
		}
	}
	return attach.Store{}, false, nil
}
