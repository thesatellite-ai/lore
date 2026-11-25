package wire

// check_history_notes.go — the internal-note half of `lore check leaks
// --history` (devco D-994, bug hunt H-262, owner org questions (a)): the
// rules the commit hook and --as-client apply to the files of today (ids
// of internal rows, eight-word passages of internal notes, internal
// metadata values, rows of another project, session ids) applied to every
// file version and commit message of the history, in the same single pass
// that reads them for secrets (each version read once).
//
//	CheckHistory
//	  --as-client  ─▶ the --as-client index (every internal row of the
//	                  attached stores and the org's stores cloned here),
//	                  the repo read as its client's
//	  client repo  ─▶ the commit hook's index and text allows (F16)
//	  own repo     ─▶ secrets only (an own repo may hold its own notes);
//	                  the ✓ line says so and names --as-client
//	scanHistory    ─▶ per chunk: the secret scan, then historyNotes.run
//	                  over the same bodies; per finding the commit that
//	                  added it (the secret pass's one git log --raw)
//
// Why: a note committed to a repo and removed later is still in every
// clone: a repo handed to its client carries its whole history, and
// --as-client read only the current files. Nothing is ever printed of a
// note but its id and the rule (as every other refusal).

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/model/errs"
)

// historyNotes is the internal-note check of a --history run; nil checks
// secrets only.
type historyNotes struct {
	// run applies the rules to items (each item's Where is its blob id or
	// historyMsgWhere of its commit, so a finding maps back to them).
	run func(items []leakcheck.Item) []leakcheck.Finding
	// asClient names the --as-client reading in the lines and next steps.
	asClient bool
	explain  bool
	// unlisted is why a repo is a client repo only by the fail-closed
	// default and how an own repo stops being one (UnlistedOwnStep), as
	// the commit hook's refusal says it; "" otherwise.
	unlisted string
}

// historyNotesSkippedOwn is the ✓ line's note for an own repo read
// without --as-client: its own notes may be in it, so they are not
// findings, and the hand-over check is named.
const historyNotesSkippedOwn = "; internal notes not checked: this is an own repo (before handing it to a client: lore check leaks --history --as-client)"

// historyMsgWhere is the item name of commit sha's message.
func historyMsgWhere(sha string) string { return "msg:" + sha }

// historyNotesOf builds the note check of the repo at root: the
// --as-client index when asClient, else the commit hook's (a client repo,
// or one whose link is unknown); nil for an own repo or a repo lore is
// not set up in, with skipped the ✓ line's note for an own lore repo.
func (a *App) historyNotesOf(ctx context.Context, res *OrgResolution, root string, asClient, explain bool) (notes *historyNotes, skipped string, err error) {
	if asClient {
		return a.historyNotesAsClient(ctx, res, root, explain)
	}
	if res == nil {
		return nil, "", nil
	}
	set, err := a.leakDocsSetupOf(ctx, res)
	if err != nil {
		return nil, "", err
	}
	if set == nil {
		if s, serr := res.Situation(ctx); serr == nil && s.ProjectID != "" {
			skipped = historyNotesSkippedOwn
		}
		return nil, skipped, nil
	}
	opts, s := set.opts, set.situation
	return &historyNotes{explain: explain, unlisted: UnlistedOwnStep(UnlistedClientRepo(s.Result.Org, s.Result.Links, s.ClassRemotes())),
		run: func(items []leakcheck.Item) []leakcheck.Finding {
			return a.leakCheckByProject(ctx, items, set.idx, opts)
		}}, "", nil
}

