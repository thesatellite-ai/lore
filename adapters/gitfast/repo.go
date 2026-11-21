package gitfast

// repo.go — finds a repository's layout (work tree top, git dir, common
// dir) the way git's discovery does for the plain layouts lore meets, and
// reads its refs from the ref files. Anything unusual (reftable, sha256,
// core.worktree, includes in the repo config, a repository owned by
// another user, GIT_CEILING_DIRECTORIES) makes discover return ok=false so
// the caller runs git.

import (
	"bufio"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// repo is one discovered repository.
type repo struct {
	top    string // work tree top ("" for a bare repository)
	gitDir string // this work tree's git dir (absolute, cleaned, links resolved)
	// rawGitDir is the git dir as found, before links were resolved
	// (includeIf gitdir: matches either form).
	rawGitDir string
	common    string // the common dir (== gitDir unless a linked worktree)
	bare      bool
	// indexFile is the index the index readers read ("" = gitDir/index):
	// a hook's GIT_INDEX_FILE (withIndex)
	indexFile string

	objOnce sync.Once
	objs    *objectStore
	objErr  error
	// objsShared: objs is kept across Sessions (objpool.go), so Close
	// leaves it open.
	objsShared bool

	packedMu  sync.Mutex
	packedKey string // size+mtime of packed-refs when packed was read
	packed    map[string]packedRef
}

// packedRef is one packed-refs line with its peeled id (^ line), if any.
type packedRef struct {
	id, peeled string
}

// objects opens the object store once.
func (r *repo) objects() (*objectStore, error) {
	r.objOnce.Do(func() { r.objs, r.objsShared, r.objErr = openObjectStore(filepath.Join(r.common, "objects")) })
	return r.objs, r.objErr
}

// withIndex is r reading the index file index (absolute) instead of its
// own: the temporary index `git commit -a` gives its hooks. It shares r's
// object store; refs are read from the same files.
func (r *repo) withIndex(index string) *repo {
	objs, err := r.objects()
	v := &repo{top: r.top, gitDir: r.gitDir, rawGitDir: r.rawGitDir, common: r.common, bare: r.bare, indexFile: index}
	v.objOnce.Do(func() { v.objs, v.objErr = objs, err })
	return v
}

// discover finds the repository dir belongs to. ok=false means "ask git".
func discover(dir string) (*repo, bool) {
	if os.Getenv("GIT_CEILING_DIRECTORIES") != "" || os.Getenv("GIT_DISCOVERY_ACROSS_FILESYSTEM") != "" {
		return nil, false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, false
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return nil, false
	}
	for d := abs; ; {
		dotgit := filepath.Join(d, ".git")
		if fi, err := os.Lstat(dotgit); err == nil {
			switch {
			case fi.Mode().IsDir():
				if isGitDir(dotgit) {
					return finish(d, dotgit, false)
				}
			case fi.Mode().IsRegular():
				gd, ok := readGitFile(dotgit)
				if !ok {
					return nil, false
				}
				return finish(d, gd, false)
			default:
				return nil, false // a symlinked .git: leave to git
			}
		}
		if isGitDir(d) {
			// a bare repository (or a git dir itself): git treats it as
			// bare unless core.bare=false; lore never runs reads from
			// inside a non-bare .git, so ask git for that case.
			return finish("", d, true)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return nil, false
		}
		d = parent
	}
}

// isGitDir is git's is_git_directory: HEAD, objects/ and refs/ present.
func isGitDir(d string) bool {
	if fi, err := os.Stat(filepath.Join(d, "HEAD")); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	common := d
	if raw, err := os.ReadFile(filepath.Join(d, "commondir")); err == nil {
		c := strings.TrimSpace(string(raw))
		if !filepath.IsAbs(c) {
			c = filepath.Join(d, c)
		}
		common = filepath.Clean(c)
	}
	for _, sub := range []string{"objects", "refs"} {
		if fi, err := os.Stat(filepath.Join(common, sub)); err != nil || !fi.IsDir() {
			return false
		}
	}
	return true
}

// readGitFile reads a "gitdir: <path>" file.
func readGitFile(p string) (string, bool) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	gd, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "gitdir: ")
	if !ok || gd == "" {
		return "", false
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(filepath.Dir(p), gd)
	}
	gd = filepath.Clean(gd)
	if !isGitDir(gd) {
		return "", false
	}
	return gd, true
}

