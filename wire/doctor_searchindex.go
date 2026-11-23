package wire

// doctor_searchindex.go — the search_index check (devco D-920, hunt H-178).
//
// Search, ask and sql read each store's cache; show reads the row file. A
// cache filled before D-920 kept a newer copy of a row over a file that
// went back to an older one (a branch switch, `git checkout` of a row
// file, a pull of a version written under a clock that was behind), and
// its cursor had moved past the file, so no later refresh read it again:
// search and ask answered with a text no file held while `lore show --id`
// showed the file. The refresh no longer keeps such a copy
// (store.CurrentCheck), but a cache that already holds one keeps it until
// it is rebuilt, so doctor compares every store's cache with its files:
//
//	per attached store: the versions the cache holds (HeldVersions)
//	  vs the rows its backend holds now (Engine.CurrentRows)
//	  a live cached row whose file has another version
//	  ─▶ one warning per store; --fix rebuilds that store's cache
//	     (what `lore search rebuild --store <store>` does)

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckSearchIndex is the search_index check's JSON name.
const DoctorCheckSearchIndex DoctorCheck = "search_index"

// staleIndexSample is how many stale row ids a finding names.
const staleIndexSample = 3

// DoctorSearchIndex reports every attached store whose cache (the search
// index) holds a row version its files do not; fix rebuilds those caches
// from the row files.
func (a *App) DoctorSearchIndex(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, nil // the store checks report an open that fails
	}
	st.caches.mu.Lock()
	order := append([]store.Source(nil), st.caches.order...)
	st.caches.mu.Unlock()
	var out []DoctorFinding
	for _, src := range order {
		stale, err := a.staleIndexRows(ctx, st, src)
		if err != nil {
			continue // a store that cannot be read is the store checks' to report
		}
		var old []store.Source
		if c, cerr := st.caches.of(src); cerr == nil {
			old = st.caches.leftoverSources(ctx, c) // #142: rows under an earlier attachment name
		}
		if len(stale) == 0 && len(old) == 0 {
			continue
		}
		msg := staleIndexMessage(src, stale)
		if len(stale) == 0 {
			msg = leftoverIndexMessage(ctx, st, src, old[0])
		}
		f := DoctorFinding{Check: DoctorCheckSearchIndex, Level: DoctorWarning, Store: src,
			Message: msg, Fix: rebuildCommand(src) + " (or lore doctor --fix)"}
		if fix {
			if err := st.caches.Rebuild(ctx, src); err == nil {
				if _, err := st.eng.Refresh(ctx, src); err == nil {
					f.Fixed = true
					f.Message = "rebuilt the search index of " + string(src) + " from its row files"
				}
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// staleIndexRows lists the ids of the live rows src's cache holds in
// another version than its backend has now.
func (a *App) staleIndexRows(ctx context.Context, st *state, src store.Source) ([]string, error) {
	c, err := st.caches.of(src)
	if err != nil {
		return nil, err
	}
	held, err := c.HeldVersions(ctx, st.caches.keyOf(src))
	if err != nil {
		return nil, err
	}
	rows, err := st.eng.CurrentRows(ctx, src)
	if err != nil {
		return nil, err
	}
	now := make(map[store.Key]store.Row, len(rows))
	for _, r := range rows {
		now[r.Key()] = r
	}
	var stale []string
	for k, h := range held {
		if h.Deleted {
			continue // a tombstone shows nothing in search or ask
		}
		// A file gone without a tombstone is not a delete (rowfs Missing,
		// reported by its own checks): only a file in another version counts.
		if r, ok := now[k]; ok && r.HLC.Compare(h.HLC) != 0 {
			stale = append(stale, k.ID.String())
		}
	}
	return stale, nil
}

// leftoverIndexMessage is the finding's line for rows src's cache file
// holds under old, a name no attachment uses any more: how many of src's
// ids it holds twice (devco D-930, hunt H-188), or how many rows it holds
// when the counts cannot be read.
func leftoverIndexMessage(ctx context.Context, st *state, src, old store.Source) string {
	c, err := st.caches.of(src)
	if err != nil {
		return "the search index of " + string(src) + " still holds rows under the name " + string(old) + ", which no attachment uses any more"
	}
	mine, merr := c.HeldVersions(ctx, st.caches.keyOf(src))
	theirs, terr := c.HeldVersions(ctx, old)
	if merr != nil || terr != nil {
		return "the search index of " + string(src) + " still holds rows under the name " + string(old) + ", which no attachment uses any more"
	}
	twice := 0
	for k := range theirs {
		if _, ok := mine[k]; ok {
			twice++
		}
	}
	why := "which no attachment uses any more (the folder was attached under that name before)"
	if strings.HasPrefix(string(st.caches.keyOf(src)), folderKeyPrefix) {
		why = "an older lore's copy, kept by the name the folder was attached under (lore now keeps one copy per folder)"
	}
	return "the search index of " + string(src) + " holds " + plural(twice, "id") + " twice: also under the name " + string(old) + " (" + plural(len(theirs), "row") + "), " + why + ": search and ask find their old text"
}

// staleIndexMessage is the finding's line for src's stale rows.
func staleIndexMessage(src store.Source, stale []string) string {
	sample := stale
	if len(sample) > staleIndexSample {
		sample = sample[:staleIndexSample]
	}
	msg := "the search index of " + string(src) + " holds " + plural(len(stale), "row") + " in another version than their row files (search and ask show that text, lore show --id the file): "
	for i, id := range sample {
		if i > 0 {
			msg += ", "
		}
		msg += id
	}
	if len(stale) > len(sample) {
		msg += ", …"
	}
	return msg
}

// rebuildCommand is the command that rebuilds src's cache, as it runs
// where doctor ran: --store names every store search takes; the machine
// store is rebuilt with the rest.
func rebuildCommand(src store.Source) string {
	if src == store.SourceMachine {
		return "lore search rebuild"
	}
	return "lore search rebuild --store " + string(src)
}
