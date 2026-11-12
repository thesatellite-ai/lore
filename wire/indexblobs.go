package wire

// indexblobs.go — read many files from the git index through one
// `git cat-file --batch` (item 30: the commit hook spawned one `git show`
// per staged file, ~2 ms each, so a 5,000-row migration commit spent ~10 s
// starting processes before scanning anything).
//
//	paths ─▶ ":<path>\n" per path on stdin ─▶ git cat-file --batch
//	      ─▶ "<sha> <type> <size>\n<body>\n" per path, in order
//	         ("<name> missing\n" for a path not in the index: skipped)

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
)

// indexBlobs returns the index content of each path under the repo top
// that is a blob there. A path that is not in the index, or names a
// submodule or a tree, is left out (the per-file `git show` reads skipped
// those too). A path holding a newline cannot be named on cat-file's
// line-based stdin; it is read alone with `git show`.
// env carries the hook's index (hookIndexEnv: GIT_INDEX_FILE for `git commit -a`
// and friends) so the blobs read are the ones being committed (item 32).
func indexBlobs(ctx context.Context, top string, env []string, paths []string) (map[string][]byte, error) {
	return revBlobs(ctx, top, env, "", paths)
}

// revBlobs is indexBlobs for any tree: rev "" reads the index (":<path>"),
// a commit-ish such as "HEAD" reads that commit's tree ("HEAD:<path>"),
// so the commit check can compare a staged row with the committed one in
// the same single git process.
func revBlobs(ctx context.Context, top string, env []string, rev string, paths []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(paths))
	batch := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		if strings.ContainsAny(p, "\n\r") {
			if b, err := (gitRun{dir: top, env: env, args: []string{"show", rev + ":" + p}}).output(ctx); err == nil {
				out[p] = b
			}
			continue
		}
		batch = append(batch, p)
	}
	if len(batch) == 0 {
		return out, nil
	}
	bin, err := exec.LookPath(gitbackend.DefaultBinary)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: git is not installed", err)
	}
	var in bytes.Buffer
	for _, p := range batch {
		in.WriteString(rev + ":" + p + "\n")
	}
	// Built directly, as gitRun does: an absolute LookPath'd binary, fixed
	// argv entries, no shell; runCancellable kills it when ctx ends.
	cmd := &exec.Cmd{Path: bin, Args: []string{bin, "cat-file", "--batch"}, Dir: top, Env: gitEnv(env), Stdin: &in}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if rerr := runCancellable(ctx, cmd); rerr != nil {
		return nil, errs.Wrap(errs.Internal, "wire: git cat-file: "+strings.TrimSpace(stderr.String()), rerr)
	}
	rd := bufio.NewReader(&stdout)
	for _, p := range batch {
		hdr, rerr := rd.ReadString('\n')
		if rerr != nil {
			return nil, errs.New(errs.Internal, "wire: git cat-file: output ended early")
		}
		typ, size, ok := batchHeader(strings.TrimSuffix(hdr, "\n"))
		if !ok {
			continue // "<name> missing" / "ambiguous": no body follows
		}
		body := make([]byte, size)
		if _, rerr = io.ReadFull(rd, body); rerr != nil {
			return nil, errs.New(errs.Internal, "wire: git cat-file: object cut short")
		}
		_, _ = rd.ReadByte() // the newline after each object
		if typ == "blob" {
			out[p] = body
		}
	}
	return out, nil
}

// batchHeader parses "<sha> <type> <size>"; false for any other line.
func batchHeader(h string) (typ string, size int, ok bool) {
	f := strings.Split(h, " ")
	if len(f) != 3 {
		return "", 0, false
	}
	switch f[1] {
	case "blob", "tree", "commit", "tag":
	default:
		return "", 0, false
	}
	n, err := strconv.Atoi(f[2])
	if err != nil || n < 0 {
		return "", 0, false
	}
	return f[1], n, true
}
