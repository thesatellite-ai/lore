package wire

// check_history.go — `lore check leaks --history` (devco D-870, owner
// org-setup item 11): the secret scan of every file version and commit
// message in a repo's history, not only the staged files (the hooks), the
// files a push changes (--ci, --since) or the current tree (--as-client).
//
//	revs: HEAD, or since..HEAD (--since)
//	  ─▶ git rev-list --objects <revs> | git cat-file --batch-check
//	     one pass over the object store: every distinct blob once, with its
//	     size and a path it lives at (a value kept through 50 commits is
//	     one blob, read once)
//	  ─▶ blobs in chunks of historyChunkBytes through git cat-file --batch,
//	     each scanned by the hook's scanner on every CPU (scanDoc: the
//	     built-in rules, the store's team rules, the repo's .gitleaks.toml,
//	     the committed and linked allow entries, .gitleaksignore)
//	  ─▶ git log --format=<sha> <message> <revs>: every commit message
//	  ─▶ findings only: one git log --raw pass names the commit that first
//	     added each blob holding one, and HEAD's tree says whether the value
//	     is still in the current files
//	  ─▶ one line per value (path, rule, match hash), the earliest commit,
//	     how many later versions carry it; a commit-pinned .gitleaksignore
//	     line (<commit>:<path>:<rule>:<line>, gitleaks' history form)
//	     accepts that finding
//	  ─▶ E_LEAK_BLOCKED, or the ✓ summary (commits, file versions, MB)
//
// Why: a key committed and removed later is still in every clone, and no
// check reported it: the hooks and --ci read only what a commit or push
// changes, --as-client only the current files. Rotating such a key is the
// fix; lore never rewrites history. No value is ever printed: each
// finding carries its sha256 match hash, as every other refusal does.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
)

// historyWhere names the history check in its lines and refusal.
const historyWhere = "lore check leaks --history"

// historyChunkBytes bounds the blob bytes one git cat-file --batch reads
// before they are scanned, so a large history never sits in memory whole.
const historyChunkBytes = 32 << 20

// historyMaxBlob skips file versions above this size, the --as-client
// limit (binaries and vendored dumps); the summary counts them.
const historyMaxBlob = asClientMaxFile

// historyProgress is where the progress lines go (a test swaps it). Counts
// only, never a path or a value: a CI log may be the client's to read.
var historyProgress io.Writer = os.Stderr

// historyBlob is one distinct file version of the scanned history.
type historyBlob struct {
	oid, path string
	size      int
}

// historyFinding is one secret value found in history: the path it was
// added at, the rule and match hash, the commit that first added it and
// how many file versions carry it.
type historyFinding struct {
	path, commit string
	hit          leakcheck.SecretHit
	atHead       bool
	versions     int
	order        int // the adding commit's place in history (oldest first)
}

// historyStats is what the scan read, for the summary and the refusal.
type historyStats struct {
	commits, blobs, large, binary, messages int
	bytes                                   int64
}

// CheckHistory scans every file version and commit message reachable from
// HEAD (since..HEAD when since is set) in the repo at root with the hook's
// secret rules and allow entries. It returns the ✓ summary, or
// E_LEAK_BLOCKED naming each value (commit, path, line, rule, match hash)
// with its next step. Fails closed: a history it cannot read refuses.
func (a *App) CheckHistory(ctx context.Context, root, since string) (string, error) {
	top := gitTop(ctx, root)
	if top == "" {
		return "", errs.New(errs.InvalidInput, "check leaks --history needs a git repo: "+root+" is not in one").
			WithHint("run it inside the repo whose history you want checked")
	}
	if _, ok := liveRefCommit(ctx, top, "HEAD"); !ok {
		return "✓ " + historyWhere + ": this repo has no commit yet, so it has no history to check", nil
	}
	revs := []string{"HEAD"}
	if since != "" {
		base, ok := liveRefCommit(ctx, top, since)
		if !ok {
			return "", errs.New(errs.InvalidInput, "check leaks --history: --since "+since+" names no commit in this repo").
				WithHint("give a commit id, branch or tag (git log --oneline lists them), or leave --since out to check the whole history")
		}
		revs = append(revs, "^"+base)
	}
	sc, err := a.HookScanner(ctx)
	if err != nil {
		return "", errs.Wrap(errs.LeakBlocked, "cannot check for secrets: the scanner could not be built: "+err.Error(), err)
	}
	allows, err := committedAllows(ctx, root)
	if err != nil {
		return "", err
	}
	allows.entries = append(allows.entries, a.linkedAllows(ctx).entries...) // D-736: a linked repo's entries live in its folder
	pinned, err := historyPins(ctx, top)
	if err != nil {
		return "", err
	}
	_, isGit, inStore := rowfs.StoreTop(root)
	inStore = inStore && isGit
	return scanHistory(ctx, top, revs, sc, allows, pinned, historyPlace{inStore: inStore, allowSaves: a.secretsAllowSaves(ctx)})
}