// finish fills a repo after discovery and refuses the layouts git would
// treat differently from the plain reading here.
func finish(top, gitDir string, bare bool) (*repo, bool) {
	r := &repo{top: top, gitDir: gitDir, rawGitDir: gitDir, common: gitDir, bare: bare}
	if raw, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		c := strings.TrimSpace(string(raw))
		if !filepath.IsAbs(c) {
			c = filepath.Join(gitDir, c)
		}
		r.common = filepath.Clean(c)
	}
	if !plainConfig(filepath.Join(r.common, "config"), bare) {
		return nil, false
	}
	if r.gitDir != r.common && fileExists(filepath.Join(r.gitDir, "config.worktree")) {
		return nil, false
	}
	if !ownedByMe(gitDir) || (top != "" && !ownedByMe(top)) {
		return nil, false // safe.directory rules: let git decide
	}
	// git resolves symlinks in what it prints for the top and the git dir
	if top != "" {
		real, err := filepath.EvalSymlinks(top)
		if err != nil {
			return nil, false
		}
		r.top = real
	}
	for _, p := range []*string{&r.gitDir, &r.common} {
		real, err := filepath.EvalSymlinks(*p)
		if err != nil {
			return nil, false
		}
		*p = real
	}
	return r, true
}

// fileExists reports whether p exists.
func fileExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// plainConfig reads the repository config and reports whether nothing in
// it changes how this package reads the repository: no includes, no
// core.worktree, sha1 objects, files ref storage, and core.bare agreeing
// with the layout found.
func plainConfig(path string, bare bool) bool {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	section := ""
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			end := strings.IndexByte(line, ']')
			if end < 0 {
				return false
			}
			section = strings.ToLower(strings.TrimSpace(line[1:end]))
			if strings.HasPrefix(section, "include") {
				return false
			}
			line = strings.TrimSpace(line[end+1:])
			if line == "" {
				continue
			}
		}
		key, val, _ := strings.Cut(line, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.ToLower(strings.Trim(strings.TrimSpace(val), "\""))
		switch section + "." + key {
		case "core.worktree":
			return false
		case "core.bare":
			isBare := val == "true" || val == "yes" || val == "on" || val == "1" || val == ""
			if isBare != bare {
				return false
			}
		case "extensions.objectformat":
			if val != "sha1" {
				return false
			}
		case "extensions.refstorage":
			if val != "files" {
				return false
			}
		case "extensions.worktreeconfig":
			if val != "false" {
				return false
			}
		}
	}
	return sc.Err() == nil
}

// refDir is the folder a ref lives in: per-worktree refs in the git dir,
// all others in the common dir.
func (r *repo) refDir(name string) string {
	if name == "HEAD" || strings.HasPrefix(name, "refs/worktree/") || strings.HasPrefix(name, "refs/bisect/") || strings.HasPrefix(name, "refs/rewritten/") || !strings.HasPrefix(name, "refs/") {
		return r.gitDir
	}
	return r.common
}

// readRef reads one ref without following symbolic refs: id or target.
// found=false: no such ref. ok=false: unreadable, ask git.
func (r *repo) readRef(name string) (id, target string, found, ok bool) {
	raw, err := os.ReadFile(filepath.Join(r.refDir(name), filepath.FromSlash(name)))
	switch {
	case err == nil:
		s := strings.TrimRight(string(raw), "\r\n")
		if t, isSym := strings.CutPrefix(s, "ref: "); isSym {
			return "", strings.TrimSpace(t), true, true
		}
		s = strings.TrimSpace(s)
		if !isFullID(s) {
			return "", "", false, false
		}
		return s, "", true, true
	case errors.Is(err, os.ErrNotExist), isNotDirErr(err):
	default:
		return "", "", false, false
	}
	if fi, err := os.Stat(filepath.Join(r.refDir(name), filepath.FromSlash(name))); err == nil && fi.IsDir() {
		// a folder where the ref would be: not a ref
		if !strings.HasPrefix(name, "refs/") {
			return "", "", false, true
		}
	}
	if !strings.HasPrefix(name, "refs/") {
		return "", "", false, true
	}
	packed, pok := r.packedRefs()
	if !pok {
		return "", "", false, false
	}
	if p, has := packed[name]; has {
		return p.id, "", true, true
	}
	return "", "", false, true
}

// isNotDirErr reports ENOTDIR (a ref path whose parent is a file).
func isNotDirErr(err error) bool {
	return errors.Is(err, syscall.ENOTDIR)
}

// resolveRef follows symbolic refs to an object id.
func (r *repo) resolveRef(name string) (id string, found, ok bool) {
	for range 6 {
		id, target, found, ok := r.readRef(name)
		if !ok || !found {
			return "", found, ok
		}
		if target == "" {
			return id, true, true
		}
		name = target
	}
	return "", false, false // a symref loop: git reports it
}

