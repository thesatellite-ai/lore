package host

// live_notfound.go — v0.1.17's F1 answer for an id a command did not find
// (saas/cmd/cli live_flags.go liveMissingExplain): a live tombstone says
// "deleted (live, <date>)", an id purged on live or on origin/<default>
// says "deleted on purpose", anything else gets the `lore sync peek` hint
// (it may have landed on the default branch while this branch is older).

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/errs"
)

// missingPersonalStore is the engine's not-found text for `--to me` with
// no personal store attached (engine.Attached: "no store named me is
// attached").
const missingPersonalStore = "no store named " + orgconf.TargetMe + " is attached"

// personalStoreHint is what a person runs before `--to me` works.
const personalStoreHint = "your personal store is not set up: run `lore store init me` (a local folder) or `lore store init me <url>`, then run the command again"

// purgedFileName is the branch's list of ids deleted on purpose.
const purgedFileName = "_purged.json"

// explainNotFound is LiveHook's OnError.
func (h *liveHost) explainNotFound(ctx context.Context, run *Run, err error) error {
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.NotFound || e.Hint != "" {
		return err
	}
	if strings.Contains(e.Message, missingPersonalStore) {
		return e.WithHint(personalStoreHint) // the TARGET is missing, not the row
	}
	if len(run.Args) == 0 || !looksLikeRowID(run.Args[0]) {
		return err
	}
	id := run.Args[0]
	if !strings.Contains(e.Message, id) {
		return err // not-found of something else (a target store, a parent): the row was found
	}
	noun, _, _ := strings.Cut(run.Entry.Spec().Name, ".")
	table, idKnown := tableOfID(id)
	nounTable, nounKnown := tableOfNoun(noun)
	switch {
	case idKnown && nounKnown && table != nounTable:
		return err // an id of another kind (`lore memory show tsk_…`): it cannot land as this noun
	case !idKnown && !nounKnown:
		return err // no built-in table to name in the hint (a dynamic kind): say nothing rather than guess
	case !idKnown:
		table = nounTable
	}
	remote, branch := defaultLiveRemote, defaultBranchFallback
	if root := liveProjectRoot(h.cfg.WorkDir); root != "" && !liveDisabled() {
		top, folder := livePlace(root)
		if repo, rerr := gitref.Open(ctx, top, h.git()); rerr == nil && !repo.Bare() {
			if s, serr := openLiveSession(ctx, h, repo, top, live.FetchCached); serr == nil {
				if code, why := s.whyMissingCode(ctx, folder, id); why != "" {
					explained := errs.New(code, why)
					if code == errs.Purged {
						explained = explained.WithHint(purgedHint)
					}
					return explained
				}
				remote, branch = s.ref.RemoteName, s.defaultBranch(ctx)
			}
		}
	}
	peek := "lore sync peek " + remote + "/" + branch + " " + table
	return e.WithHint("it may have landed on " + branch + " while this branch is older: `" + peek + "`")
}

// purgedHint is the hint of an E_PURGED answer, as the engine's purgedErr
// gives it.
const purgedHint = "write the content as a new row (a new id)"

// whyMissing is whyMissingCode's message.
func (s *liveSession) whyMissing(ctx context.Context, folder, id string) string {
	_, why := s.whyMissingCode(ctx, folder, id)
	return why
}

// whyMissingCode explains an id the live tree or origin/<default> knows
// about ("" when neither does) and gives its code: E_PURGED for an id on
// the live ref's purge list or main's _purged.json (DQ4: purged rows never
// come back; a `lore trash purge` lists its ids on the live ref too,
// BUG-LIVE-PURGE-LIST), checked first, since a purge wins over the
// tombstone it may leave; E_NOT_FOUND for a live tombstone. An unreadable
// purge list is reported (E_NOT_FOUND).
func (s *liveSession) whyMissingCode(ctx context.Context, folder, id string) (errs.Code, string) {
	if _, tree, err := s.ref.LocalTree(ctx); err == nil {
		purged, perr := tree.Purged()
		if perr != nil {
			return errs.NotFound, id + ": the live purge list could not be read (" + perr.Error() + ")"
		}
		if purged[id] {
			return errs.Purged, id + " was purged: deleted on purpose (`lore trash purge` or `lore live purge`); purged rows never come back (DQ4)"
		}
		rows, _ := tree.Rows(folder) // undecodable files say nothing about id
		for k, f := range rows {
			if k.ID == id && f.Deleted() {
				when := ""
				if !f.Block.HLC.Wall.IsZero() {
					when = ", " + f.Block.HLC.Wall.Format(time.DateOnly)
				}
				return errs.NotFound, id + " was deleted (live" + when + ")"
			}
		}
	}
	trk, ok := gitremote.TrackingRef(s.ref.RemoteName, "refs/heads/"+s.defaultBranch(ctx))
	if !ok {
		return errs.NotFound, ""
	}
	b, found, err := s.repo.ReadFile(ctx, trk, mainDataPath(folder, purgedFileName))
	if err == nil && found && strings.Contains(string(b), `"`+id+`"`) {
		return errs.Purged, id + " was purged: deleted on purpose (listed in main's _purged.json); purged rows never come back (DQ4)"
	}
	return errs.NotFound, ""
}

// looksLikeRowID: <prefix>_<hex…>. Only such an argument gets the live
// answer (a missing config key or a path is not a row that could land).
func looksLikeRowID(s string) bool {
	p, hex, ok := strings.Cut(strings.TrimSpace(s), "_")
	if !ok || p == "" || hex == "" {
		return false
	}
	for _, r := range p {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	for _, r := range hex {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
