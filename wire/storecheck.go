package wire

// storecheck.go — the first half of `lore store doctor` (item 6): every
// store this repo and machine use (storeStandings: stores.yaml, the org's
// stores, the personal store `me`), each checked for what needs the
// person's attention:
//
//	folder ─▶ the folder holds the store marker?
//	git    ─▶ clone halves whole? ─▶ remote answers (ls-remote, the
//	          setup's route)? ─▶ unpushed commits (count, oldest) ─▶ a
//	          refused push waiting (local state) ─▶ last push (a note)
//
// It reads only: it never fetches, pushes, deletes or repairs.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/gitfault"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

var _ opsstores.StoreChecker = (*App)(nil)

// Store doctor problem texts.
const (
	checkNoMarker  = "the store folder holds no lore store marker (moved or deleted?); fix the url with `lore store set-url`, or remove the entry"
	checkNotCloned = "not cloned on this machine yet (the next command that reads it clones it)"
)

// CheckStores implements opsstores.StoreChecker.
func (a *App) CheckStores(ctx context.Context) []opsstores.StoreCheck {
	refusals := map[string]StorePushRefusal{}
	var own []StorePushRefusal
	if list, err := a.StorePushRefusals(ctx); err == nil {
		for _, r := range list {
			refusals[r.Store] = r
		}
		own, _ = splitHeldRecords(list)
	}
	now := a.Now()
	// a store's push mode, read only for a cloned store with waiting
	// commits (PushViews would open, and so clone, every store: doctor
	// only reads)
	modeOf := func(name string) string {
		v, err := a.StorePush(ctx, name)
		if err != nil {
			return ""
		}
		return v.Push
	}
	cloneOf := map[string]string{} // clone worktree -> the first store checked with it
	var out []opsstores.StoreCheck
	for _, s := range a.storeStandings(ctx) {
		if s.git && s.cloned && s.err == "" {
			if first, ok := cloneOf[s.dir]; ok {
				// H-16: a folder of a store shares its clone; its commits
				// and remote are the store's, checked once on its line
				// (one waiting commit was counted for the store, the folder
				// and the url)
				c := opsstores.StoreCheck{Store: s.entry.Name, Source: s.entry.Source, Kind: opsstores.StoreKindGit, URL: s.entry.URL,
					Clone: string(gitbackend.CloneWhole), Reachable: true, Notes: []string{sameCloneText(first)}, Shown: repourl.Mask(s.entry.URL)}
				out = append(out, c)
				continue
			}
			cloneOf[s.dir] = s.entry.Name
		}
		c := a.checkStore(ctx, s, now, modeOf)
		if s.git && s.cloned && s.err == "" {
			// G-19: a clone left at the store's old address (after
			// `lore store set-url`) is named, with what removes it
			if m, merr := rowfs.ReadMarker(s.dir); merr == nil && !m.ID.IsZero() {
				if line := leftClonesLine(leftClones(ctx, a.opts.Home, m.ID.String(), s.entry.URL), nil); line != "" {
					c.Notes = append(c.Notes, line)
				}
			}
		}
		if r, ok := refusals[s.entry.Name]; ok && r.HeldBy != "" {
			// decision 7: another folder's refusal held this push; the
			// refusal itself is that folder's problem
			on := heldOn(r, own)
			c.Refused = opsstores.PushRefusal{Code: r.Code, Reason: r.Reason, At: r.At, Rows: len(r.Rows), HeldBy: opsstores.HeldNames(on)}
			c.Problems = append(c.Problems, opsstores.HeldLines(on)+"; the commits wait in the local clone: "+heldNext(on))
		} else if ok {
			c.Refused = opsstores.PushRefusal{Code: r.Code, Reason: r.Reason, At: r.At, Rows: len(r.Rows)}
			c.Problems = append(c.Problems, fmt.Sprintf("its last push was refused at %s (%s: %s); the commits wait in the local clone: fix what the store's push rule names, then run `lore sync`", r.At, r.Code, r.Reason))
		}
		c.Shown = repourl.Mask(c.URL)
		out = append(out, c)
	}
	return out
}

