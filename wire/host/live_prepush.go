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
	docs, err := pushDocs(ctx, repo, remote, updates)
	if err != nil {
		return errs.Wrap(errs.LeakBlocked, "cannot check: could not list what this push sends", err).
			WithHint("bypassing the check is your deliberate `git push --no-verify`")
	}
	return h.cfg.LeakCheck(wire.WithPushBases(ctx, pushBases(ctx, repo, remote, updates)), wire.LeakWherePush, docs)
}

// maxPushBases caps the remote commits a push check compares with (as the
// commit check's maxHistoryBases).
const maxPushBases = 8

// pushBases returns the commits the remote already has that this push
// builds on: each updated ref's current remote tip, and the boundary of
// what the push sends (`rev-list --boundary <tips> --not --remotes=<remote>`).
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
	notRemote := "--remotes"
	if remote != "" {
		notRemote += "=" + remote
	}
	out, err := repo.Run(ctx, gitref.Invocation{Args: append(args, "--not", notRemote)})
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
// remote's tracking refs nor from the remote ref's current tip.
func pushDocs(ctx context.Context, repo *gitref.Repo, remote string, updates []pushUpdate) (map[string][]byte, error) {
	args := []string{"rev-list", "--objects"}
	tips := 0
	for _, u := range updates {
		if isZeroOID(u.localSHA) {
			continue // a delete sends nothing
		}
		args = append(args, u.localSHA)
		tips++
		if !isZeroOID(u.remoteSHA) {
			if _, err := repo.Run(ctx, gitref.Invocation{Args: []string{"cat-file", "-e", u.remoteSHA + "^{commit}"}}); err == nil {
				args = append(args, "^"+u.remoteSHA)
			}
		}
	}
	if tips == 0 {
		return nil, nil
	}
	notRemote := "--remotes"
	if remote != "" {
		notRemote += "=" + remote
	}
	out, err := repo.Run(ctx, gitref.Invocation{Args: append(args, "--not", notRemote)})
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
// path, commits by their message (trees and tags carry nothing more).
func readPushObjects(ctx context.Context, repo *gitref.Repo, oids []string, paths map[string]string) (map[string][]byte, error) {
	docs := map[string][]byte{}
	if len(oids) == 0 {
		return docs, nil
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
			docs[p] = body
		case objectCommit:
			_, msg, _ := bytes.Cut(body, []byte("\n\n"))
			docs[commitMsgWhere+f[0][:shortOIDLen]] = msg
		}
	}
	return docs, nil
}
