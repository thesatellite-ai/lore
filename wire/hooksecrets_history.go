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

// historyNotice is the one line a commit check prints when it let findings
// through because the same row already held them at HEAD.
const historyNotice = " secret-looking value(s) in this commit were already in the same row(s) at HEAD, not new, so they do not block it; list every stored one with `lore check leaks --rows`"

// historyLine is the notice of n findings let through as already in history.
func historyLine(n int) string {
	return "lore: " + strconv.Itoa(n) + historyNotice
}

// dropHistoryHits removes from hits (hits[i] are the findings of paths[i])
// every finding whose fingerprint the same row (a row file) or the same
// path (any other file) already held at HEAD, and returns the rest with
// the number removed. See the file comment.
func dropHistoryHits(ctx context.Context, sc scan.Scanner, root string, inStore bool, paths []string, docs map[string][]byte, hits [][]leakcheck.SecretHit) ([][]leakcheck.SecretHit, int) {
	want := map[ids.AnyID]bool{}
	rowOf := make([]ids.AnyID, len(paths))
	dirs := map[string]bool{} // the .lore/data folders the rows sit in (a code repo)
	var plain []string        // other files with findings: compared with HEAD at the same path
	for i, p := range paths {
		if len(hits[i]) == 0 || p == checkWhereCommitMsg {
			continue // the commit message is new by definition
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
		return hits, 0
	}
	top := gitTop(ctx, root)
	if top == "" {
		return hits, 0
	}
	var specs []string // none (a store clone): its whole tree
	if !inStore {
		for d := range dirs {
			specs = append(specs, d)
		}
	}
	oldRows := map[ids.AnyID]map[string]bool{}
	oldFiles := map[string]map[string]bool{}
	for _, base := range historyBases(ctx, top) { // none: fail closed
		if len(want) > 0 {
			for id, set := range historyFingerprints(ctx, sc, top, inStore, want, specs, base) {
				oldRows[id] = union(oldRows[id], set)
			}
		}
		if len(plain) > 0 {
			if blobs, err := revBlobs(ctx, top, nil, base, plain); err == nil {
				for p, b := range blobs {
					oldFiles[p] = union(oldFiles[p], previewsOf(scanDoc(sc, nil, p, b, inStore)))
				}
			}
		}
	}
	dropped := 0
	out := make([][]leakcheck.SecretHit, len(hits))
	for i, hs := range hits {
		had := oldFiles[paths[i]]
		if !rowOf[i].IsZero() {
			had = oldRows[rowOf[i]]
		}
		for _, h := range hs {
			if had[h.Preview] {
				dropped++
				continue
			}
			out[i] = append(out[i], h)
		}
	}
	return out, dropped
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
// (`rev-list --boundary HEAD --not --remotes`), so the commit an amend
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
	var bases []string
	if out, lerr := (gitRun{dir: top, args: []string{"rev-list", "--boundary", "HEAD", "--not", "--remotes"}}).output(ctx); lerr == nil {
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
		set := previewsOf(scanDoc(sc, nil, p, b, inStore))
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
	hits := scanDocs(sc, nil, paths, docs, false)
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
		". They were already committed, so the commit check lets this migration through; still move each real secret to your vault (lore edit <id>, ${secret:…}), or mark one that is not: lore secrets allow <id> --field <field> --rule <rule> --match-hash <fingerprint> --reason \"<why>\" (every one: lore check leaks --rows)"
}
