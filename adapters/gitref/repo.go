package gitref

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Repo is one git repository (normal, linked worktree or bare), addressed by
// a directory inside it. Every method runs git in Dir through the Git seam.
//
// Invariant: a Repo's fields are fixed by Open, so one Repo may be used
// from many goroutines at once. The one thing it fills in later is idx, a
// memo of facts that never change (a commit's tree listing, a blob's
// bytes, both keyed by object id), behind its own lock.
type Repo struct {
	dir  string
	git  Git
	bare bool
	zero string
	idx  *treeIndex
}

// Sentinels.
var (
	// ErrNotRepo means the directory is not inside a git repository.
	ErrNotRepo = errors.New("gitref: not a git repository")
	// ErrNotFound means a revision, ref or path does not exist.
	ErrNotFound = errors.New("gitref: not found")
	// ErrBadRef means a ref name is not a full, valid name under refs/.
	ErrBadRef = errors.New("gitref: invalid ref name")
	// ErrBadPath means a tree path is empty, absolute, or climbs out of
	// the tree.
	ErrBadPath = errors.New("gitref: invalid tree path")
	// ErrCheckedOut refuses to move a branch that a work tree has checked
	// out: its index and files would silently describe another commit.
	ErrCheckedOut = errors.New("gitref: refusing to move a checked-out branch")
	// ErrRefMoved is wrapped by *CASError: the ref was not at the expected
	// commit.
	ErrRefMoved = errors.New("gitref: ref moved")
	// ErrUnsupportedEntry means a tree holds a symlink or submodule where
	// lore expects only regular files.
	ErrUnsupportedEntry = errors.New("gitref: tree entry is not a regular file")
)

// Open returns the repository containing dir. g nil means Exec{}.
func Open(ctx context.Context, dir string, g Git) (*Repo, error) {
	if g == nil {
		g = Exec{}
	}
	out, err := g.Run(ctx, Invocation{Dir: dir, Args: []string{"rev-parse", "--is-bare-repository", "--show-object-format"}})
	if errors.Is(err, ErrNoGit) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %s", ErrNotRepo, dir, Stderr(err))
	}
	lines := strings.Fields(string(out))
	if len(lines) != openLines {
		return nil, fmt.Errorf("gitref: unexpected rev-parse output %q", out)
	}
	zeroLen, ok := oidHexLen[lines[1]]
	if !ok {
		return nil, fmt.Errorf("gitref: unknown object format %q", lines[1])
	}
	return &Repo{dir: dir, git: g, bare: lines[0] == "true", zero: strings.Repeat("0", zeroLen), idx: newTreeIndex()}, nil
}

// openLines is how many values Open's rev-parse prints.
const openLines = 2

// oidHexLen maps git's object formats to their hex id length.
var oidHexLen = map[string]int{"sha1": 40, "sha256": 64}

// Dir is the directory the Repo was opened with.
func (r *Repo) Dir() string { return r.dir }

// Bare reports whether the repository has no work tree.
func (r *Repo) Bare() bool { return r.bare }

// ZeroOID is the all-zero object id of this repository's hash ("the ref
// must not exist" in compare-and-swap and in push leases).
func (r *Repo) ZeroOID() string { return r.zero }

// Run is the one escape hatch: it runs any git command in this repository
// through the same seam, for the rare plumbing call this package does not
// wrap. Output is untouched.
func (r *Repo) Run(ctx context.Context, inv Invocation) ([]byte, error) {
	inv.Dir = r.dir
	return r.git.Run(ctx, inv)
}

// run runs git in the repo and returns trimmed stdout.
func (r *Repo) run(ctx context.Context, env []string, stdin []byte, args ...string) (string, error) {
	out, err := r.git.Run(ctx, Invocation{Dir: r.dir, Args: args, Env: env, Stdin: stdin})
	return strings.TrimSpace(string(out)), err
}

// ValidateRef checks that ref is a full ref name under refs/ that git
// accepts. Short names ("main") are refused: they are ambiguous between a
// branch, a tag and a remote-tracking ref, and a plumbing write to the wrong
// one is silent.
func (r *Repo) ValidateRef(ctx context.Context, ref string) error {
	if !strings.HasPrefix(ref, refsPrefix) {
		return fmt.Errorf("%w: %q (want a full name under refs/)", ErrBadRef, ref)
	}
	if _, err := r.run(ctx, nil, nil, "check-ref-format", ref); err != nil {
		return fmt.Errorf("%w: %q", ErrBadRef, ref)
	}
	return nil
}

