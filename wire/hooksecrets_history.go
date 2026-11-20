package wire

// hooksecrets_history.go — the commit check blocks only secrets a commit
// ADDS (coordinator D2, bug B1-14). `lore fix` rewrites every row file into
// the new format and asks for one commit; a row that already held a
// secret-looking value at HEAD then shows up as a "new" finding although
// the value has been in the history all along, and the person cannot
// commit the migration at all.
//
//	staged file with findings ─▶ not a row: the same path at HEAD
//	(a doc line with a placeholder credential that every edit of the
//	page re-stages); a row ─▶ the same row at HEAD (same id: its old
//	path, or any path, so a moved row file counts too) ─▶ scanned with the
//	same scanner ─▶ a finding whose fingerprint (scan.Mask, the sha256
//	preview every refusal prints) the HEAD row already held is "in
//	history": counted, not blocked; anything else still blocks
//
// "HEAD" above is the commit the check compares with, historyBases: HEAD
// itself when it is pushed (or the repo has no remote at all), else the
// pushed commits HEAD's unpushed ones sit on (bug L-1). An amend replaces
// HEAD, so comparing with it let a secret already in the commit being
// amended (one committed with --no-verify) through as "not new"; nothing
// that never reached a remote counts as history now.
//
// Fails closed: when HEAD cannot be read (no commit yet, a git error),
// nothing is in history and every finding blocks as before. Only rows with
// findings cost anything: their old blobs are read in one git process per
// compared commit.