// historyPlace is what the repo is, for the scan and the next steps:
// inStore (a store clone, whose row files sit at <folder>/<table>/) and
// allowSaves (`lore secrets allow` can save an entry here; false in a
// repo lore is not set up in).
type historyPlace struct{ inStore, allowSaves bool }

// scanHistory is CheckHistory after its inputs are resolved (the unit the
// tests drive with a plain scanner).
func scanHistory(ctx context.Context, top string, revs []string, sc scan.Scanner, allows hookAllows, pinned historyPinSet, at historyPlace) (string, error) {
	inStore := at.inStore
	var st historyStats
	count, err := liveGit(ctx, top, append([]string{"rev-list", "--count"}, revs...)...)
	if err != nil {
		return "", leakCannotCheckErr("the commits of the history", err)
	}
	st.commits, _ = strconv.Atoi(strings.TrimSpace(string(count)))
	blobs, err := historyBlobs(ctx, top, revs)
	if err != nil {
		return "", err
	}
	var read []historyBlob
	var total int64
	for _, b := range blobs {
		if b.size > historyMaxBlob {
			st.large++
			continue
		}
		read = append(read, b)
		total += int64(b.size)
	}
	_, _ = fmt.Fprintf(historyProgress, "%s: reading %d commits, %d file versions (%d MB); every version is read once\n",
		historyWhere, st.commits, len(read), total>>20)
	_ = sc.Scan("", "")                        // build a lazy scanner once, before the workers share it
	hits := map[string][]leakcheck.SecretHit{} // blob oid → its findings
	var done int64
	for lo := 0; lo < len(read); {
		hi, size := lo, 0
		for hi < len(read) && (hi == lo || size+read[hi].size <= historyChunkBytes) {
			size += read[hi].size
			hi++
		}
		chunk := read[lo:hi]
		names := make([]string, len(chunk))
		for i, b := range chunk {
			names[i] = b.oid
		}
		bodies, cerr := catFileBatch(ctx, top, nil, names)
		if cerr != nil {
			return "", leakCannotCheckErr("the file versions of the history", cerr)
		}
		for i, h := range scanHistoryChunk(sc, allows, chunk, bodies, inStore) {
			if bodies[i] == nil {
				return "", leakCannotCheckErr("file version "+chunk[i].oid+" of the history", nil)
			}
			if isBinaryDoc(bodies[i]) {
				st.binary++
				continue
			}
			st.blobs++
			st.bytes += int64(len(bodies[i]))
			if len(h) > 0 {
				hits[chunk[i].oid] = h
			}
		}
		done += int64(size)
		if total > historyChunkBytes && hi < len(read) {
			_, _ = fmt.Fprintf(historyProgress, "%s: %d of %d MB read\n", historyWhere, done>>20, total>>20)
		}
		lo = hi
	}
	msgHits, nmsg, err := historyMessages(ctx, top, revs, sc, allows)
	if err != nil {
		return "", err
	}
	st.messages = nmsg
	if len(hits) == 0 && len(msgHits) == 0 {
		return historyClean(st), nil
	}
	found, err := historyFindings(ctx, top, revs, blobs, hits, pinned)
	if err != nil {
		return "", err
	}
	if len(found) == 0 && len(msgHits) == 0 {
		return historyClean(st), nil
	}
	return "", historyRefusal(st, found, msgHits, at)
}