// historyNotesAsClient is the --as-client reading: the index, project id,
// data prefix and session ids CheckAsClient uses, plus the text allows of
// the internal stores, so `lore leaks allow` lets an approved passage
// through in history as it does in a commit.
func (a *App) historyNotesAsClient(ctx context.Context, res *OrgResolution, root string, explain bool) (*historyNotes, string, error) {
	if r, err := a.knownRepoTop(ctx); err == nil && r != "" {
		root = r
	}
	pid := orgcfg.ProjectID(root)
	if pid == "" {
		if p, perr := a.Project(ctx); perr == nil && !p.IsZero() {
			pid = p.String()
		}
	}
	if pid == "" {
		return nil, "", errs.New(errs.InvalidInput, "check leaks --history --as-client: no lore project here; run from the project root").
			WithHint("lore init sets this repo up; lore check leaks --history alone checks any repo for secrets")
	}
	idx, allows, err := a.asClientIndexAllows(ctx, res)
	if err != nil {
		return nil, "", err
	}
	sessions, err := a.KnownToolSessionIDs(ctx)
	if err != nil {
		return nil, "", leakCannotCheckErr("this machine's session ids", err)
	}
	opts := leakcheck.Options{RepoProjectID: pid, ClientRepo: true, DataPrefix: asClientDataPrefix(gitTop(ctx, root), root),
		SessionIDs: sessions, TextAllows: allows}
	return &historyNotes{asClient: true, explain: explain, run: func(items []leakcheck.Item) []leakcheck.Finding {
		return leakcheck.Check(items, idx, opts)
	}}, "", nil
}

// historyNoteFinding is one internal-note finding in history: the path it
// was added at, the commit that first added it, how many file versions
// carry it and whether HEAD still holds it.
type historyNoteFinding struct {
	path, commit string
	f            leakcheck.Finding
	atHead       bool
	versions     int
	order        int
}

// historyNoteMsg is an internal-note finding in a commit message.
type historyNoteMsg struct {
	commit string
	f      leakcheck.Finding
}

// blobHits runs the note check over a chunk's text bodies; out maps a
// blob id to its blocking findings, allowed counts the findings a text
// allow let through.
func (n *historyNotes) blobHits(chunk []historyBlob, bodies [][]byte, out map[string][]leakcheck.Finding) (allowed int) {
	if n == nil {
		return 0
	}
	items := make([]leakcheck.Item, 0, len(chunk))
	for i, b := range chunk {
		if bodies[i] == nil || isBinaryDoc(bodies[i]) {
			continue
		}
		items = append(items, leakcheck.Item{Where: b.oid, Path: b.path, Content: bodies[i]})
	}
	for _, f := range n.run(items) {
		if f.Allowed.ID != "" {
			allowed++
		}
		if f.Warn {
			continue
		}
		out[f.Where] = append(out[f.Where], f)
	}
	return allowed
}

// msgHits runs the note check over the commit messages (sha → body).
func (n *historyNotes) msgHits(order []string, bodies map[string]string) (hits []historyNoteMsg, allowed int) {
	if n == nil || len(order) == 0 {
		return nil, 0
	}
	items := make([]leakcheck.Item, 0, len(order))
	for _, sha := range order {
		items = append(items, leakcheck.Item{Where: historyMsgWhere(sha), Content: []byte(bodies[sha])})
	}
	byWhere := map[string][]leakcheck.Finding{}
	for _, f := range n.run(items) {
		if f.Allowed.ID != "" {
			allowed++
		}
		if !f.Warn {
			byWhere[f.Where] = append(byWhere[f.Where], f)
		}
	}
	for _, sha := range order { // history order, newest first
		for _, f := range byWhere[historyMsgWhere(sha)] {
			hits = append(hits, historyNoteMsg{commit: sha, f: f})
		}
	}
	return hits, allowed
}