import (
	"context"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"

	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// rowFileSuffix ends every row file name (<id>.json).
const rowFileSuffix = ".json"

// historyNotice is the one line a commit check prints when it let row
// findings through because the same row already held them at HEAD;
// pushHistoryNotice is the push check's, for values the remote already
// held. A plain file's findings get fileHistoryLine instead (H-45).
const (
	historyNotice     = " secret-looking value(s) in this commit were already in the same row(s) at HEAD, not new, so they do not block it; list every stored one with `lore check leaks --rows`"
	pushHistoryNotice = " secret-looking value(s) in this push were already in the same row(s) on the remote, not new, so they do not block it; list every stored one with `lore check leaks --rows`"
)

// maxHistoryPlaces caps the file:line places one history line names.
const maxHistoryPlaces = 5

// historyHit is one finding a check let through as already in history
// (devco:D-788): a row file's findings are counted under the row wording;
// a plain file's are named by path and line with the ref that already
// holds the value, because "in the same row(s) at HEAD" told someone
// committing a code file (a Taskfile after a squash merge) neither which
// file nor that the reason was the remote.
type historyHit struct {
	Path string // the file (doc key) the finding is in
	Line int    // its line in the checked text
	Row  bool   // a row file: counted, not named
	Ref  string // the ref that already holds it: "origin/main", "tag v1.2"; "" for a commit with no remote ref (HEAD)
}

// historyLines are the notices of the findings of the action where let
// through as already in history: one count line for row findings (as
// before), one line per ref for plain files naming each file:line.
func historyLines(hs []historyHit, where string) []string {
	rows := 0
	var refs []string
	byRef := map[string][]string{}
	for _, h := range hs {
		if h.Row {
			rows++
			continue
		}
		if _, ok := byRef[h.Ref]; !ok {
			refs = append(refs, h.Ref)
		}
		byRef[h.Ref] = append(byRef[h.Ref], h.Path+":"+strconv.Itoa(h.Line))
	}
	var lines []string
	if rows > 0 {
		lines = append(lines, historyLine(rows, where))
	}
	for _, ref := range refs {
		lines = append(lines, fileHistoryLine(byRef[ref], ref, where))
	}
	return lines
}

// historyLine is the notice of n row findings of the action where let
// through as already in history.
func historyLine(n int, where string) string {
	if where == LeakWherePush {
		return "lore: " + strconv.Itoa(n) + pushHistoryNotice
	}
	return "lore: " + strconv.Itoa(n) + historyNotice
}

// fileHistoryLine is the notice of plain-file findings (places are
// "path:line") let through because ref already holds the same value at
// the same path: the remote's ref when there is one, else HEAD (a commit
// in a repo whose history no remote holds).
func fileHistoryLine(places []string, ref, where string) string {
	named := places
	more := ""
	if len(named) > maxHistoryPlaces {
		named = named[:maxHistoryPlaces]
		more = " and " + strconv.Itoa(len(places)-maxHistoryPlaces) + " more"
	}
	// singular / plural wording: one place, or several
	subject, verb, value, was, file, neg := named[0], "holds", "a secret-looking value that", "is", "file", "it does not"
	if len(places) > 1 {
		subject, verb, value, was, file, neg = strings.Join(named, ", ")+more, "hold", "secret-looking values that", "are", "files", "they do not"
	}
	action := "this commit"
	if where == LeakWherePush {
		action = "this push"
	}
	if ref == "" && where != LeakWherePush {
		was = map[string]string{"is": "was", "are": "were"}[was]
		return "lore: " + subject + " " + verb + " " + value + " " + was + " already in the same " + file + " at HEAD, not new, so " + neg + " block " + action
	}
	on := ""
	if ref != "" {
		on = " (" + ref + ")"
	}
	return "lore: " + subject + " " + verb + " " + value + " " + was + " already on the remote in the same " + file + on + ", so nothing new is sent and " + neg + " block " + action
}

// dropHistoryHits removes from hits (hits[i] are the findings of paths[i])
// every finding whose fingerprint the same row (a row file) or the same
// path (any other file) already held at HEAD, and returns the rest with
// the ones removed (a plain file's named with the ref that held it). See
// the file comment.
// bases are the commits to compare with: nil means historyBases (a
// commit check), a push check passes the commits the remote has.
func dropHistoryHits(ctx context.Context, sc scan.Scanner, root string, inStore bool, paths []string, docs map[string][]byte, hits [][]leakcheck.SecretHit, bases []string) ([][]leakcheck.SecretHit, []historyHit) {
	want := map[ids.AnyID]bool{}
	rowOf := make([]ids.AnyID, len(paths))
	dirs := map[string]bool{} // the .lore/data folders the rows sit in (a code repo)
	var plain []string        // other files with findings: compared with HEAD at the same path
	for i, p := range paths {
		if len(hits[i]) == 0 || isCommitMessageDoc(p) {
			continue // a commit message is new by definition
		}
		if !isRowDocPath(p, inStore) {
			plain = append(plain, p)
			continue
		}
		r, err := row.Decode(docs[p])
		if err != nil || r.ID.IsZero() {
			plain = append(plain, p) // not a row after all: as any file
			continue
		}
		rowOf[i] = r.ID
		want[r.ID] = true
		if at := strings.Index("/"+p, "/"+loreDataMark); at >= 0 {
			dirs[p[:at+len(loreDataMark)]] = true
		}
	}
	if len(want) == 0 && len(plain) == 0 {
		return hits, nil
	}
	top := gitTop(ctx, root)
	if top == "" {
		return hits, nil
	}
	var specs []string // none (a store clone): its whole tree
	if !inStore {
		for d := range dirs {
			specs = append(specs, d)
		}
	}
	oldRows := map[ids.AnyID]map[string]bool{}
	oldFiles := map[string]map[string]string{} // path → fingerprint → the first base that held it
	if bases == nil {
		// A commit check also compares with the remote's branch tips: after
		// a squash merge the branch's own commits are on no remote by
		// ancestry, but a value the remote's branch already holds at the
		// same path is not new (issue #51). Only read when a finding exists.
		bases = append(historyBases(ctx, top), remoteBranchTips(ctx, top)...)
	}
	for _, base := range bases { // none: fail closed
		if len(want) > 0 {
			for id, set := range historyFingerprints(ctx, sc, top, inStore, want, specs, base) {
				oldRows[id] = union(oldRows[id], set)
			}
		}
		if len(plain) > 0 {
			if blobs, err := revBlobs(ctx, top, nil, base, plain); err == nil {
				for p, b := range blobs {
					if oldFiles[p] == nil {
						oldFiles[p] = map[string]string{}
					}
					for fp := range previewsOf(scanDoc(sc, hookAllows{}, p, b, inStore)) {
						if oldFiles[p][fp] == "" {
							oldFiles[p][fp] = base
						}
					}
				}
			}
		}
	}
	var dropped []historyHit
	refOf := map[string]string{} // base commit → the ref naming it (looked up once, only for a dropped file finding)
	out := make([][]leakcheck.SecretHit, len(hits))
	for i, hs := range hits {
		for _, h := range hs {
			if !rowOf[i].IsZero() {
				if oldRows[rowOf[i]][h.Preview] {
					dropped = append(dropped, historyHit{Path: paths[i], Line: h.Line, Row: true})
					continue
				}
			} else if base := oldFiles[paths[i]][h.Preview]; base != "" {
				ref, ok := refOf[base]
				if !ok {
					ref = remoteRefHolding(ctx, top, base)
					refOf[base] = ref
				}
				dropped = append(dropped, historyHit{Path: paths[i], Line: h.Line, Ref: ref})
				continue
			}
			out[i] = append(out[i], h)
		}
	}
	return out, dropped
}

// remoteRefHolding names the ref the remote holds commit under, for a
// history notice: a remote-tracking branch that contains it (its main or
// master first), else a tag the remote holds at the same object that
// contains it ("tag v1.2", the oldest), else "" (no remote has it: a repo
// without a remote, where HEAD is the history). One git process, run only
// when a plain file's finding was let through.
func remoteRefHolding(ctx context.Context, top, commit string) string {
	out, err := gitRun{dir: top, args: []string{"for-each-ref", "--sort=creatordate", "--contains", commit,
		"--format=%(refname)%00%(objectname)%00%(*objectname)", remotesRefPrefix, tagsRefPrefix}}.output(ctx)
	if err != nil {
		return ""
	}
	var branches, tags []string
	var pushedTags map[string]bool
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Split(strings.TrimSpace(l), "\x00")
		if len(f) != 3 {
			continue
		}
		if b, ok := strings.CutPrefix(f[0], remotesRefPrefix); ok {
			if !strings.HasSuffix(b, "/HEAD") {
				branches = append(branches, b)
			}
			continue
		}
		name, ok := strings.CutPrefix(f[0], tagsRefPrefix)
		if !ok {
			continue
		}
		if pushedTags == nil { // only a local tag the remote holds names where the value is
			pushedTags = map[string]bool{}
			for _, c := range RemoteTagCommits(ctx, top, remoteNames(ctx, top)) {
				pushedTags[c] = true
			}
		}
		if c := f[2]; pushedTags[c] || (c == "" && pushedTags[f[1]]) {
			tags = append(tags, name)
		}
	}
	for _, b := range branches {
		if _, rest, _ := strings.Cut(b, "/"); rest == "main" || rest == "master" {
			return b
		}
	}
	if len(branches) > 0 {
		return branches[0]
	}
	if len(tags) > 0 {
		return "tag " + tags[0]
	}
	return ""
}

