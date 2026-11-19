package wire

// rosterhold.go — names the changes every reader holds back (devco R-5,
// R-6, R-18; the check is adapters/backend/git authority.go): a roster
// change whose commit author is not an admin, or any change by a person
// already marked as left. Reads serve the last accepted version; `lore
// sync` and `lore doctor` say which row, which commit, who and why, so a
// hand-made change is never silently believed and never silently lost.

import (
	"context"
	"sort"
	"strconv"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/ops/team"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckHeldChanges names the check.
const DoctorCheckHeldChanges DoctorCheck = "held_changes"

// rosterHeldNext is the next step of a held change.
const rosterHeldNext = "an admin who agrees makes the change with lore (or reverts the commit) and the hold ends; `git -C <store clone> show <commit>` shows what it changed"

// heldChange is one held file with the store it was read from.
type heldChange struct {
	src  store.Source
	held gitbackend.HeldChange
	// url and folder are the clone the hold was read from (its remote)
	// and the folder src attaches, as this process opened them. Why
	// (devco T-23): the line named the store by src's place in the org
	// resolution made after sync fetched new links, so a team-store hold
	// read through "globex" was named under the client store "globex"
	// meant after the fetch.
	url, folder string
}

// heldChanges lists the held files of every attached git store, once
// per clone and file.
func (a *App) heldChanges(ctx context.Context) []heldChange {
	if _, err := a.state(ctx); err != nil {
		return nil
	}
	a.mu.Lock()
	if a.st == nil {
		a.mu.Unlock()
		return nil
	}
	backends := make(map[store.Source]store.Backend, len(a.st.byName))
	folders := make(map[store.Source]string, len(a.st.byName))
	for n, b := range a.st.byName {
		backends[n] = b
		folders[n] = a.st.folders[n]
	}
	a.mu.Unlock()
	names := make([]store.Source, 0, len(backends))
	for n := range backends {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	seen := map[string]bool{}
	var out []heldChange
	for _, n := range names {
		g, ok := concrete(backends[n]).(*gitbackend.Backend)
		if !ok || !g.CloneMode() {
			continue
		}
		held, herr := g.Held(ctx)
		if herr != nil {
			continue
		}
		for _, h := range held {
			key := g.Dir() + "\x00" + h.Path
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, heldChange{src: n, held: h, url: g.RemoteURL(), folder: folders[n]})
		}
	}
	return out
}

// heldLine is the one line of a held change.
func (a *App) heldLine(ctx context.Context, h heldChange) string {
	return "held back in " + a.heldStoreName(ctx, h) + ": " + h.held.Table + " " + h.held.ID + " (commit " + h.held.Short() + " by " + h.held.Author + "): " + h.held.Reason
}

// heldStoreName names the store a hold was read from by its clone's own
// remote (storeOwnName), never by where the attached name points after a
// later link fetch; a clone no org lists keeps the attached name.
func (a *App) heldStoreName(ctx context.Context, h heldChange) string {
	if h.url == "" {
		return a.shownStoreName(ctx, string(h.src))
	}
	return storeShown(string(h.src), storeOwnName(a.opts.Home, h.url, h.folder))
}

// heldSyncLines are the held-change lines a sync prints after the store
// lines: one per change, then the next step once.
func (a *App) heldSyncLines(ctx context.Context) []string {
	held := a.heldChanges(ctx)
	if len(held) == 0 {
		return nil
	}
	lines := make([]string, 0, len(held)+1)
	for _, h := range held {
		lines = append(lines, a.heldLine(ctx, h))
	}
	return append(lines, strconv.Itoa(len(held))+" change(s) held back, not applied: "+rosterHeldNext)
}

// DoctorHeldChanges is the check (registered in doctor_op.go).
func (a *App) DoctorHeldChanges(ctx context.Context) ([]DoctorFinding, error) {
	var out []DoctorFinding
	for _, h := range a.heldChanges(ctx) {
		out = append(out, DoctorFinding{Check: DoctorCheckHeldChanges, Level: DoctorWarning, Store: h.src,
			Message: a.heldLine(ctx, h) + "; " + rosterHeldNext})
	}
	return out, nil
}

var _ team.MemberHistory = (*App)(nil)

// MemberHistory implements team.MemberHistory: the person's commits in
// every attached git store clone (once per clone), newest first.
func (a *App) MemberHistory(ctx context.Context, emails []string) ([]team.HistoryEntry, error) {
	if _, err := a.state(ctx); err != nil {
		// a folder lore does not read (a repo with no .lore, no repo at
		// all): the stores of the setup, each opened alone, as the
		// setup's sync opens them
		return a.setupMemberHistory(ctx, emails)
	}
	return a.historyOfOpened(ctx, emails)
}

// historyOfOpened reads the history of the stores this App opened (no
// fallback: a store's own App never goes back to the setup).
func (a *App) historyOfOpened(ctx context.Context, emails []string) ([]team.HistoryEntry, error) {
	a.mu.Lock()
	if a.st == nil {
		a.mu.Unlock()
		return nil, nil
	}
	backends := make(map[store.Source]store.Backend, len(a.st.byName))
	for n, b := range a.st.byName {
		backends[n] = b
	}
	a.mu.Unlock()
	names := make([]store.Source, 0, len(backends))
	for n := range backends {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	seen := map[string]bool{}
	var out []team.HistoryEntry
	for _, n := range names {
		g, ok := concrete(backends[n]).(*gitbackend.Backend)
		if !ok || !g.CloneMode() || seen[g.Dir()] {
			continue
		}
		seen[g.Dir()] = true
		list, err := g.CommitsBy(ctx, emails)
		if err != nil {
			return nil, err
		}
		shown, _, _ := strings.Cut(a.shownStoreName(ctx, string(n)), targetSep)
		for _, c := range list {
			out = append(out, team.HistoryEntry{Commit: c.Commit, At: c.At, Store: shown, Subject: c.Subject, Files: c.Files, Held: c.Held})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out, nil
}

// setupMemberHistory is MemberHistory over the setup's stores when the
// folder here opens none (each store on an App of its own).
func (a *App) setupMemberHistory(ctx context.Context, emails []string) ([]team.HistoryEntry, error) {
	stores, _, err := a.setupStoresShown(ctx)
	if err != nil {
		return nil, err
	}
	var out []team.HistoryEntry
	for _, one := range stores {
		x, oerr := openStoreApp(ctx, a.opts, orgOfURL(a.opts.Home, one.URL), one)
		if oerr != nil {
			continue // a store that cannot be opened has no history to add
		}
		if _, serr := x.state(ctx); serr != nil {
			_ = x.Close()
			continue
		}
		list, herr := x.historyOfOpened(ctx, emails)
		_ = x.Close()
		if herr != nil {
			return nil, herr
		}
		out = append(out, list...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out, nil
}