// refsPrefix starts every full ref name.
const refsPrefix = "refs/"

// headsPrefix starts every branch ref.
const headsPrefix = "refs/heads/"

// ResolveRef returns the object id ref points at. ok is false when the ref
// does not exist. ref must be a full name (ValidateRef).
func (r *Repo) ResolveRef(ctx context.Context, ref string) (oid string, ok bool, err error) {
	if verr := r.ValidateRef(ctx, ref); verr != nil {
		return "", false, verr
	}
	// for-each-ref matches patterns as prefixes of whole components, so
	// "refs/lore/live" also lists refs/lore/live/x; the exact ref, when
	// present, is the only line whose name equals the pattern. (rev-parse
	// would also accept the name but falls back to other ref namespaces.)
	named, err := r.run(ctx, nil, nil, "for-each-ref", "--format=%(objectname) %(refname)", "--", ref)
	if err != nil {
		return "", false, err
	}
	for line := range strings.SplitSeq(named, "\n") {
		id, name, _ := strings.Cut(line, " ")
		if name == ref {
			return id, true, nil
		}
	}
	return "", false, nil
}

// ResolveCommit returns the commit id rev names (any revision syntax: a
// full ref, an oid, "origin/main~2"). ok is false when it names nothing.
func (r *Repo) ResolveCommit(ctx context.Context, rev string) (oid string, ok bool, err error) {
	out, err := r.run(ctx, nil, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+commitPeel)
	if err != nil {
		if code, isExit := ExitCode(err); isExit && code == gitExitNo {
			return "", false, nil
		}
		return "", false, err
	}
	return out, true, nil
}

// commitPeel and treePeel make rev-parse resolve a revision to its commit or
// its tree.
const (
	commitPeel = "^{commit}"
	treePeel   = "^{tree}"
)

// gitExitNo is git's exit status for a negative answer that is not a
// failure (rev-parse --verify --quiet: no such object; merge-base
// --is-ancestor: not an ancestor).
const gitExitNo = 1

// TreeOf returns the tree id of commit rev.
func (r *Repo) TreeOf(ctx context.Context, rev string) (string, error) {
	out, err := r.run(ctx, nil, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+treePeel)
	if err != nil {
		if code, isExit := ExitCode(err); isExit && code == gitExitNo {
			return "", fmt.Errorf("%w: %s", ErrNotFound, rev)
		}
		return "", err
	}
	return out, nil
}

// ListRefs returns every ref under prefix (a full prefix such as
// "refs/lore-remote/origin/"), keyed by full name.
func (r *Repo) ListRefs(ctx context.Context, prefix string) (map[string]string, error) {
	out, err := r.run(ctx, nil, nil, "for-each-ref", "--format=%(objectname) %(refname)", "--", prefix)
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for line := range strings.SplitSeq(out, "\n") {
		if id, name, ok := strings.Cut(line, " "); ok && strings.HasPrefix(name, prefix) {
			refs[name] = id
		}
	}
	return refs, nil
}