// checkStore checks one store (refusals aside); modeOf reads a store's
// push mode ("" when push modes do not apply to it).
func (a *App) checkStore(ctx context.Context, s storeStanding, now time.Time, modeOf func(string) string) opsstores.StoreCheck {
	c := opsstores.StoreCheck{Store: s.entry.Name, Source: s.entry.Source, Kind: opsstores.StoreKindFolder, URL: s.entry.URL}
	if !s.git {
		if _, ok := markerVisibility(s.dir); ok && s.err == "" {
			c.Reachable = true
		} else {
			c.Problems = append(c.Problems, checkNoMarker)
		}
		return c
	}
	c.Kind = opsstores.StoreKindGit
	if s.err != "" && !s.cloned {
		c.Problems = append(c.Problems, s.err)
		return c
	}
	found := gitbackend.StateOf(s.dir, s.dir)
	if !isDirPath(found.Paths.GitDir) {
		c.Clone = cloneNotCloned
		c.Notes = append(c.Notes, checkNotCloned)
		return c
	}
	c.Clone = string(found.State)
	if found.State == gitbackend.CloneNotCheckedOut {
		c.Notes = append(c.Notes, "the first clone was cut short before its files were checked out; lore finishes it the next time it opens the store")
		return c
	}
	if found.State != gitbackend.CloneWhole {
		if f, ok := storeCloneFinding(found); ok {
			c.Problems = append(c.Problems, f.Message+" (fix: "+f.Fix+")")
		}
		return c
	}
	if err := gitbackend.ProbeRemote(ctx, s.dir, s.entry.URL, gitbackend.WithRoute(func(ctx context.Context, remote string) ([]string, []string) {
		return cloneRoute(ctx, a.opts.Home, a.projectDir(), remote)
	}), gitbackend.WithAccessNote(accessNoteFor(a.opts.Home))); err != nil {
		// the probe's whole sentence (what failed, at which masked url,
		// git's telling line); a fault waiting cannot fix (no repository
		// there, refused credentials) also gets its next step: for no
		// answer the unpushed-commits problem below already says it
		c.Fault = string(gitbackend.ProbeFault(err))
		p := reasonOf(err)
		if c.Fault != string(gitfault.Unreachable) {
			p += "; " + probeHintOf(err)
		}
		c.Problems = append(c.Problems, p)
	} else {
		c.Reachable = true
	}
	// B2-26: the person's merge or rebase in the clone; every write and
	// sync is refused until it ends (gitbackend gitop.go), so say how
	if op, way := gitbackend.PersonGitOpAt(ctx, s.dir); op != "" {
		c.Problems = append(c.Problems, "the clone "+s.dir+" is in the middle of a git "+op+"; lore leaves it alone until it is finished: "+way)
	}
	if s.err != "" {
		c.Problems = append(c.Problems, s.err)
		return c
	}
	st := s.state
	c.Unpushed = st.Ahead
	if !st.OldestUnpushed.IsZero() {
		c.OldestUnpushed = st.OldestUnpushed.UTC().Format(time.RFC3339)
	}
	if !st.LastPush.IsZero() {
		c.LastPush = st.LastPush.UTC().Format(time.RFC3339)
	}
	if gitbackend.RewoundAt(s.dir) {
		// H-9: someone rewound the store branch; say what sync says.
		c.Problems = append(c.Problems, fmt.Sprintf("the store at %s was rewritten by someone (its branch moved back); this clone %s still holds %d commit(s) that are no longer on it and lore will not push them back on its own: put them back with `lore store push --restore --store %s`, or take the rewrite with `lore store forget --url %s --yes`, then `lore sync`", s.entry.URL, s.dir, st.Ahead, s.entry.URL, s.entry.URL))
	} else if gitbackend.RemoteResetAt(s.dir) {
		// B2-29: say what sync says; the waiting commits are not pushed
		// by the next command while the store stays reset.
		c.Problems = append(c.Problems, fmt.Sprintf("the store at %s was reset (deleted and made again); this clone %s still holds the old store (%d commit(s) not pushed) and lore will not push it there on its own: put it back with `lore store push --restore --store %s`, or start fresh with `lore store forget --url %s --yes`, then `lore store init --name <name> --url %s`", s.entry.URL, s.dir, st.Ahead, s.entry.URL, s.entry.URL, s.entry.URL))
	} else if st.Ahead > 0 {
		age := ""
		if !st.OldestUnpushed.IsZero() {
			age = ", the oldest " + storeAge(now.Sub(st.OldestUnpushed)) + " old"
		}
		mode := modeOf(s.entry.Name)
		switch {
		case heldMode(mode) && c.Reachable:
			// H-16: under push manual or batch, waiting commits are the
			// mode working as set (the write said "lore sync sends it"),
			// not a problem; nothing else pushes them soon, so say what does
			c.Notes = append(c.Notes, fmt.Sprintf("%d commit(s) not sent yet%s; push %s: `lore sync` sends them", st.Ahead, age, mode))
		case heldMode(mode):
			c.Problems = append(c.Problems, fmt.Sprintf("%d commit(s) not sent yet%s; push %s: `lore sync` sends them once the store answers", st.Ahead, age, mode))
		default:
			c.Problems = append(c.Problems, fmt.Sprintf("%d commit(s) not pushed%s; the next lore command that reaches the store pushes them, or run `lore sync`", st.Ahead, age))
		}
	}
	c.Notes = append(c.Notes, standingText(s, time.Time{})) // no relative ages: the same text on every run
	return c
}

// heldMode reports whether a store's push mode holds commits until a
// sync (manual) or until its interval passed (batch): no write's push
// sends them at once, so "the next command pushes them" would be false.
func heldMode(mode string) bool {
	return mode == string(storepush.ModeManual) || mode == string(storepush.ModeBatch)
}

// cloneNotCloned is StoreCheck.Clone for a git store with no clone here.
const cloneNotCloned = "not_cloned"

// isDirPath reports whether p is a folder.
func isDirPath(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// probeHintOf is err's hint ("" when it has none).
func probeHintOf(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}

// reasonOf is err's message without its [E_CODE] prefix (a doctor line
// reads as a sentence).
func reasonOf(err error) string {
	var e *errs.Error
	if !errors.As(err, &e) {
		return err.Error()
	}
	if e.Cause != nil && !strings.Contains(e.Message, e.Cause.Error()) {
		return e.Message + ": " + reasonOf(e.Cause)
	}
	return e.Message
}