// union adds every key of b to a (a new map when a is nil) and returns it.
func union(a, b map[string]bool) map[string]bool {
	if a == nil {
		a = make(map[string]bool, len(b))
	}
	for k := range b {
		a[k] = true
	}
	return a
}

// maxHistoryBases caps the pushed commits a commit check compares with (a
// branch merged from many pushed branches has one per merge).
const maxHistoryBases = 8

// historyBases returns the commits a commit check counts as history (see
// the file comment): the pushed commits under HEAD's unpushed ones
// (`rev-list --boundary HEAD --not --remotes <tags the remote has>`), so the commit an amend
// replaces is never its own history; HEAD itself when HEAD is pushed, or
// when none of its history ever was (no remote, or remote refs of another
// history only: nothing to compare with, and the push check scans every
// pushed blob whole). Nil when HEAD cannot be read: every finding is new.
func historyBases(ctx context.Context, top string) []string {
	if _, err := (gitRun{dir: top, args: []string{"rev-parse", "-q", "--verify", "HEAD^{commit}"}}).output(ctx); err != nil {
		return nil
	}
	head := []string{"HEAD"}
	remotes, err := gitRun{dir: top, args: []string{"for-each-ref", "--count=1", "--format=%(refname)", "refs/remotes/"}}.output(ctx)
	if err != nil || strings.TrimSpace(string(remotes)) == "" {
		return head
	}
	unpushed, err := gitRun{dir: top, args: []string{"rev-list", "-1", "HEAD", "--not", "--remotes"}}.output(ctx)
	if err != nil || strings.TrimSpace(string(unpushed)) == "" {
		return head // pushed (or unknown: as before)
	}
	// A commit the remote holds behind a pushed tag is pushed too (issue
	// #51): the tags' commits are negated next to --remotes.
	notPushed := append([]string{"rev-list", "--boundary", "HEAD", "--not", "--remotes"}, RemoteTagCommits(ctx, top, remoteNames(ctx, top))...)
	var bases []string
	if out, lerr := (gitRun{dir: top, args: notPushed}).output(ctx); lerr == nil {
		for _, l := range strings.Split(string(out), "\n") {
			if c, ok := strings.CutPrefix(strings.TrimSpace(l), "-"); ok && c != "" && len(bases) < maxHistoryBases {
				bases = append(bases, c)
			}
		}
	}
	if len(bases) > 0 {
		return bases
	}
	return head // nothing under HEAD was ever pushed (the remote refs are another history): as a repo with no remote
}

