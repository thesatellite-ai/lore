// leak_prepush.go — the F16 leak check in the pre-push hook (trial F7,
// owner's decision 2026-11-03). The pre-commit and commit-msg checks never
// run for `git commit --no-verify`, so internal text committed that way
// reached a client's remote. The pre-push hook now checks every object the
// push would send that the remote does not already have: each new file
// version (blob) under the path it was found at, and each new commit's
// message, with the same id, text and secret rules as the commit hooks.
// It fails closed when the store cannot be read, as they do.
//
// `git push --no-verify` skips this hook too: it is a copy-paste catch,
// not a wall (§6.6); the client's CI check (`lore check leaks --ci`) is
// the last line.
package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/leakcheck"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"
)

// pushUpdate is one line git hands the pre-push hook on stdin:
// "<local ref> <local sha> <remote ref> <remote sha>".
type pushUpdate struct {
	localRef, localSHA, remoteRef, remoteSHA string
}

// pushUpdateFields is the number of fields of a pre-push stdin line.
const pushUpdateFields = 4

// parsePushUpdates reads the pre-push hook's stdin. Malformed lines are
// skipped (git never writes them).
func parsePushUpdates(stdin []byte) []pushUpdate {
	var out []pushUpdate
	sc := bufio.NewScanner(bytes.NewReader(stdin))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < pushUpdateFields {
			continue
		}
		out = append(out, pushUpdate{localRef: f[0], localSHA: f[1], remoteRef: f[2], remoteSHA: f[3]})
	}
	return out
}

// isZeroOID reports git's "no object" id (a deleted or new ref), in either
// hash length.
func isZeroOID(oid string) bool {
	return oid != "" && strings.Trim(oid, "0") == ""
}

// metaGlob finds every lore project's _meta.json in the repo's index
// (a monorepo may hold several projects; "**/" also matches the top).
var metaGlob = ":(glob)**/" + path.Join(projresolve.MarkerDir, lsync.DataDirName, lsync.MetaFileName)

// prePushLeakCheck is the pre-push leak check. top is the repository's
// top level (git runs the hook there), remote the remote's name or URL
// (the hook's $1). Projects that are not client repos (the org's own, or a
// repo with no org) are not checked, without any store access.
func prePushLeakCheck(ctx context.Context, top, remote string, updates []pushUpdate) error {
	projects, err := pushLeakProjects(ctx, top)
	if err != nil || len(projects) == 0 {
		return err
	}
	items, err := pushItems(ctx, top, remote, updates)
	if err != nil {
		return errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()+" (could not list what this push sends)").WithCause(err).
			WithHint("bypassing the check is your deliberate `git push --no-verify`")
	}
	if len(items) == 0 {
		return nil
	}
	for _, root := range projects {
		if err := runLeakCheck(ctx, root, items, false, leakContext{action: leakActionPush, what: "this push"}); err != nil {
			return err
		}
	}
	return nil
}

// pushLeakProjects lists the roots of the repo's lore projects that need
// the check: client repos, and repos lore cannot place (fail closed).
func pushLeakProjects(ctx context.Context, top string) ([]string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", top, "ls-files", "-z", "--", metaGlob).Output()
	if err != nil {
		return nil, nil // not a git checkout lore can read: nothing of lore's to guard
	}
	var roots []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) == 0 {
			continue
		}
		dir := path.Dir(path.Dir(path.Dir(string(p)))) // <root>/.lore/data/_meta.json → <root>
		root := top
		if dir != "." {
			root = top + "/" + dir
		}
		s, err := gatherOrgSituation(ctx, root)
		if err != nil {
			return nil, err
		}
		if s.ResolveErr != nil {
			return nil, orgResolveCLIError(s.ResolveErr)
		}
		if writeNeedsLeakCheck(s) {
			roots = append(roots, root)
		}
	}
	return roots, nil
}

// pushItems lists what the push would send that the remote does not have:
// every new blob (as a file item, at the first path git reports for it)
// and every new commit's message. "New" means not reachable from the
// remote's tracking refs nor from the remote ref's current tip.
func pushItems(ctx context.Context, top, remote string, updates []pushUpdate) ([]leakcheck.Item, error) {
	args := []string{"-C", top, "rev-list", "--objects"}
	tips := 0
	for _, u := range updates {
		if isZeroOID(u.localSHA) {
			continue // a delete sends nothing
		}
		args = append(args, u.localSHA)
		tips++
		if !isZeroOID(u.remoteSHA) && objectExists(ctx, top, u.remoteSHA) {
			args = append(args, "^"+u.remoteSHA)
		}
	}
	if tips == 0 {
		return nil, nil
	}
	notRemote := "--remotes"
	if remote != "" {
		notRemote += "=" + remote
	}
	args = append(args, "--not", notRemote)
	out, err := exec.CommandContext(ctx, "git", args...).Output()
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
	return readPushObjects(ctx, top, oids, paths)
}

// objectExists reports whether the local repository holds oid (the
// remote's tip may be a commit this clone never fetched).
func objectExists(ctx context.Context, top, oid string) bool {
	return exec.CommandContext(ctx, "git", "-C", top, "cat-file", "-e", oid+"^{commit}").Run() == nil
}

// shortOIDLen is how many hex digits of a commit id a finding shows.
const shortOIDLen = 7

// Object types `git cat-file --batch` reports that the check reads.
const (
	objectBlob   = "blob"
	objectCommit = "commit"
)

// readPushObjects reads oids with one `git cat-file --batch` and turns
// blobs and commits into leak-check items (trees and tags carry nothing a
// commit or blob does not).
func readPushObjects(ctx context.Context, top string, oids []string, paths map[string]string) ([]leakcheck.Item, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", top, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(oids, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cat-file: %w", err)
	}
	r := bufio.NewReader(bytes.NewReader(out))
	var items []leakcheck.Item
	for range oids {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("cat-file: short output: %w", err)
		}
		f := strings.Fields(header) // "<oid> <type> <size>" or "<oid> missing"
		if len(f) != 3 {
			return nil, fmt.Errorf("cat-file: object %s: %s", f[0], strings.TrimSpace(header))
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
			items = append(items, leakcheck.Item{Where: p, Path: p, Content: body})
		case objectCommit:
			_, msg, _ := bytes.Cut(body, []byte("\n\n"))
			items = append(items, leakcheck.Item{Where: "message of commit " + f[0][:shortOIDLen], Content: msg})
		}
	}
	return items, nil
}
