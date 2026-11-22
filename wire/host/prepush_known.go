package host

// prepush_known.go — text the remote already has is not new to a push
// (public issue #128, devco:D-905). The pre-push F16 check reads the lines
// the pushed commits add (devco:D-728). After a rebase every commit of the
// branch is new, so every line of the branch counted as added, even a line
// byte-identical at the same path on the remote ref the force-push
// replaces: product copy pushed before an internal note quoted it was
// refused on the next rebase + force-push. Such a line, and a rebased
// commit's message the remote already holds, are already shared with that
// remote: refusing them protects nothing.
//
//	F16 refuses a doc set ─▶ pushRemoteKnown (once per push, so a clean
//	push starts no extra git process: the hook's git budget, D-780)
//	   tips the remote has: the replaced remote tips, the remote's
//	   tracking refs (newest first), the commits of tags it holds
//	   ─▶ `cat-file --batch-check` <tip>:<path> for every pushed path
//	   ─▶ `cat-file --batch` of those versions ─▶ per path, its lines
//	   ─▶ `log -z --format=%B <tips> --not <pushed tips>` ─▶ messages
//	─▶ the set is checked again with those lines (and messages) dropped
//	   from what it adds; a line the push really adds is still refused.
//
// Only that remote counts (text on another remote is new to this one),
// and only the same path (a line moved to another file is checked again).
// Any git error leaves the first refusal standing (fails closed).

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/wire"
)

// Limits of what a refused push reads from the remote's history.
const (
	maxKnownTips     = 64      // remote tips whose files are read
	maxKnownMessages = 2000    // remote commits whose messages are read
	maxKnownBlob     = 8 << 20 // a bigger file version is not read (its lines stay new)
)

// remoteKnown is the text a remote already has, as the pre-push check
// compares it: per path, the lines that path holds on one of the remote's
// tips; and the commit messages of the remote's recent commits (trimmed).
type remoteKnown struct {
	lines    map[string]map[string]bool
	messages map[string]bool
}

// empty reports whether k names nothing (no re-check is worth running).
func (k remoteKnown) empty() bool { return len(k.lines) == 0 && len(k.messages) == 0 }

// knownFunc returns what the remote of a push already has; checkPushDocs
// calls it only after a refusal, at most once.
type knownFunc func(paths []string) remoteKnown

// pushKnown is the knownFunc of a push to remote with updates.
func pushKnown(ctx context.Context, repo *gitref.Repo, remote string, updates []pushUpdate) knownFunc {
	return func(paths []string) remoteKnown { return pushRemoteKnown(ctx, repo, remote, updates, paths) }
}

// dropKnownLines returns lines (path → the line numbers the push adds)
// without the lines whose text the remote already holds at that path, and
// marks a commit message the remote already holds as adding nothing. docs
// is the doc set the line numbers refer to. lines itself is not changed.
func dropKnownLines(lines map[string]map[int]bool, docs map[string][]byte, known remoteKnown) map[string]map[int]bool {
	out := make(map[string]map[int]bool, len(lines))
	for p, nums := range lines {
		out[p] = nums
		have := known.lines[p]
		if len(have) == 0 {
			continue
		}
		text := strings.Split(string(docs[p]), "\n")
		kept := map[int]bool{}
		for n := range nums {
			if n < 1 || n > len(text) || !have[text[n-1]] {
				kept[n] = true
			}
		}
		out[p] = kept
	}
	for p, b := range docs {
		if strings.HasPrefix(p, commitMsgWhere) && known.messages[strings.TrimSpace(string(b))] {
			out[p] = map[int]bool{} // the remote holds this message already: nothing of it is new
		}
	}
	return out
}

// pushRemoteKnown reads what remote already has of paths (see the file
// comment). An empty result on any git error or when remote is "".
func pushRemoteKnown(ctx context.Context, repo *gitref.Repo, remote string, updates []pushUpdate, paths []string) remoteKnown {
	known := remoteKnown{lines: map[string]map[string]bool{}, messages: map[string]bool{}}
	if remote == "" {
		return known
	}
	tips, pushed := knownTips(ctx, repo, remote, updates)
	if len(tips) == 0 {
		return known
	}
	known.lines = knownPathLines(ctx, repo, tips, paths)
	known.messages = knownMessages(ctx, repo, tips, pushed)
	return known
}

