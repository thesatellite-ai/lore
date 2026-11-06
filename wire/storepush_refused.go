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
//	    landed      ─▶ clear   (not offline, nothing refused, no error)
//	App.StorePushRefusals ─▶ lore sync status lines + data.store_refusals
//
// The rows are the ids the backend names in its refusals (the git backend
// lists the rows of the refused range); a refusal with no id means the
// whole push. Recording never fails the command: local state that cannot
// be opened only loses the status line.

import (
	"context"
	"os"
	"sort"
	"time"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
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
}

// observeSync records or clears this store's refused push after one
// inner Sync: refused rows are recorded; a sync that reached the remote
// with nothing refused clears the record (the waiting commits landed).
// An error or an offline sync says nothing new and changes nothing.
func (b *storePush) observeSync(ctx context.Context, rep store.SyncReport, err error) {
	if err != nil || b.res == nil || b.res.home == "" {
		return
	}
	switch {
	case len(rep.Refused) > 0:
		b.recordRefusal(ctx, rep.Refused)
	case !rep.Offline:
		b.clearRefusal(ctx)
	}
}

// recordRefusal stores refused as this store's refusal record.
func (b *storePush) recordRefusal(ctx context.Context, refused []store.Refusal) {
	rec := StorePushRefusal{Store: string(b.name), Code: string(refused[0].Code), Reason: refused[0].Reason,
		At: b.res.now().UTC().Format(time.RFC3339)}
	seen := map[string]bool{}
	for _, r := range refused {
		if r.ID.IsZero() {
			continue
		}
		if id := r.ID.String(); !seen[id] {
			seen[id] = true
			rec.Rows = append(rec.Rows, id)
		}
	}
	sort.Strings(rec.Rows)
	st, err := sqlite.OpenLocalState(ctx, LocalStatePath(b.res.home))
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
	st, err := sqlite.OpenLocalState(ctx, LocalStatePath(b.res.home))
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

// localStateExists reports whether this machine's local.db exists.
func localStateExists(home string) bool {
	_, err := os.Stat(LocalStatePath(home))
	return err == nil
}