// packedRefs reads packed-refs, again when its size or time changed.
func (r *repo) packedRefs() (map[string]packedRef, bool) {
	p := filepath.Join(r.common, "packed-refs")
	fi, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]packedRef{}, true
	}
	if err != nil {
		return nil, false
	}
	key := fi.ModTime().String() + "/" + strconv.FormatInt(fi.Size(), 10)
	r.packedMu.Lock()
	defer r.packedMu.Unlock()
	if r.packed != nil && r.packedKey == key {
		return r.packed, true
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	out := map[string]packedRef{}
	last := ""
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case line == "" || line[0] == '#':
		case line[0] == '^':
			if last == "" || !isFullID(line[1:]) {
				return nil, false
			}
			pr := out[last]
			pr.peeled = line[1:]
			out[last] = pr
		default:
			id, name, ok := strings.Cut(line, " ")
			if !ok || !isFullID(id) {
				return nil, false
			}
			out[name] = packedRef{id: id}
			last = name
		}
	}
	r.packed, r.packedKey = out, key
	return out, true
}

// listRefs returns every ref (name -> id) under refs/, loose winning over
// packed, symbolic refs resolved. ok=false: ask git.
func (r *repo) listRefs() (map[string]string, bool) {
	packed, ok := r.packedRefs()
	if !ok {
		return nil, false
	}
	out := map[string]string{}
	for n, p := range packed {
		out[n] = p.id
	}
	var walk func(base, prefix string) bool
	walk = func(base, prefix string) bool {
		entries, err := os.ReadDir(base)
		if errors.Is(err, os.ErrNotExist) {
			return true
		}
		if err != nil {
			return false
		}
		for _, e := range entries {
			name := prefix + e.Name()
			full := filepath.Join(base, e.Name())
			if e.IsDir() {
				if !walk(full, name+"/") {
					return false
				}
				continue
			}
			if strings.HasSuffix(e.Name(), ".lock") {
				continue
			}
			id, found, rok := r.resolveRef(name)
			if !rok {
				return false
			}
			if found {
				out[name] = id
			} else {
				delete(out, name)
			}
		}
		return true
	}
	if !walk(filepath.Join(r.common, "refs"), "refs/") {
		return nil, false
	}
	if r.gitDir != r.common {
		for _, sub := range []string{"refs/worktree", "refs/bisect", "refs/rewritten"} {
			if !walk(filepath.Join(r.gitDir, filepath.FromSlash(sub)), sub+"/") {
				return nil, false
			}
		}
	}
	return out, true
}

// isFullID reports whether s is a full lowercase sha1 object id.
func isFullID(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// repoLocatingEnv are the variables that move git's repository discovery
// away from the folder walk noRepoAbove repeats; with any of them set the
// answer is left to git.
var repoLocatingEnv = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM"}

// noRepoAbove reports whether git finds no repository at or above dir:
// dir is a folder and neither it nor any folder above it holds a .git
// entry of any kind or is itself a git directory. Anything it cannot
// rule out (a .git it cannot read, a variable in repoLocatingEnv, a dir
// that is not a folder) is false, so git answers that case itself.
// Why (devco:D-826): a read outside every repository asked git up to ten
// times "where is the repository?" (rev-parse --show-toplevel and the
// like), each a process start of several milliseconds, to hear "none".
func noRepoAbove(dir string) bool {
	for _, k := range repoLocatingEnv {
		if os.Getenv(k) != "" {
			return false
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return false
	}
	for d := abs; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); !errors.Is(err, fs.ErrNotExist) {
			return false
		}
		if isGitDir(d) {
			return false
		}
		parent := filepath.Dir(d)
		if parent == d {
			return true
		}
		d = parent
	}
}

// noRepoMessage is git's standard error for a command that needs a
// repository run outside every one (git 2.x, unchanged since 1.8).
const noRepoMessage = "fatal: not a git repository (or any of the parent directories): .git"

// noRepoExit is git's exit status for that refusal.
const noRepoExit = 128

// noRepoReply is git's answer to a repository read run outside every
// repository.
func noRepoReply() Reply {
	return Reply{Code: noRepoExit, Stderr: noRepoMessage}
}

// repoFlags are the rev-parse options that ask about the repository and
// make git refuse outside one (needsRepo); --git-path takes a value.
var repoFlags = map[string]bool{
	"--show-toplevel": true, "--is-inside-work-tree": true, "--is-bare-repository": true, "--is-inside-git-dir": true,
	"--show-object-format": true, "--git-dir": true, "--absolute-git-dir": true, "--git-common-dir": true,
	"--show-prefix": true, "--path-format=absolute": true, "--path-format=relative": true,
}

// needsRepo reports whether rev-parse with args only asks about the
// repository (every argument a repoFlags option or --git-path <path>),
// which git refuses outside one with noRepoReply. Any other argument (a
// revision, --parseopt, --sq-quote, ...) is left to git.
func needsRepo(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for i := 0; i < len(args); i++ {
		switch {
		case repoFlags[args[i]]:
		case args[i] == "--git-path" && i+1 < len(args):
			i++
		default:
			return false
		}
	}
	return true
}
