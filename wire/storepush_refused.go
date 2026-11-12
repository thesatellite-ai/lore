package wire

// storepush_refused.go — a store push the remote refused (a server-side
// hook, branch protection, a permission) is remembered on this machine
// until a later push lands, so `lore sync status` can say which store,
// which rows wait and why (CLI_REFERENCE 2.9 `lore sync`: "Changes refused
// at the push hook stay local, marked refused with their E_ code, and
// `lore sync status` lists them").
//
//	storePush ─ every inner Sync (fetch, push after a write, lore sync)
//	    rep.Refused ─▶ record  (local.db store_push_refusals, key = store)
//	      only held ─▶ record held_by (another folder's row held the push)
//	    landed      ─▶ clear   (not offline, nothing refused, no error)
//	App.StorePushRefusals ─▶ lore sync status lines + data.store_refusals
//	                         (held ones: data.store_held)
//
// Owner decision 7 (2026-11-10): the store clone syncs every folder at
// once, so a row both sides changed stops the push of every folder of the
// store. The refusal is recorded once, under the folder that holds the
// row; each other folder records a held marker (HeldBy, the folder whose
// refusal holds it) and is never cleared or stamped as pushed while the
// refusal stands (its push did not run).
//
// The rows are the ids the backend names in its refusals (the git backend
// lists the rows of the refused range); a refusal with no id means the
// whole push. Recording never fails the command: local state that cannot
// be opened only loses the status line.

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/localstate"
)

// StorePushRefusal is one store whose last push was refused.
type StorePushRefusal struct {
	// Store is the store's name in this repo.
	Store string `json:"store"`
	// Code is the refusal's E_ code (E_POLICY_DENIED for a push the
	// remote refused, a merge refusal's own code otherwise).
	Code string `json:"code"`
	// Reason is what the remote said (git's push summary).
	Reason string `json:"reason"`
	// Rows are the ids of the rows the refused push carried; empty when
	// the backend could not list them (the whole push waits).
	Rows []string `json:"rows,omitempty"`
	// At is when the refusal was seen (RFC 3339, UTC).
	At string `json:"at"`
	// HeldBy, when set, marks a held push: nothing of this store folder
	// was refused, but the clone's push stopped on a refusal of a row in
	// that other folder of the same store (rowfs folder name, "." for the
	// root); Rows are that folder's rows. A binary without the field reads
	// it as a refusal of those rows, which is what it was before.
	HeldBy string `json:"held_by,omitempty"`
	// HeldOn names every folder whose refusal held this push, with its
	// code and rows, when there are several (HeldBy is the first, for a
	// binary without the field); empty for a push held by one folder.
	HeldOn []HeldFolder `json:"held_on,omitempty"`
}

// HeldFolder is one store folder whose refusal holds a push (rowfs folder
// name, "." for the root), its refusal's code and the rows it refused.
type HeldFolder struct {
	Folder string   `json:"folder"`
	Code   string   `json:"code"`
	Rows   []string `json:"rows,omitempty"`
}

// observeSync records or clears this store's refused push after one
// inner Sync: refused rows are recorded; a sync that reached the remote
// with nothing refused clears the record (the waiting commits landed).
// An error or an offline sync says nothing new and changes nothing.
func (b *storePush) observeSync(ctx context.Context, rep store.SyncReport, err error) {
	if err != nil || b.res == nil || b.res.home == "" {
		return
	}
	own, held := splitHeld(rep.Refused)
	switch {
	case len(own) > 0:
		b.recordRefusal(ctx, own, "")
	case len(held) > 0:
		b.recordRefusal(ctx, held, heldFolderOf(held))
	case !rep.Offline:
		b.clearRefusal(ctx)
	}
}

// splitHeld separates this folder's own refusals from the held ones (rows
// of another folder of the store, store.Refusal.Held).
func splitHeld(refused []store.Refusal) (own, held []store.Refusal) {
	for _, r := range refused {
		if r.Held {
			held = append(held, r)
		} else {
			own = append(own, r)
		}
	}
	return own, held
}