// remoteBranchTips are the remote-tracking commits a value counts as
// already on the remote at: the current branch's upstream and each
// remote's default branch (refs/remotes/<remote>/HEAD, else its main or
// master). Missing refs are skipped; none on any error.
func remoteBranchTips(ctx context.Context, top string) []string {
	refs := []string{"@{upstream}"}
	for _, r := range remoteNames(ctx, top) {
		refs = append(refs, remotesRefPrefix+r+"/HEAD", remotesRefPrefix+r+"/main", remotesRefPrefix+r+"/master")
	}
	seen := map[string]bool{}
	var tips []string
	for _, ref := range refs {
		if len(tips) >= maxHistoryBases {
			break
		}
		out, err := gitRun{dir: top, args: []string{"rev-parse", "-q", "--verify", ref + "^{commit}"}}.output(ctx)
		if c := strings.TrimSpace(string(out)); err == nil && c != "" && !seen[c] {
			seen[c] = true
			tips = append(tips, c)
		}
	}
	return tips
}

// previewsOf is the set of fingerprints of hs.
func previewsOf(hs []leakcheck.SecretHit) map[string]bool {
	set := make(map[string]bool, len(hs))
	for _, h := range hs {
		set[h.Preview] = true
	}
	return set
}

// isRowDocPath reports whether p (relative to the repo top) is a row file.
func isRowDocPath(p string, inStore bool) bool {
	return strings.Contains("/"+p, "/"+loreDataMark) || (inStore && isStoreRowPath(p))
}

