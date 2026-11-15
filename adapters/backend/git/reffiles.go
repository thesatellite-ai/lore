package git

// reffiles.go — resolve a ref from git's own files, without a git process
// (devco T-3: a store Get resolved HEAD with `git rev-parse` per row, ~20
// processes per imported task).
//
// A loose ref is a file holding the object id; a packed one is a line of
// packed-refs; HEAD is "ref: <name>" or an id. Anything else (a reftable
// repository, a linked worktree, a ref name with odd characters) answers
// ok=false and the caller asks git, as before. A loose ref git is writing
// is replaced by rename, so a read sees the old id or the new one whole.

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// maxSymrefHops bounds HEAD -> branch symbolic ref chains.
const maxSymrefHops = 4

// RefFromFiles resolves ref ("HEAD" or a full refs/… name) in the clone at
// dir from its ref files. ok is false when the files cannot answer for
// sure; oid "" with ok true means the ref does not exist (an unborn branch).
func RefFromFiles(dir, ref string) (oid string, ok bool) {
	gitDir := filepath.Join(dir, ".git")
	if fi, err := os.Stat(gitDir); err != nil || !fi.IsDir() {
		if _, berr := os.Stat(filepath.Join(dir, "HEAD")); berr != nil {
			return "", false // not a plain clone (a linked worktree's .git is a file)
		}
		gitDir = dir // a bare clone
	}
	if _, err := os.Stat(filepath.Join(gitDir, "refs")); err != nil {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(gitDir, "commondir")); err == nil {
		return "", false
	}
	for range maxSymrefHops {
		if ref != "HEAD" && !strings.HasPrefix(ref, "refs/") || strings.Contains(ref, "..") {
			return "", false
		}
		b, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(ref)))
		switch {
		case err == nil:
			s := strings.TrimSpace(string(b))
			if target, sym := strings.CutPrefix(s, "ref: "); sym {
				ref = strings.TrimSpace(target)
				continue
			}
			if isOID(s) {
				return s, true
			}
			return "", false
		case !os.IsNotExist(err):
			return "", false
		}
		if ref == "HEAD" {
			return "", false
		}
		return packedRef(gitDir, ref)
	}
	return "", false
}

// packedRef looks ref up in packed-refs ("" with ok true: not there either).
func packedRef(gitDir, ref string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(gitDir, "packed-refs"))
	if os.IsNotExist(err) {
		return "", true
	}
	if err != nil {
		return "", false
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		id, name, found := strings.Cut(line, " ")
		if found && name == ref {
			if !isOID(id) {
				return "", false
			}
			return id, true
		}
	}
	if sc.Err() != nil {
		return "", false
	}
	return "", true
}

// isOID reports whether s is a full sha1 or sha256 object id.
func isOID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}
