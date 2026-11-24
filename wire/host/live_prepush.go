package host

// live_prepush.go — the F16 leak check in the pre-push hook (trial F7;
// ported from saas/cmd/cli leak_prepush.go, P3-MODEB-9). The commit hooks
// never run for `git commit --no-verify`, so the pre-push hook checks every
// object the push would send that the remote does not have: each new file
// version (blob) under the path it was found at, and each new commit's
// message. It fails closed when it cannot list what the push sends, and
// LiveConfig.LeakCheck fails closed when the stores cannot be read.
// `git push --no-verify` skips it too: a copy-paste catch, not a wall.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
)

// pushUpdate is one pre-push stdin line: "<local ref> <local sha> <remote
// ref> <remote sha>".
type pushUpdate struct{ localRef, localSHA, remoteRef, remoteSHA string }

// Pre-push words.
const (
	pushUpdateFields = 4
	shortOIDLen      = 7
	objectBlob       = "blob"
	objectCommit     = "commit"
	commitMsgWhere   = wire.PushCommitMsgPrefix
)

// parsePushUpdates reads the hook's stdin (malformed lines skipped).
func parsePushUpdates(in []byte) []pushUpdate {
	var out []pushUpdate
	sc := bufio.NewScanner(bytes.NewReader(in))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < pushUpdateFields {
			continue
		}
		out = append(out, pushUpdate{f[0], f[1], f[2], f[3]})
	}
	return out
}

// isZeroOID is git's "no object" id, in either hash length.
func isZeroOID(oid string) bool { return oid != "" && strings.Trim(oid, "0") == "" }

// prePushLeakCheck checks what this push sends (remote: the hook's $1).
func (h *liveHost) prePushLeakCheck(ctx context.Context, repo *gitref.Repo, remote string, updates []pushUpdate) error {
	if h.cfg.LeakCheck == nil {
		return nil
	}
	var refs []string
	for _, u := range updates {
		if !isZeroOID(u.localSHA) {
			refs = append(refs, u.remoteRef)
		}
	}
	wire.NoteTagPush(ctx, repo.Dir(), remote, refs) // a pushed tag is listed again on the next check
	sets, err := pushDocs(ctx, repo, remote, updates)
	if err != nil {
		return errs.Wrap(errs.LeakBlocked, "cannot check: could not list what this push sends", err).
			WithHint("bypassing the check is your deliberate `git push --no-verify`")
	}
	return checkPushDocs(wire.WithPushBases(ctx, pushBases(ctx, repo, remote, updates)), h.cfg.LeakCheck, sets, pushAddedLines(ctx, repo, remote, updates), pushKnown(ctx, repo, remote, updates))
}

// pushSet is one doc set of a push (pushDocs): path → content, and the
// blob id each file version was read from (commit messages have none).
type pushSet struct {
	docs  map[string][]byte
	blobs map[string]string
}

// checkPushDocs runs check over every doc set of a push (pushDocs), the
// newest file versions first, and returns the first refusal. added (blob
// id → the lines the pushed commits add, pushAddedLines) narrows F16 to
// those lines (devco:D-728, wire.WithAddedLines); a file version it does
// not describe, and every file when added is nil, is checked whole.
// When a set is refused and known is set, the set is checked once more
// without the lines (and commit messages) the remote already has
// (prepush_known.go, issue #128): known runs at most once per push and
// only after a refusal, so a clean push reads nothing more.
func checkPushDocs(ctx context.Context, check func(context.Context, string, map[string][]byte) error, sets []pushSet, added map[string]map[int]bool, known knownFunc) error {
	var have *remoteKnown
	for _, set := range sets {
		setCtx := ctx
		var lines map[string]map[int]bool
		if added != nil {
			lines = map[string]map[int]bool{}
			for p, blob := range set.blobs {
				if l, ok := added[blob]; ok {
					lines[p] = l
				}
			}
			setCtx = wire.WithAddedLines(ctx, lines)
		}
		err := check(setCtx, wire.LeakWherePush, set.docs)
		if err == nil || lines == nil || known == nil || !errs.Has(err, errs.LeakBlocked) {
			if err != nil {
				return err
			}
			continue
		}
		if have == nil {
			k := known(pushPaths(sets))
			have = &k
		}
		if have.empty() {
			return err
		}
		if err := check(wire.WithAddedLines(ctx, dropKnownLines(lines, set.docs, *have)), wire.LeakWherePush, set.docs); err != nil {
			return err
		}
	}
	return nil
}

