package wire

// rosterhold.go — names the changes every reader holds back (devco R-5,
// R-6, R-18; the check is adapters/backend/git authority.go): a roster
// change whose commit author is not an admin, or any change by a person
// already marked as left. Reads serve the last accepted version; `lore
// sync` and `lore doctor` say which row, which commit, who and why, so a
// hand-made change is never silently believed and never silently lost.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
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
	// dir is the clone's folder on this machine, for the `git -C <dir>
	// show <commit>` an id's not-found answer prints (heldNotFound).
	dir string
}

// heldChanges lists the held files of every attached git store, once
// per clone and file. A folder that opens no store (the home folder, a
// code repo with no lore project) lists the holds of the setup's stores
// cloned on this machine (setupHeldChanges): before (devco:D-858), doctor
// there skipped every store check and a hold showed only in sync.
func (a *App) heldChanges(ctx context.Context) []heldChange {
	if _, err := a.state(ctx); err != nil {
		return a.setupHeldChanges(ctx)
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
			out = append(out, heldChange{src: n, held: h, url: g.RemoteURL(), folder: folders[n], dir: g.Dir()})
		}
	}
	return out
}

// setupHeldChanges is heldChanges over the setup's stores when the folder
// here opens none: each store this machine already holds a clone of, on
// an App of its own (as setupMemberHistory reads them). Where no setup
// covers the folder (the home folder) and none was named, the stores of
// every enabled org setup are read: a hold in any store this machine
// keeps is named, not only in the folders one setup covers. A store with
// no clone here is skipped, never cloned: a check or a not-found answer
// must not fetch a store. Each clone is read once.
func (a *App) setupHeldChanges(ctx context.Context) []heldChange {
	ctxs := []context.Context{ctx}
	if !setupNamed(ctx) {
		for _, name := range a.orgSetups() {
			ctxs = append(ctxs, op.WithSetup(ctx, name))
		}
	}
	read := map[string]bool{} // clone folders already read
	var out []heldChange
	for _, sctx := range ctxs {
		stores, _, err := a.setupStoresShown(sctx)
		if err != nil {
			continue // a setup whose stores cannot be listed names no hold
		}
		for _, one := range stores {
			dir, _, derr := gitbackend.CloneDirOf(a.opts.Home, one.URL, one.Branch)
			if derr != nil || read[dir] {
				continue // not a git store (a folder on this machine), or read already
			}
			read[dir] = true
			if _, serr := os.Stat(filepath.Join(dir, ".git")); serr != nil {
				continue // no clone here yet
			}
			x, oerr := openStoreApp(sctx, a.opts, orgOfURL(a.opts.Home, one.URL), one)
			if oerr != nil {
				continue // a store that cannot be opened holds nothing it can name
			}
			out = append(out, x.heldChanges(sctx)...)
			_ = x.Close()
		}
	}
	return out
}

// heldNotFound turns an E_NOT_FOUND for a row id that a store this
// command reads holds back into an answer naming the hold (store, row,
// commit, author, why) and the git command that shows the commit; any
// other error is returned as it is. It runs on every path of App.Run (a
// code repo, a repo with no lore project, the home folder). Why (devco:D-858): `lore rule show
// --id <held id>` answered E_NOT_FOUND with "it may have landed on main",
// pointing at a branch while the row sat in the store's clone, held back
// by every reader. The code stays E_NOT_FOUND (the row is not readable);
// the hint is set, so the branch hint of the live hook is never added.
func (a *App) heldNotFound(ctx context.Context, err error) error {
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.NotFound || !looksLikeRowIDIn(e.Message) {
		return err
	}
	return heldAnswer(err, a.heldChanges(ctx), func(h heldChange) string { return a.heldLine(ctx, h) })
}

// heldAnswer is heldNotFound's answer once the holds are read: err as it
// is unless it is an E_NOT_FOUND whose message names the row id of one of
// held, which then gives the hold's line (line) and its next step.
func heldAnswer(err error, held []heldChange, line func(heldChange) string) error {
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.NotFound {
		return err
	}
	for _, h := range held {
		if h.held.ID == "" || !strings.Contains(e.Message, h.held.ID) {
			continue
		}
		return errs.New(errs.NotFound, e.Message+": "+line(h)).WithHint(heldChangeNext(h) + "; `lore doctor` lists every held change")
	}
	return err
}

// looksLikeRowIDIn reports whether msg names something shaped like a row
// id (<prefix>_<hex>), so a not-found of anything else (a store, a
// setup, a file) never reads the stores' holds.
func looksLikeRowIDIn(msg string) bool {
	for _, w := range strings.FieldsFunc(msg, func(r rune) bool {
		return r == ' ' || r == '"' || r == '\'' || r == '`' || r == ',' || r == ':' || r == '(' || r == ')'
	}) {
		if _, err := ids.ParseAny(w); err == nil {
			return true
		}
	}
	return false
}

// heldChangeNext is the next step of one held change: rosterHeldNext with the
// clone's folder and the commit filled in, so the git command runs as
// printed (rosterHeldNext keeps the placeholders when the folder is not
// known).
func heldChangeNext(h heldChange) string {
	if h.dir == "" {
		return rosterHeldNext
	}
	return "an admin who agrees makes the change with lore (or reverts the commit) and the hold ends; `git -C " + h.dir + " show " + h.held.Short() + "` shows what it changed"
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
	return a.heldFindings(ctx, a.heldChanges(ctx)), nil
}

// heldFindings are doctor's findings for held.
func (a *App) heldFindings(ctx context.Context, held []heldChange) []DoctorFinding {
	var out []DoctorFinding
	for _, h := range held {
		out = append(out, DoctorFinding{Check: DoctorCheckHeldChanges, Level: DoctorWarning, Store: h.src,
			Message: a.heldLine(ctx, h) + "; " + heldChangeNext(h)})
	}
	return out
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