// knownTips are the commits remote has, newest first and at most
// maxKnownTips: each replaced remote tip this clone holds, the remote's
// tracking refs, the commits of the tags it holds. pushed are the tips
// the push sends.
func knownTips(ctx context.Context, repo *gitref.Repo, remote string, updates []pushUpdate) (tips, pushed []string) {
	seen := map[string]bool{}
	add := func(c string) {
		if c != "" && !seen[c] && len(tips) < maxKnownTips {
			seen[c] = true
			tips = append(tips, c)
		}
	}
	for _, u := range updates {
		if !isZeroOID(u.localSHA) {
			pushed = append(pushed, u.localSHA)
		}
		if !isZeroOID(u.remoteSHA) {
			if _, err := repo.Run(ctx, gitref.Invocation{Args: []string{"cat-file", "-e", u.remoteSHA + "^{commit}"}}); err == nil {
				add(u.remoteSHA)
			}
		}
	}
	out, err := repo.Run(ctx, gitref.Invocation{Args: []string{"for-each-ref", "--sort=-committerdate", "--format=%(objectname)", "refs/remotes/" + remote + "/"}})
	if err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			add(strings.TrimSpace(l))
		}
	}
	for _, c := range wire.RemoteTagCommits(ctx, repo.Dir(), []string{remote}) {
		add(c)
	}
	return tips, pushed
}

// knownPathLines is, per path, the lines it holds on any of tips (one
// `cat-file --batch-check` for the versions, one `cat-file --batch` to
// read the distinct ones).
func knownPathLines(ctx context.Context, repo *gitref.Repo, tips, paths []string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	var queries []string
	var qpath []string
	for _, p := range paths {
		if p == "" || strings.ContainsAny(p, "\n\r") || strings.HasPrefix(p, commitMsgWhere) {
			continue
		}
		for _, t := range tips {
			queries = append(queries, t+":"+p)
			qpath = append(qpath, p)
		}
	}
	if len(queries) == 0 {
		return out
	}
	res, err := repo.Run(ctx, gitref.Invocation{Args: []string{"cat-file", "--batch-check"}, Stdin: []byte(strings.Join(queries, "\n") + "\n")})
	if err != nil {
		return out
	}
	blobPaths := map[string][]string{} // version → the paths it is on
	var blobs []string
	for i, l := range strings.Split(strings.TrimRight(string(res), "\n"), "\n") {
		f := strings.Fields(l) // "<oid> blob <size>", or "<query> missing"
		if i >= len(qpath) || len(f) != 3 || f[1] != objectBlob {
			continue
		}
		if size, err := strconv.Atoi(f[2]); err != nil || size > maxKnownBlob {
			continue
		}
		if _, ok := blobPaths[f[0]]; !ok {
			blobs = append(blobs, f[0])
		}
		blobPaths[f[0]] = append(blobPaths[f[0]], qpath[i])
	}
	if len(blobs) == 0 {
		return out
	}
	body, err := repo.Run(ctx, gitref.Invocation{Args: []string{"cat-file", "--batch"}, Stdin: []byte(strings.Join(blobs, "\n") + "\n")})
	if err != nil {
		return out
	}
	r := bufio.NewReader(bytes.NewReader(body))
	for range blobs {
		header, err := r.ReadString('\n')
		if err != nil {
			return out
		}
		f := strings.Fields(header)
		if len(f) != 3 {
			return out
		}
		size, err := strconv.Atoi(f[2])
		if err != nil {
			return out
		}
		content := make([]byte, size+1) // content and the trailing newline
		if _, err := io.ReadFull(r, content); err != nil {
			return out
		}
		for _, p := range blobPaths[f[0]] {
			if out[p] == nil {
				out[p] = map[string]bool{}
			}
			for _, line := range strings.Split(string(content[:size]), "\n") {
				if line != "" {
					out[p][line] = true
				}
			}
		}
	}
	return out
}

// knownMessages are the trimmed messages of the newest maxKnownMessages
// commits of tips that the pushed tips do not reach (a rebased commit's
// original is among them). Empty on any git error.
func knownMessages(ctx context.Context, repo *gitref.Repo, tips, pushed []string) map[string]bool {
	out := map[string]bool{}
	args := append([]string{"log", "-z", "--format=%B", "-n", strconv.Itoa(maxKnownMessages)}, tips...)
	if len(pushed) > 0 {
		args = append(append(args, "--not"), pushed...)
	}
	res, err := repo.Run(ctx, gitref.Invocation{Args: append(args, "--")})
	if err != nil {
		return out
	}
	for _, m := range strings.Split(string(res), "\x00") {
		if m = strings.TrimSpace(m); m != "" {
			out[m] = true
		}
	}
	return out
}