// historyNoteFindings turns the blob hits into one finding per note and
// place (path, rule, row, passage): the earliest commit that added it,
// its path there (HEAD's when HEAD holds it), the versions carrying it.
func historyNoteFindings(blobs []historyBlob, hits map[string][]leakcheck.Finding, added map[string]historyAdd, head map[string]string) []historyNoteFinding {
	type key struct {
		path, rule, row, detail string
		hash                    uint32
	}
	byKey := map[key]*historyNoteFinding{}
	for _, b := range blobs {
		fs, ok := hits[b.oid]
		if !ok {
			continue
		}
		ad := added[b.oid]
		p := b.path
		if ad.path != "" {
			p = ad.path
		}
		hp, inHead := head[b.oid]
		if inHead {
			p = hp
		}
		for _, f := range fs {
			f.Path = p
			k := key{p, string(f.Rule), f.RowID, f.Detail, f.Hash}
			nf, ok := byKey[k]
			if !ok {
				nf = &historyNoteFinding{path: p, f: f, commit: ad.commit, order: ad.order}
				byKey[k] = nf
			} else if ad.commit != "" && (nf.commit == "" || ad.order < nf.order) {
				nf.commit, nf.order, nf.f.Line = ad.commit, ad.order, f.Line
			}
			nf.versions++
			nf.atHead = nf.atHead || inHead
		}
	}
	out := make([]historyNoteFinding, 0, len(byKey))
	for _, f := range byKey {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].order != out[j].order {
			return out[i].order < out[j].order
		}
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		if out[i].f.Line != out[j].f.Line {
			return out[i].f.Line < out[j].f.Line
		}
		return out[i].f.RowID < out[j].f.RowID
	})
	return out
}

// historyNoteLines are the refusal lines of the note findings: one per
// note and place with the commit that added it, then the allow line of a
// text or metadata finding (`lore leaks allow ... --file <path>`).
func historyNoteLines(found []historyNoteFinding, msgs []historyNoteMsg, explain bool) []string {
	var lines []string
	for _, nf := range found {
		at := nf.path
		if nf.f.Line > 0 {
			at += ":" + strconv.Itoa(nf.f.Line)
		}
		commit := "<commit unknown>"
		if nf.commit != "" {
			commit = shortSHA(nf.commit)
		}
		state := "removed since, still in history"
		if nf.atHead {
			state = "still in the current files"
		}
		if nf.versions > 1 {
			state += fmt.Sprintf(", in %d versions of the file", nf.versions)
		}
		lines = append(lines, fmt.Sprintf("  %s %-40s internal: %s (%s)", commit, at, nf.f.Describe(explain), state))
		if l := historyNoteAllow(nf.f); l != "" {
			lines = append(lines, "      "+l)
		}
	}
	for _, m := range msgs {
		lines = append(lines, fmt.Sprintf("  %s %-40s internal: %s", shortSHA(m.commit), "commit message", m.f.Describe(explain)))
	}
	return lines
}

// historyNoteAllow is the `not internal?` line of a text or metadata
// finding: the `lore leaks allow` entry (with the file) the history check
// honours; "" for a rule no allow covers (an id, a row file).
func historyNoteAllow(f leakcheck.Finding) string {
	switch f.Rule {
	case leakcheck.RuleText:
		return "approved public text the note quotes? " + leakAllowCmd(f, LeakWhereCommit)
	case leakcheck.RuleExactToken, leakcheck.RuleKeyPair:
		return "a public metadata value of " + f.RowID + "? " + leakAllowCmd(f, LeakWhereCommit)
	}
	return ""
}

// historyNoteHint is the refusal's next step for note findings.
func historyNoteHint(n *historyNotes) string {
	return joinNonEmpty("; ", historyNoteNext(n.asClient), n.unlisted)
}

// historyNoteNext is what to do about a note in history.
func historyNoteNext(asClient bool) string {
	head := "an internal note in history: the client and anyone with a clone can read every commit, so removing it from the files does not remove it from history; " +
		"remove it from the current files and commit; rewriting the history without it (git filter-repo, then a force push every clone must follow) is your choice, lore never does it; "
	if asClient {
		head = "an internal note in history: the client receives every commit with the repo, so removing it from the files does not remove it from history; " +
			"remove it from the current files and commit; before the hand-over rewrite the history without it (git filter-repo, then a force push every clone must follow) or hand over a fresh history; lore never rewrites history; "
	}
	return head + "a passage or value that is public: run the `lore leaks allow` line printed under it"
}

// historyWhat is the summary's name for what was checked.
func historyWhat(notes bool) string {
	if notes {
		return "secret or internal note"
	}
	return "secret"
}

// joinNonEmpty joins the non-empty parts with sep.
func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