// scanHistoryChunk scans each body of a chunk on every CPU; out[i] are the
// findings of chunk[i] (nil for a missing or binary body).
func scanHistoryChunk(sc scan.Scanner, allows hookAllows, chunk []historyBlob, bodies [][]byte, inStore bool) [][]leakcheck.SecretHit {
	out := make([][]leakcheck.SecretHit, len(chunk))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), max(len(chunk), 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				if bodies[i] != nil {
					out[i] = scanDoc(sc, allows, chunk[i].path, bodies[i], inStore)
				}
			}
		}()
	}
	for i := range chunk {
		next <- i
	}
	close(next)
	wg.Wait()
	return out
}

// historyBlobs lists every distinct blob reachable from revs with its size
// and a path it lives at: one git rev-list --objects walk, its ids fed to
// one git cat-file --batch-check (no object body is read here).
func historyBlobs(ctx context.Context, top string, revs []string) ([]historyBlob, error) {
	named, err := liveGit(ctx, top, append([]string{"rev-list", "--objects"}, revs...)...)
	if err != nil {
		return nil, leakCannotCheckErr("the objects of the history", err)
	}
	paths := map[string]string{}
	var objs bytes.Buffer
	for _, l := range strings.Split(string(named), "\n") {
		oid, p, _ := strings.Cut(l, " ")
		if oid == "" {
			continue
		}
		objs.WriteString(oid + "\n")
		if p != "" {
			paths[oid] = p
		}
	}
	checked, err := gitRun{dir: top, stdin: objs.Bytes(), args: []string{"cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)"}}.output(ctx)
	if err != nil {
		return nil, leakCannotCheckErr("the sizes of the history's file versions", err)
	}
	var out []historyBlob
	for _, l := range strings.Split(string(checked), "\n") {
		f := strings.Fields(l)
		if len(f) != 3 || f[1] != "blob" {
			continue
		}
		n, aerr := strconv.Atoi(f[2])
		if aerr != nil {
			return nil, leakCannotCheckErr("the size of file version "+f[0], aerr)
		}
		p, ok := paths[f[0]]
		if !ok {
			continue // a blob no tree names (a tag pointing at a blob): no file
		}
		out = append(out, historyBlob{oid: f[0], path: p, size: n})
	}
	return out, nil
}

// historyMessages scans every commit message reachable from revs; the
// findings are keyed "message of commit <short sha>" (the pre-push hook's
// name for a pushed message), in history order (newest first).
func historyMessages(ctx context.Context, top string, revs []string, sc scan.Scanner, allows hookAllows) ([]historyMsgHit, int, error) {
	out, err := liveGit(ctx, top, append([]string{"log", "--format=%H%x1f%B%x1e"}, revs...)...)
	if err != nil {
		return nil, 0, leakCannotCheckErr("the commit messages of the history", err)
	}
	var hits []historyMsgHit
	n := 0
	for _, rec := range strings.Split(string(out), "\x1e") {
		sha, body, ok := strings.Cut(strings.TrimLeft(rec, "\n"), "\x1f")
		if !ok {
			continue
		}
		n++
		key := PushCommitMsgPrefix + shortSHA(sha)
		for _, h := range scanDoc(sc, allows, key, []byte(body), false) {
			hits = append(hits, historyMsgHit{commit: sha, hit: h})
		}
	}
	return hits, n, nil
}

// historyMsgHit is a secret in a commit message.
type historyMsgHit struct {
	commit string
	hit    leakcheck.SecretHit
}

