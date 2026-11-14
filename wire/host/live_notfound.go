package host

// live_notfound.go — v0.1.17's F1 answer for an id a command did not find
// (saas/cmd/cli live_flags.go liveMissingExplain): a live tombstone says
// "deleted (live, <date>)", an id purged on live or on origin/<default>
// says "deleted on purpose", anything else gets the `lore sync peek` hint
// (it may have landed on the default branch while this branch is older).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// missingPersonalStore is the engine's not-found text for `--to me` with
// no personal store attached (engine.Attached: "no store named me is
// attached").
const missingPersonalStore = "no store named " + orgconf.TargetMe + " is attached"

// personalStoreHint is what a person runs before `--to me` works.
const personalStoreHint = "your personal store is not set up: run `lore store init --name me` (a local folder) or `lore store init --name me --url <url>`, then run the command again"

// firstArg is the operation's first positional argument as the caller gave
// it: on the CLI the decoded value of that field (by its flag, `--id
// tsk_…`, or by position: U-4), or over `lore api` (no Args) the JSON member of the
// input field at argument 0 (the {id} of the route, rest.input). Without
// the REST form a request for a purged or not-yet-landed id answered a bare
// E_NOT_FOUND while the CLI said E_PURGED or gave the `lore sync peek`
// hint (ONE OPERATION SPEC). "" when there is none.
func firstArg(run *Run) string {
	if _, ok := run.Decoder.(op.ArgsDecoder); ok {
		return decodedFirstArg(run) // U-4: given as --id or by position
	}
	if len(run.Args) > 0 {
		return run.Args[0]
	}
	d, ok := run.Decoder.(op.JSONDecoder)
	if !ok {
		return ""
	}
	for _, f := range run.Entry.InputFields() {
		if f.Arg != 0 {
			continue
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(d.Body, &body) != nil {
			return ""
		}
		var v string
		if json.Unmarshal(body[f.Name], &v) != nil {
			return ""
		}
		return v
	}
	return ""
}

// decodedFirstArg is the value of the operation's first positional field
// as decoded from the CLI flags (`lore task show --id tsk_…`, U-4), "" when
// it has none or the input does not decode.
func decodedFirstArg(run *Run) string {
	name := ""
	for _, f := range run.Entry.InputFields() {
		if f.Arg == 0 {
			name = f.Name
		}
	}
	if name == "" {
		return ""
	}
	in, err := run.Entry.Decode(run.Decoder)
	if err != nil {
		return ""
	}
	for _, tv := range in.Texts() {
		if tv.Field == name {
			return tv.Text
		}
	}
	return ""
}

// idFlags are the input fields (flag, JSON name) that take another row's
// id: `comment add --on-id`, `link add --entity`, `handoff add --task`,
// `task add --parent`. A not-found id given there gets the same answer as
// a positional one.
var idFlags = []struct{ flag, json string }{
	{"on-id", "on_id"}, {"entity", "entity"}, {"task", "task"}, {"parent", "parent"},
}

// flagArgs are the id-taking flags the caller set, in idFlags order: the
// CLI's changed flags, or over `lore api` the JSON members of those names.
func flagArgs(run *Run) []string {
	var out []string
	if run.Cmd != nil {
		for _, f := range idFlags {
			if fl := run.Cmd.Flags().Lookup(f.flag); fl != nil && fl.Changed {
				out = append(out, fl.Value.String())
			}
		}
		return out
	}
	d, ok := run.Decoder.(op.JSONDecoder)
	if !ok {
		return nil
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(d.Body, &body) != nil {
		return nil
	}
	for _, f := range idFlags {
		var v string
		if json.Unmarshal(body[f.json], &v) == nil && v != "" {
			out = append(out, v)
		}
	}
	return out
}

// missingID is the id the not-found message names: the first positional
// argument, else an id-taking flag's value (fromFlag), "" when none is a
// row id the message names.
func missingID(run *Run, msg string) (id string, fromFlag bool) {
	if a := firstArg(run); looksLikeRowID(a) && strings.Contains(msg, a) {
		return a, false
	}
	for _, v := range flagArgs(run) {
		if looksLikeRowID(v) && strings.Contains(msg, v) {
			return v, true
		}
	}
	return "", false
}

// purgedFileName is the branch's list of ids deleted on purpose.
const purgedFileName = "_purged.json"

// explainNotFound is LiveHook's OnError.
func (h *liveHost) explainNotFound(ctx context.Context, run *Run, err error) error {
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.NotFound {
		return err
	}
	// The engine's text may sit under a wrapper (an implicit write to the
	// personal store, such as the owner's actor row for --scope global,
	// wraps it), so the whole chain is searched, not the outer message.
	// It replaces the engine's generic list of attached places: setting the
	// personal store up is the way out, not another --to.
	if strings.Contains(err.Error(), missingPersonalStore) {
		return e.WithHint(personalStoreHint) // the TARGET is missing, not the row
	}
	if e.Hint != "" {
		return err
	}
	// The id the message names: the positional one, or one given by an
	// id-taking flag (--on-id, --entity, --task, --parent). Neither: a
	// not-found of something else (a target store): say nothing.
	id, fromFlag := missingID(run, e.Message)
	if id == "" {
		return err
	}
	noun, _, _ := strings.Cut(run.Entry.Spec().Name, ".")
	table, idKnown := tableOfID(id)
	nounTable, nounKnown := tableOfNoun(noun)
	if fromFlag {
		nounTable, nounKnown = table, idKnown // a flag's id is of its own kind, not the command's noun
	}
	switch {
	case idKnown && nounKnown && table != nounTable:
		return err // an id of another kind (`lore memory show tsk_…`): it cannot land as this noun
	case !idKnown && !nounKnown:
		return err // no built-in table to name in the hint (a dynamic kind): say nothing rather than guess
	case !idKnown:
		table = nounTable
	}
	remote, branch := defaultLiveRemote, defaultBranchFallback
	upToDate := false
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
				// H-7, H-14: a row file git still has off this branch's
				// tip (a git rm, a force-push or a rebase that dropped
				// it) gets the git command that brings it back.
				if why := rowFileHint(ctx, repo, id, mainDataPath(folder, table+"/"+id+rowFileExt)); why != "" {
					return e.WithHint(why)
				}
				remote, branch = s.ref.RemoteName, s.defaultBranch(ctx)
				upToDate = holdsBranch(ctx, repo, remote, branch)
			}
		}
	}
	peek := "lore sync peek --ref " + remote + "/" + branch + " --table " + table
	if upToDate {
		// A-15: this branch already holds everything on the default
		// branch as last fetched, so "while this branch is older" is
		// wrong; the usual cause is a row of another repo, and a row
		// pushed since that fetch is the other (peek fetches).
		return e.WithHint("this branch has everything on " + branch + " as last fetched: the row may belong to another repo (a row lives in one repo's notes: run the command there), or be newer than that fetch: `" + peek + "`" + storeRowNotYetHint)
	}
	return e.WithHint("it may have landed on " + branch + " while this branch is older: `" + peek + "`" + storeRowNotYetHint)
}

// storeRowNotYetHint ends a not-found hint (devco D-221): a row a teammate
// wrote to a store stays committed on their machine until their `lore
// sync`, and reaches this one with this machine's `lore sync`; the hint
// named only the code branch, so a store row looked lost.
const storeRowNotYetHint = "; a row a teammate wrote to a store arrives once they and you run `lore sync`"

// holdsBranch reports whether HEAD already contains remote/branch as
// last fetched (that tip is an ancestor of HEAD, or HEAD itself). False
// when either is unknown: the older-branch hint then stays.
func holdsBranch(ctx context.Context, repo *gitref.Repo, remote, branch string) bool {
	tip, ok, err := repo.ResolveRef(ctx, "refs/remotes/"+remote+"/"+branch)
	if err != nil || !ok {
		return false
	}
	head, ok, err := repo.ResolveCommit(ctx, "HEAD")
	if err != nil || !ok {
		return false
	}
	if tip == head {
		return true
	}
	anc, err := repo.IsAncestor(ctx, tip, head)
	return err == nil && anc
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