// pushPaths are the file paths of every doc set of a push (sorted, once).
func pushPaths(sets []pushSet) []string {
	seen := map[string]bool{}
	var out []string
	for _, set := range sets {
		for p := range set.blobs {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

// pushRange is the rev-list arguments naming what a push sends: each
// updated ref's new tip, minus its current remote tip and everything the
// remote's tracking refs have. ok is false when the push sends nothing
// (only deletes).
func pushRange(ctx context.Context, repo *gitref.Repo, remote string, updates []pushUpdate) (args []string, ok bool) {
	for _, u := range updates {
		if isZeroOID(u.localSHA) {
			continue // a delete sends nothing
		}
		args = append(args, u.localSHA)
		ok = true
		if !isZeroOID(u.remoteSHA) {
			if _, err := repo.Run(ctx, gitref.Invocation{Args: []string{"cat-file", "-e", u.remoteSHA + "^{commit}"}}); err == nil {
				args = append(args, "^"+u.remoteSHA)
			}
		}
	}
	return append(args, notOnRemote(ctx, repo, remote)...), ok
}

// notOnRemote is the rev-list tail excluding what remote already has: its
// tracking refs (every remote's when remote is "") and the commits of the
// local tags it holds (a commit pushed only through a tag, issue #51;
// wire.RemoteTagCommits, cached, no network call per push).
func notOnRemote(ctx context.Context, repo *gitref.Repo, remote string) []string {
	notRemote := "--remotes"
	remotes := []string{remote}
	if remote != "" {
		notRemote += "=" + remote
	} else {
		remotes = nil
	}
	return append([]string{"--not", notRemote}, wire.RemoteTagCommits(ctx, repo.Dir(), remotes)...)
}

// pushAddedLines is, per blob id, the line numbers the commits this push
// sends add to it (wire.PushAddedLines over one `git diff-tree --stdin`
// of every pushed commit against each parent). Nil on any git error:
// then every pushed file version is checked whole (fails closed).
func pushAddedLines(ctx context.Context, repo *gitref.Repo, remote string, updates []pushUpdate) map[string]map[int]bool {
	rng, ok := pushRange(ctx, repo, remote, updates)
	if !ok {
		return nil
	}
	revs, err := repo.Run(ctx, gitref.Invocation{Args: append([]string{"rev-list", "--parents"}, rng...)})
	if err != nil {
		return nil
	}
	parents := map[string]int{}
	for _, l := range strings.Split(string(revs), "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			parents[f[0]] = len(f) - 1
		}
	}
	if len(parents) == 0 {
		return map[string]map[int]bool{}
	}
	patch, err := repo.Run(ctx, gitref.Invocation{
		Args:  []string{"-c", "core.quotePath=false", "diff-tree", "--stdin", "-p", "-U0", "--full-index", "-m", "--root", "-r", "--find-renames", "--diff-filter=ACMR", "--no-color", "--no-ext-diff", "--no-textconv"},
		Stdin: revs,
	})
	if err != nil {
		return nil
	}
	return wire.PushAddedLines(string(patch), parents)
}

// maxPushBases caps the remote commits a push check compares with (as the
// commit check's maxHistoryBases).
const maxPushBases = 8

// pushBases returns the commits the remote already has that this push
// builds on: each updated ref's current remote tip, and the boundary of
// what the push sends (`rev-list --boundary <tips> --not --remotes=<remote>
// <tags the remote has>`).
// The secret check treats a value the same row held at one of them as
// already on the remote (wire.WithPushBases). Nil on any git error or an
// empty remote: then every finding blocks, as before (fails closed).
func pushBases(ctx context.Context, repo *gitref.Repo, remote string, updates []pushUpdate) []string {
	var bases []string
	seen := map[string]bool{}
	add := func(c string) {
		if c != "" && !seen[c] && len(bases) < maxPushBases {
			seen[c] = true
			bases = append(bases, c)
		}
	}
	args := []string{"rev-list", "--boundary"}
	tips := 0
	for _, u := range updates {
		if isZeroOID(u.localSHA) {
			continue
		}
		args = append(args, u.localSHA)
		tips++
		if !isZeroOID(u.remoteSHA) {
			if _, err := repo.Run(ctx, gitref.Invocation{Args: []string{"cat-file", "-e", u.remoteSHA + "^{commit}"}}); err == nil {
				add(u.remoteSHA)
				args = append(args, "^"+u.remoteSHA)
			}
		}
	}
	if tips == 0 {
		return bases
	}
	out, err := repo.Run(ctx, gitref.Invocation{Args: append(args, notOnRemote(ctx, repo, remote)...)})
	if err != nil {
		return bases
	}
	for _, l := range strings.Split(string(out), "\n") {
		if c, ok := strings.CutPrefix(strings.TrimSpace(l), "-"); ok {
			add(c)
		}
	}
	return bases
}

// pushDocs lists every new blob (at the first path git reports for it) and
// every new commit's message the push would send: not reachable from the
// remote's tracking refs nor from the remote ref's current tip. A push of
// several commits can carry several versions of one file under one path;
// a doc map holds one per path, so each further version goes into the next
// set (devco:S-9: the older, clean version of a row file overwrote the
// newer one a --no-verify commit had put a key into, and the push went
// out). The first set holds the newest version of every path.
func pushDocs(ctx context.Context, repo *gitref.Repo, remote string, updates []pushUpdate) ([]pushSet, error) {
	rng, ok := pushRange(ctx, repo, remote, updates)
	if !ok {
		return nil, nil
	}
	out, err := repo.Run(ctx, gitref.Invocation{Args: append([]string{"rev-list", "--objects"}, rng...)})
	if err != nil {
		return nil, fmt.Errorf("rev-list: %w", err)
	}
	var oids []string
	paths := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		oid, p, _ := strings.Cut(strings.TrimSpace(line), " ")
		if oid == "" {
			continue
		}
		if _, seen := paths[oid]; seen {
			continue
		}
		paths[oid] = p
		oids = append(oids, oid)
	}
	return readPushObjects(ctx, repo, oids, paths)
}

// readPushObjects reads oids with one `git cat-file --batch`: blobs by
// path, commits by their message (trees and tags carry nothing more). A
// path met again (an older version: rev-list lists newer commits first)
// goes into the first set that does not hold it yet (pushDocs).
func readPushObjects(ctx context.Context, repo *gitref.Repo, oids []string, paths map[string]string) ([]pushSet, error) {
	var sets []pushSet
	put := func(p, blob string, b []byte) {
		for _, set := range sets {
			if _, taken := set.docs[p]; !taken {
				set.docs[p] = b
				if blob != "" {
					set.blobs[p] = blob
				}
				return
			}
		}
		set := pushSet{docs: map[string][]byte{p: b}, blobs: map[string]string{}}
		if blob != "" {
			set.blobs[p] = blob
		}
		sets = append(sets, set)
	}
	if len(oids) == 0 {
		return sets, nil
	}
	out, err := repo.Run(ctx, gitref.Invocation{Args: []string{"cat-file", "--batch"}, Stdin: []byte(strings.Join(oids, "\n") + "\n")})
	if err != nil {
		return nil, fmt.Errorf("cat-file: %w", err)
	}
	r := bufio.NewReader(bytes.NewReader(out))
	for range oids {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("cat-file: short output: %w", err)
		}
		f := strings.Fields(header) // "<oid> <type> <size>" or "<oid> missing"
		if len(f) != 3 {
			return nil, fmt.Errorf("cat-file: %s", strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, fmt.Errorf("cat-file: size of %s: %w", f[0], err)
		}
		body := make([]byte, size+1) // content and the trailing newline
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, fmt.Errorf("cat-file: read %s: %w", f[0], err)
		}
		body = body[:size]
		switch f[1] {
		case objectBlob:
			p := paths[f[0]]
			if p == "" {
				p = f[0]
			}
			put(p, f[0], body)
		case objectCommit:
			_, msg, _ := bytes.Cut(body, []byte("\n\n"))
			put(commitMsgWhere+f[0][:shortOIDLen], "", msg)
		}
	}
	return sets, nil
}