// historyFindings turns the blob hits into one finding per value: the
// commit that first added it (one git log --raw pass), the path there,
// whether HEAD still holds it, and the versions carrying it. A finding a
// commit-pinned .gitleaksignore line names is dropped.
func historyFindings(ctx context.Context, top string, revs []string, blobs []historyBlob, hits map[string][]leakcheck.SecretHit, pinned historyPinSet) ([]historyFinding, error) {
	added, err := historyAdders(ctx, top, revs, hits)
	if err != nil {
		return nil, err
	}
	head, err := treeBlobOIDs(ctx, top, nil, "HEAD")
	if err != nil {
		return nil, leakCannotCheckErr("the files at HEAD", err)
	}
	atHead := map[string]string{} // blob oid → a path HEAD holds it at
	for p, oid := range head {
		if _, ok := hits[oid]; ok {
			atHead[oid] = p
		}
	}
	type key struct{ path, rule, preview string }
	byKey := map[key]*historyFinding{}
	for _, b := range blobs {
		hs, ok := hits[b.oid]
		if !ok {
			continue
		}
		ad := added[b.oid]
		p := b.path
		if ad.path != "" {
			p = ad.path
		}
		if hp, ok := atHead[b.oid]; ok {
			p = hp
		}
		for _, h := range hs {
			if pinned.has(ad.commit, p, h) {
				continue
			}
			k := key{p, h.Rule, h.Preview}
			f, ok := byKey[k]
			if !ok {
				f = &historyFinding{path: p, hit: h, commit: ad.commit, order: ad.order}
				byKey[k] = f
			} else if ad.commit != "" && (f.commit == "" || ad.order < f.order) {
				f.commit, f.order, f.hit.Line = ad.commit, ad.order, h.Line
			}
			f.versions++
			if _, ok := atHead[b.oid]; ok {
				f.atHead = true
			}
		}
	}
	out := make([]historyFinding, 0, len(byKey))
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
		return out[i].hit.Line < out[j].hit.Line
	})
	return out, nil
}

// historyAdd is where a blob first entered the history.
type historyAdd struct {
	commit, path string
	order        int
}

// historyAdders names, for each blob in want, the oldest commit whose diff
// (against its first parent for a merge) adds it, and its path there:
// one git log --raw pass, oldest first.
func historyAdders(ctx context.Context, top string, revs []string, want map[string][]leakcheck.SecretHit) (map[string]historyAdd, error) {
	args := append([]string{"-c", "core.quotePath=false", "log", "--reverse", "--raw", "--no-abbrev", "--no-renames",
		"--diff-merges=first-parent", "--format=commit %H"}, revs...)
	out, err := liveGit(ctx, top, args...)
	if err != nil {
		return nil, leakCannotCheckErr("the commits that added each finding", err)
	}
	found := map[string]historyAdd{}
	commit, order := "", 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		l := sc.Text()
		if c, ok := strings.CutPrefix(l, "commit "); ok {
			commit = c
			order++
			continue
		}
		if !strings.HasPrefix(l, ":") {
			continue
		}
		meta, p, ok := strings.Cut(l, "\t")
		f := strings.Fields(meta) // :oldmode newmode oldsha newsha status
		if !ok || len(f) != 5 {
			continue
		}
		oid := f[3]
		if _, need := want[oid]; !need {
			continue
		}
		if _, seen := found[oid]; seen {
			continue
		}
		if strings.HasPrefix(p, `"`) {
			if u, uerr := strconv.Unquote(p); uerr == nil {
				p = u
			}
		}
		found[oid] = historyAdd{commit: commit, path: p, order: order}
	}
	if err := sc.Err(); err != nil {
		return nil, leakCannotCheckErr("the commits that added each finding", err)
	}
	return found, nil
}

// historyClean is the ✓ line of a history with no finding.
func historyClean(st historyStats) string {
	msg := fmt.Sprintf("✓ %s: no secret in %d commit(s) reachable from HEAD (%d file versions, %d MB, %d commit messages read)",
		historyWhere, st.commits, st.blobs, st.bytes>>20, st.messages)
	return msg + historySkipped(st)
}

// historySkipped names the file versions the scan did not read, so a pass
// never claims more than it checked.
func historySkipped(st historyStats) string {
	var parts []string
	if st.large > 0 {
		parts = append(parts, fmt.Sprintf("%d file version(s) above %d MB", st.large, historyMaxBlob>>20))
	}
	if st.binary > 0 {
		parts = append(parts, fmt.Sprintf("%d binary file version(s)", st.binary))
	}
	if len(parts) == 0 {
		return ""
	}
	return "; not read: " + strings.Join(parts, ", ")
}