// heldFolderOf is the first holding folder of held refusals, in folder
// order (HeldBy of the record).
func heldFolderOf(held []store.Refusal) string {
	first := held[0].Folder
	for _, r := range held {
		if r.Folder < first {
			first = r.Folder
		}
	}
	return first
}

// heldFolders groups held refusals by the folder that holds them, in
// folder order; nil when one folder holds them all (HeldBy says it).
func heldFolders(held []store.Refusal) []HeldFolder {
	var out []HeldFolder
	for _, r := range held {
		i := slices.IndexFunc(out, func(h HeldFolder) bool { return h.Folder == r.Folder })
		if i < 0 {
			out = append(out, HeldFolder{Folder: r.Folder, Code: string(r.Code)})
			i = len(out) - 1
		}
		if id := r.ID.String(); !r.ID.IsZero() && !slices.Contains(out[i].Rows, id) {
			out[i].Rows = append(out[i].Rows, id)
		}
	}
	if len(out) < 2 {
		return nil
	}
	slices.SortFunc(out, func(x, y HeldFolder) int { return strings.Compare(x.Folder, y.Folder) })
	for i := range out {
		slices.Sort(out[i].Rows)
	}
	return out
}

// recordRefusal stores refused as this store's refusal record; heldBy is
// the other folder whose refusal held this push ("" = its own refusal).
func (b *storePush) recordRefusal(ctx context.Context, refused []store.Refusal, heldBy string) {
	rec := StorePushRefusal{Store: string(b.name), Code: string(refused[0].Code), Reason: refused[0].Reason,
		At: b.res.now().UTC().Format(time.RFC3339), HeldBy: heldBy}
	seen := map[string]bool{}
	for _, r := range refused {
		if r.ID.IsZero() {
			continue // a held record names the rows of the folders that hold it
		}
		if id := r.ID.String(); !seen[id] {
			seen[id] = true
			rec.Rows = append(rec.Rows, id)
		}
	}
	sort.Strings(rec.Rows)
	if heldBy != "" {
		rec.HeldOn = heldFolders(refused)
	}
	st, err := openLocalState(ctx, b.res.home)
	if err != nil {
		return // the status line is lost, the commits are not
	}
	defer func() { _ = st.Close() }() // read-write handle of one small row; a close error loses nothing written
	_ = localstate.PutJSON(ctx, st, localstate.StorePushRefusals, rec.Store, rec)
}

// clearRefusal drops this store's refusal record, if any.
func (b *storePush) clearRefusal(ctx context.Context) {
	if !localStateExists(b.res.home) {
		return // nothing was ever recorded: never create local.db for a clear
	}
	st, err := openLocalState(ctx, b.res.home)
	if err != nil {
		return
	}
	defer func() { _ = st.Close() }()
	if _, ok, gerr := st.Get(ctx, localstate.StorePushRefusals, string(b.name)); gerr != nil || !ok {
		return
	}
	_ = st.Delete(ctx, localstate.StorePushRefusals, string(b.name))
}

// StorePushRefusals lists the stores whose last push the remote refused,
// by store name. A machine with no local state lists none.
func (a *App) StorePushRefusals(ctx context.Context) ([]StorePushRefusal, error) {
	if !localStateExists(a.opts.Home) {
		return nil, nil // never create local.db for a read
	}
	st, err := a.LocalState(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	entries, err := st.List(ctx, localstate.StorePushRefusals, "")
	if err != nil {
		return nil, err
	}
	out := make([]StorePushRefusal, 0, len(entries))
	for _, e := range entries {
		rec, ok, gerr := localstate.GetJSON[StorePushRefusal](ctx, st, localstate.StorePushRefusals, e.Key)
		if gerr != nil || !ok {
			continue // an unreadable record is skipped, not fatal for status
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Store < out[j].Store })
	return out, nil
}
