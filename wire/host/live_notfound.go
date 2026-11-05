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
	"github.com/thesatellite-ai/lore/model/errs"
)

// nounTables are the command nouns whose table is not the plain plural.
var nounTables = map[string]string{
	"memory": "memories", "hotfix": "hotfixes", "tasklist": "task_lists",
}

// purgedFileName is the branch's list of ids deleted on purpose.
const purgedFileName = "_purged.json"

// explainNotFound is LiveHook's OnError.
func (h *liveHost) explainNotFound(ctx context.Context, run *Run, err error) error {
	var e *errs.Error
	if len(run.Args) == 0 || !errors.As(err, &e) || e.Code != errs.NotFound || e.Hint != "" || !looksLikeRowID(run.Args[0]) {
		return err
	}
	id := run.Args[0]
	noun, _, _ := strings.Cut(run.Entry.Spec().Name, ".")
	table := nounTables[noun]
	if table == "" {
		table = strings.ReplaceAll(noun, "-", "_") + "s"
	}
	remote, branch := defaultLiveRemote, defaultBranchFallback
	if root := liveProjectRoot(h.cfg.WorkDir); root != "" && !liveDisabled() {
		top, folder := livePlace(root)
		if repo, rerr := gitref.Open(ctx, top, h.git()); rerr == nil && !repo.Bare() {
			if s, serr := openLiveSession(ctx, h, repo, top, live.FetchCached); serr == nil {
				if why := s.whyMissing(ctx, folder, id); why != "" {
					return errs.New(errs.NotFound, why)
				}
				remote, branch = s.ref.RemoteName, s.defaultBranch(ctx)
			}
		}
	}
	peek := "lore sync peek " + remote + "/" + branch + " " + table
	return e.WithHint("it may have landed on " + branch + " while this branch is older: `" + peek + "`")
}

// whyMissing explains an id the live tree or origin/<default> knows about
// ("" when neither does). An unreadable purge list is reported.
func (s *liveSession) whyMissing(ctx context.Context, folder, id string) string {
	if _, tree, err := s.ref.LocalTree(ctx); err == nil {
		rows, _ := tree.Rows(folder) // undecodable files say nothing about id
		for k, f := range rows {
			if k.ID == id && f.Deleted() {
				when := ""
				if !f.Block.HLC.Wall.IsZero() {
					when = ", " + f.Block.HLC.Wall.Format(time.DateOnly)
				}
				return id + " was deleted (live" + when + ")"
			}
		}
		purged, perr := tree.Purged()
		if perr != nil {
			return id + ": the live purge list could not be read (" + perr.Error() + ")"
		}
		if purged[id] {
			return id + " was deleted on purpose (`lore live purge`)"
		}
	}
	trk, ok := gitremote.TrackingRef(s.ref.RemoteName, "refs/heads/"+s.defaultBranch(ctx))
	if !ok {
		return ""
	}
	b, found, err := s.repo.ReadFile(ctx, trk, mainDataPath(folder, purgedFileName))
	if err == nil && found && strings.Contains(string(b), `"`+id+`"`) {
		return id + " was deleted on purpose (listed in main's _purged.json)"
	}
	return ""
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