// historyFingerprints returns, for each wanted row id found at commit rev
// in the repo at top, the fingerprints of every finding that version of the
// row holds (an empty map when rev cannot be read: fail closed).
// specs are top-relative folders to list (a row moved within them is still
// found); none lists the whole tree.
func historyFingerprints(ctx context.Context, sc scan.Scanner, top string, inStore bool, want map[ids.AnyID]bool, specs []string, rev string) map[ids.AnyID]map[string]bool {
	// One listing of rev's files there (no such commit: an error, nothing old).
	args := append([]string{"-c", "core.quotePath=false", "ls-tree", "-r", "-z", "--name-only", "--full-tree", rev, "--"}, specs...)
	out, err := gitRun{dir: top, args: args}.output(ctx)
	if err != nil {
		return nil
	}
	byName := make(map[string]bool, len(want))
	for id := range want {
		byName[id.String()+rowFileSuffix] = true
	}
	var cands []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" && byName[path.Base(p)] {
			cands = append(cands, p) // a row file is named after its id, wherever it sits
		}
	}
	if len(cands) == 0 {
		return nil
	}
	blobs, err := revBlobs(ctx, top, nil, rev, cands)
	if err != nil {
		return nil
	}
	had := map[ids.AnyID]map[string]bool{}
	for _, p := range cands {
		b, ok := blobs[p]
		if !ok {
			continue
		}
		r, derr := row.Decode(b)
		if derr != nil || !want[r.ID] {
			continue // not that row after all (a file named like it)
		}
		// No allow entries: every finding of the old row, allowed or not,
		// is a fingerprint already in history.
		set := previewsOf(scanDoc(sc, hookAllows{}, p, b, inStore))
		for k := range had[r.ID] {
			set[k] = true // the row at two paths at HEAD: both count
		}
		had[r.ID] = set
	}
	return had
}

// migratedSecretsMax caps the rows the lore fix warning names one by one.
const migratedSecretsMax = 10

// migratedSecretsLine is the one warning `lore fix` / `lore migrate` prints
// when the rows it rewrote in the code repo hold secret-looking values
// (bug B1-14): it names each by row id, field, rule and fingerprint (never
// the value) and says what to do, before the person commits the
// migration. "" when none (or the scanner cannot be built: the commit
// check still runs). tree is the data folder's tree after the run, data
// the folder relative to top, diff the files the run changed.
func (a *App) migratedSecretsLine(ctx context.Context, top, tree, data string, diff []git.TreeChange) string {
	var rels []string
	for _, c := range diff {
		if strings.HasSuffix(c.Path, rowFileSuffix) && strings.Trim(c.NewBlob, "0") != "" {
			rels = append(rels, c.Path)
		}
	}
	if len(rels) == 0 {
		return ""
	}
	sc, err := a.HookScanner(ctx)
	if err != nil {
		return ""
	}
	blobs, err := revBlobs(ctx, top, nil, tree, rels) // "<tree>:<path>": the bytes the run wrote
	if err != nil {
		return ""
	}
	docs := make(map[string][]byte, len(blobs))
	paths := make([]string, 0, len(blobs))
	for rel, b := range blobs {
		p := path.Join(data, rel)
		docs[p] = b
		paths = append(paths, p)
	}
	sort.Strings(paths)
	hits := scanDocs(sc, hookAllows{}, paths, docs, false)
	var named []string
	n := 0
	for i, p := range paths {
		if len(hits[i]) == 0 {
			continue
		}
		id := strings.TrimSuffix(path.Base(p), rowFileSuffix)
		for _, h := range hits[i] {
			n++
			if len(named) < migratedSecretsMax {
				named = append(named, id+" "+h.Field+" "+h.Rule+" "+h.Preview)
			}
		}
	}
	if n == 0 {
		return ""
	}
	more := ""
	if n > len(named) {
		more = ", and " + strconv.Itoa(n-len(named)) + " more"
	}
	return "warning: " + strconv.Itoa(n) + " secret-looking value(s) in the rewritten rows (row field rule fingerprint): " + strings.Join(named, "; ") + more +
		". They were already committed, so the commit check lets this migration through; still move each real secret to your vault (lore edit --id <id>, ${secret:…}), or mark one that is not: " + leakcheck.AllowRowCommand("<row>", "<field>", "<rule>", "<fingerprint>") + " (every one, each with its own line: lore check leaks --rows)"
}