// historyRefusal is the E_LEAK_BLOCKED for the findings: one line per
// value with the commit that added it, then its next step.
// A value still in the current files gets the `lore secrets allow` line
// where lore can save one; every other finding the commit-pinned
// .gitleaksignore line this check honours.
func historyRefusal(st historyStats, found []historyFinding, msgs []historyMsgHit, place historyPlace) error {
	var lines []string
	for _, f := range found {
		at := fmt.Sprintf("%s:%d", f.path, f.hit.Line)
		if f.hit.Field != "" {
			at += " (field " + f.hit.Field + ")"
		}
		commit := "<commit unknown>"
		if f.commit != "" {
			commit = shortSHA(f.commit)
		}
		state := "removed since, still in history"
		if f.atHead {
			state = "still in the current files"
		}
		if f.versions > 1 {
			state += fmt.Sprintf(", in %d versions of the file", f.versions)
		}
		lines = append(lines, fmt.Sprintf("  %s %-40s secret: rule %s match %s (%s)", commit, at, f.hit.Rule, f.hit.Preview, state))
		switch l := allowLineOf(f.path, f.hit, place.inStore); {
		case f.atHead && place.allowSaves && l != "":
			lines = append(lines, "      not a secret? "+l)
		case f.commit != "":
			lines = append(lines, "      not a secret? add this line to the repo's .gitleaksignore and commit it: "+f.commit+":"+f.path+":"+f.hit.Rule+":"+strconv.Itoa(f.hit.Line))
		}
	}
	for _, m := range msgs {
		lines = append(lines, fmt.Sprintf("  %s %-40s secret: rule %s match %s", shortSHA(m.commit), "commit message", m.hit.Rule, m.hit.Preview))
	}
	n := len(found) + len(msgs)
	head := fmt.Sprintf("%s: %d secret(s) in the history of HEAD (%d commits, %d file versions, %d commit messages read%s):\n",
		historyWhere, n, st.commits, st.blobs, st.messages, historySkipped(st))
	hint := "rotate each credential first: anyone with a clone has every commit, so removing a value from the files does not remove it from history; " +
		"remove a value still in the current files and commit; rewriting history (git filter-repo, then a force push every clone must follow) is your choice, lore never does it; " +
		"a finding that is not a secret: follow the `not a secret?` line printed under it (a commit message cannot be allowed: rotate the credential)"
	return errs.New(errs.LeakBlocked, head+strings.Join(lines, "\n")).WithHint(hint)
}

// shortSHA is the first 12 characters of a commit id.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// historyPin is one commit-pinned .gitleaksignore line.
type historyPin struct {
	commit, path, rule string
	line               int
}

// historyPinSet is the commit-pinned lines of a repo's .gitleaksignore
// (gitleaks' history form, which the commit check skips: D17).
type historyPinSet []historyPin

// historyPins reads the commit-pinned lines of .gitleaksignore as
// committed at HEAD (the history's own accepted findings).
func historyPins(ctx context.Context, top string) (historyPinSet, error) {
	blobs, err := revBlobs(ctx, top, nil, "HEAD", []string{gitleaksIgnoreFile})
	if err != nil {
		return nil, leakCannotCheckErr("the committed "+gitleaksIgnoreFile, err)
	}
	return parseHistoryPins(blobs[gitleaksIgnoreFile]), nil
}

// parseHistoryPins reads the "<commit>:<path>:<rule>:<line>" lines.
func parseHistoryPins(b []byte) historyPinSet {
	var out historyPinSet
	for _, raw := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		parts := strings.Split(l, ":")
		n := len(parts)
		if n < 4 || !isCommitID(parts[0]) {
			continue
		}
		line, err := strconv.Atoi(parts[n-1])
		if err != nil || line < 1 || parts[n-2] == "" {
			continue
		}
		p := strings.Join(parts[1:n-2], ":")
		if p == "" {
			continue
		}
		out = append(out, historyPin{commit: parts[0], path: p, rule: parts[n-2], line: line})
	}
	return out
}

// has reports whether a pinned line names this finding: the commit (a
// prefix of at least 7 characters, as gitleaks prints the full id), path,
// line and rule (or a gitleaks rule lore's rule covers).
func (s historyPinSet) has(commit, p string, h leakcheck.SecretHit) bool {
	if commit == "" {
		return false
	}
	for _, pin := range s {
		if pin.path != p || pin.line != h.Line || !strings.HasPrefix(commit, pin.commit) {
			continue
		}
		// drop matches lore's rule or a gitleaks rule it covers
		if len(gitleaksIgnore{ignoreKey{path: p, rule: pin.rule, line: h.Line}: true}.drop(p, []leakcheck.SecretHit{h})) == 0 {
			return true
		}
	}
	return false
}
