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
	"github.com/thesatellite-ai/lore/adapters/gitfast"
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
	names := make([]string, len(batch))
	for i, p := range batch {
		names[i] = rev + ":" + p
	}
	if rev != "" && len(batch) >= revTreeMin {
		// "<rev>:<path>" makes git walk the tree from the commit for every
		// path: a live ref with 5,000 rows in one folder rescanned that
		// folder 5,000 times (15 s per read, T-12/T-20). One ls-tree gives
		// every blob id; cat-file then reads by id.
		oids, terr := treeBlobOIDs(ctx, top, env, rev)
		if terr != nil {
			return nil, terr
		}
		kept := batch[:0:0]
		names = names[:0]
		for _, p := range batch {
			if oid, ok := oids[p]; ok {
				kept = append(kept, p)
				names = append(names, oid)
			}
		}
		batch = kept
	}
	bodies, err := catFileBatch(ctx, top, env, names)
	if err != nil {
		return nil, err
	}
	for i, p := range batch {
		if bodies[i] != nil {
			out[p] = bodies[i]
		}
	}
	return out, nil
}

// revTreeMin is the path count from which revBlobs lists the tree once and
// reads by blob id instead of naming "<rev>:<path>" per path.
const revTreeMin = 32

// treeBlobOIDs maps every blob path in rev's tree (from the repo root) to
// its blob id, through one `git ls-tree -r -z --full-tree`.
func treeBlobOIDs(ctx context.Context, top string, env []string, rev string) (map[string]string, error) {
	listing, err := (gitRun{dir: top, env: env, args: []string{"ls-tree", "-r", "-z", "--full-tree", rev}}).output(ctx)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: git ls-tree "+rev, err)
	}
	out := map[string]string{}
	for _, entry := range bytes.Split(listing, []byte{0}) {
		meta, path, ok := strings.Cut(string(entry), "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta) // mode type oid
		if len(f) == 3 && f[1] == "blob" {
			out[path] = f[2]
		}
	}
	return out, nil
}

// catFileBatch reads each object name (a blob id, "<rev>:<path>" or
// ":<path>") through one `git cat-file --batch`; the body is nil for a name
// that is missing or is not a blob.
func catFileBatch(ctx context.Context, top string, env []string, names []string) ([][]byte, error) {
	out := make([][]byte, len(names))
	if len(names) == 0 {
		return out, nil
	}
	bin, err := exec.LookPath(gitbackend.DefaultBinary)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: git is not installed", err)
	}
	var in bytes.Buffer
	for _, n := range names {
		in.WriteString(n + "\n")
	}
	// Built directly, as gitRun does: an absolute LookPath'd binary, fixed
	// argv entries, no shell; runCancellable kills it when ctx ends.
	stdin := in.Bytes()
	cmd := &exec.Cmd{Path: bin, Args: []string{bin, "cat-file", "--batch"}, Dir: top, Env: gitEnv(env), Stdin: bytes.NewReader(stdin)}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// gitfast (devco:D-418): the objects are read from the repository files
	// when the Session can; else git runs as before
	raw, rerr := gitfast.Output(ctx, top, cmd.Args[1:], env, stdin, func() ([]byte, error) {
		err := runCancellable(ctx, cmd)
		return stdout.Bytes(), err
	})
	if rerr != nil {
		return nil, errs.Wrap(errs.Internal, "wire: git cat-file: "+strings.TrimSpace(stderr.String()), rerr)
	}
	rd := bufio.NewReader(bytes.NewReader(raw))
	for i := range names {
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
			out[i] = body
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
