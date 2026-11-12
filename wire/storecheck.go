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
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

var _ opsstores.StoreChecker = (*App)(nil)

// Store doctor problem texts.
const (
	checkNoMarker    = "the store folder holds no lore store marker (moved or deleted?); fix the url with `lore store set-url`, or remove the entry"
	checkUnreachable = "unreachable: "
	checkNotCloned   = "not cloned on this machine yet (the next command that reads it clones it)"
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
	var out []opsstores.StoreCheck
	for _, s := range a.storeStandings(ctx) {
		c := a.checkStore(ctx, s, now)
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

// checkStore checks one store (refusals aside).
func (a *App) checkStore(ctx context.Context, s storeStanding, now time.Time) opsstores.StoreCheck {
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
		c.Problems = append(c.Problems, checkUnreachable+reasonOf(err))
	} else {
		c.Reachable = true
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
	if st.Ahead > 0 {
		age := ""
		if !st.OldestUnpushed.IsZero() {
			age = ", the oldest " + storeAge(now.Sub(st.OldestUnpushed)) + " old"
		}
		c.Problems = append(c.Problems, fmt.Sprintf("%d commit(s) not pushed%s; the next lore command that reaches the store pushes them, or run `lore sync`", st.Ahead, age))
	}
	c.Notes = append(c.Notes, standingText(s, time.Time{})) // no relative ages: the same text on every run
	return c
}

// cloneNotCloned is StoreCheck.Clone for a git store with no clone here.
const cloneNotCloned = "not_cloned"

// isDirPath reports whether p is a folder.
func isDirPath(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
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