// IsAncestor reports whether commit a is an ancestor of (or equal to)
// commit b.
func (r *Repo) IsAncestor(ctx context.Context, a, b string) (bool, error) {
	_, err := r.run(ctx, nil, nil, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	if code, isExit := ExitCode(err); isExit && code == gitExitNo {
		return false, nil
	}
	return false, err
}

// MergeBase returns the best common ancestor of a and b; ok is false when
// they share no history (two parentless lines, as after a compaction).
func (r *Repo) MergeBase(ctx context.Context, a, b string) (oid string, ok bool, err error) {
	out, err := r.run(ctx, nil, nil, "merge-base", a, b)
	if err == nil {
		return out, true, nil
	}
	if code, isExit := ExitCode(err); isExit && code == gitExitNo {
		return "", false, nil
	}
	return "", false, err
}

// Entry modes git uses in trees.
const (
	modeRegular    = "100644"
	modeExecutable = "100755"
)

// ReadTree returns every file under prefix in revision rev, keyed by path
// relative to the repository root (slash separated). prefix "" means the
// whole tree; a prefix matching nothing gives an empty map. Works in bare
// repositories and from any subfolder of a work tree, because paths are
// always root-relative (C3).
//
// A symlink or submodule under prefix is an ErrUnsupportedEntry: lore trees
// hold only regular files, and silently skipping one would hide data.
func (r *Repo) ReadTree(ctx context.Context, rev, prefix string) (map[string][]byte, error) {
	if _, ok, err := r.ResolveCommit(ctx, rev); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, rev)
	}
	args := []string{"ls-tree", "-r", "-z", "--full-tree", rev}
	if prefix != "" {
		clean, err := cleanPath(prefix)
		if err != nil {
			return nil, err
		}
		args = append(args, "--", clean)
	}
	out, err := r.git.Run(ctx, Invocation{Dir: r.dir, Args: args})
	if err != nil {
		return nil, err
	}
	var paths, oids []string
	for rec := range strings.SplitSeq(string(out), "\x00") {
		if rec == "" {
			continue
		}
		meta, path, ok := strings.Cut(rec, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != lsTreeFields {
			return nil, fmt.Errorf("gitref: unexpected ls-tree record %q", rec)
		}
		if fields[0] != modeRegular && fields[0] != modeExecutable {
			return nil, fmt.Errorf("%w: %s (mode %s)", ErrUnsupportedEntry, path, fields[0])
		}
		paths = append(paths, path)
		oids = append(oids, fields[2])
	}
	files := make(map[string][]byte, len(paths))
	if len(paths) == 0 {
		return files, nil
	}
	blobs, err := r.catBlobs(ctx, oids)
	if err != nil {
		return nil, err
	}
	for i, p := range paths {
		files[p] = blobs[i]
	}
	return files, nil
}

// lsTreeFields is the field count before the tab in an ls-tree record:
// "<mode> <type> <oid>".
const lsTreeFields = 3

// ReadFile returns one file of revision rev (path root-relative). ok is false
// when rev has no such file; a missing rev is ErrNotFound.
func (r *Repo) ReadFile(ctx context.Context, rev, path string) ([]byte, bool, error) {
	if dir, _, nested := cutLastSlash(path); nested && r.idx != nil {
		return r.readIndexed(ctx, rev, path, dir)
	}
	files, err := r.ReadTree(ctx, rev, path)
	if err != nil {
		return nil, false, err
	}
	clean, _ := cleanPath(path) // ReadTree already validated it
	b, ok := files[clean]
	return b, ok, nil
}

// catBlobs reads blobs by id with one `git cat-file --batch`.
func (r *Repo) catBlobs(ctx context.Context, oids []string) ([][]byte, error) {
	var in bytes.Buffer
	for _, id := range oids {
		in.WriteString(id)
		in.WriteByte('\n')
	}
	out, err := r.git.Run(ctx, Invocation{Dir: r.dir, Args: []string{"cat-file", "--batch"}, Stdin: in.Bytes()})
	if err != nil {
		return nil, err
	}
	rd := bufio.NewReader(bytes.NewReader(out))
	blobs := make([][]byte, len(oids))
	for i, id := range oids {
		header, err := rd.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("gitref: cat-file header for %s: %w", id, err)
		}
		fields := strings.Fields(header)
		if len(fields) != catFileHeaderFields {
			return nil, fmt.Errorf("gitref: cat-file %s: %s", id, strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("gitref: cat-file size: %w", err)
		}
		buf := make([]byte, size+1) // content + trailing LF
		if _, err := io.ReadFull(rd, buf); err != nil {
			return nil, fmt.Errorf("gitref: cat-file body %s: %w", id, err)
		}
		blobs[i] = buf[:size]
	}
	return blobs, nil
}

// catFileHeaderFields is the field count of a `git cat-file --batch` header
// line: "<object> <type> <size>".
const catFileHeaderFields = 3

// cleanPath validates a tree path and returns it slash-separated without a
// trailing slash. Absolute paths, "." and ".." components, empty components
// and a ".git" component are refused: each would either escape the tree or
// write something git itself treats specially.
func cleanPath(p string) (string, error) {
	p = strings.TrimSuffix(strings.ReplaceAll(p, "\\", "/"), "/")
	if p == "" || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%w: %q", ErrBadPath, p)
	}
	for part := range strings.SplitSeq(p, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return "", fmt.Errorf("%w: %q", ErrBadPath, p)
		}
	}
	return p, nil
}
