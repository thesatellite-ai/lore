package teamharness

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Remote is one bare repository on the fake GitHub.
type Remote struct {
	w    *World
	Slug string // host/owner/repo, e.g. github.com/acme/app (the F10-normalised form)
	Dir  string // the bare repository on disk
}

// Remote creates (or returns the existing) bare repository for slug
// "github.com/<owner>/<repo>", with main as its default branch.
func (w *World) Remote(slug string) *Remote {
	w.T.Helper()
	if !strings.HasPrefix(slug, fakeHost+"/") || strings.Count(slug, "/") != 2 {
		w.T.Fatalf("remote slug must be %s/<owner>/<repo>, got %q", fakeHost, slug)
	}
	dir := filepath.Join(w.hosts, slug+".git")
	if _, err := os.Stat(dir); err != nil {
		mustMkdir(w.T, filepath.Dir(dir))
		w.Git(w.Root, "init", "-q", "--bare", "-b", "main", dir)
	}
	return &Remote{w: w, Slug: slug, Dir: dir}
}

// URL is the https form a user pastes: https://github.com/acme/app.
func (r *Remote) URL() string { return "https://" + r.Slug }

// SSHURL is the ssh form: git@github.com:acme/app.git.
func (r *Remote) SSHURL() string {
	return "git@" + fakeHost + ":" + strings.TrimPrefix(r.Slug, fakeHost+"/") + ".git"
}

// ProtectMain installs a pre-receive hook that refuses every push to
// refs/heads/main with GitHub's GH006 message (F6's protected-main world).
// Admin pushes (EnvAdminPush) still go through, like the merge button.
func (r *Remote) ProtectMain() {
	r.w.T.Helper()
	writeExec(r.w.T, filepath.Join(r.Dir, "hooks", "pre-receive"), protectedMainHook)
}

// RefuseLoreRefs installs a pre-receive hook that refuses refs/lore/*, like
// hosts that accept only heads and tags (side effect 27).
func (r *Remote) RefuseLoreRefs() {
	r.w.T.Helper()
	writeExec(r.w.T, filepath.Join(r.Dir, "hooks", "pre-receive"), `#!/bin/sh
while read old new ref; do
  case "$ref" in refs/lore/*) echo "error: refusing to update $ref" >&2; exit 1;; esac
done
exit 0
`)
}

// Rev returns the commit a ref points at on the remote, or "" when absent.
func (r *Remote) Rev(ref string) string {
	r.w.T.Helper()
	res := r.w.TryGit(r.Dir, "rev-parse", "--verify", "-q", ref+"^{commit}")
	if res.Code != 0 {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// Has reports whether ref exists on the remote.
func (r *Remote) Has(ref string) bool { return r.Rev(ref) != "" }

// Refs lists every ref on the remote under prefix (e.g. "refs/lore/").
func (r *Remote) Refs(prefix string) []string {
	r.w.T.Helper()
	out := r.w.Git(r.Dir, "for-each-ref", "--format=%(refname)", prefix)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// Files lists every file path in ref's tree (empty when the ref is absent).
func (r *Remote) Files(ref string) []string {
	r.w.T.Helper()
	if !r.Has(ref) {
		return nil
	}
	out := r.w.Git(r.Dir, "ls-tree", "-r", "--name-only", ref)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// FilesMatching lists the paths in ref's tree that contain substr.
func (r *Remote) FilesMatching(ref, substr string) []string {
	var out []string
	for _, f := range r.Files(ref) {
		if strings.Contains(f, substr) {
			out = append(out, f)
		}
	}
	return out
}

// Read returns the content of path in ref's tree and whether it exists.
func (r *Remote) Read(ref, path string) (string, bool) {
	r.w.T.Helper()
	res := r.w.TryGit(r.Dir, "show", ref+":"+path)
	if res.Code != 0 {
		return "", false
	}
	return res.Stdout, true
}

// CommitCount counts the commits reachable from ref (0 when absent).
func (r *Remote) CommitCount(ref string) int {
	r.w.T.Helper()
	if !r.Has(ref) {
		return 0
	}
	n, err := strconv.Atoi(r.w.Git(r.Dir, "rev-list", "--count", ref))
	if err != nil {
		r.w.T.Fatal(err)
	}
	return n
}

// Parents counts the parents of the commit ref points at (signals: always 0).
func (r *Remote) Parents(ref string) int {
	r.w.T.Helper()
	out := r.w.Git(r.Dir, "rev-list", "--parents", "-n", "1", ref)
	return len(strings.Fields(out)) - 1
}

// Log returns ref's commit subjects, newest first.
func (r *Remote) Log(ref string) []string {
	r.w.T.Helper()
	if !r.Has(ref) {
		return nil
	}
	out := r.w.Git(r.Dir, "log", "--format=%s", ref)
	return strings.Split(out, "\n")
}

// Authors returns "name <email>" of every commit reachable from ref.
func (r *Remote) Authors(ref string) []string {
	r.w.T.Helper()
	if !r.Has(ref) {
		return nil
	}
	return strings.Split(r.w.Git(r.Dir, "log", "--format=%an <%ae>", ref), "\n")
}

// DeleteRef removes a ref on the remote, like someone running
// `git push origin :refs/lore/live` (side effect 28).
func (r *Remote) DeleteRef(ref string) {
	r.w.T.Helper()
	r.w.Git(r.Dir, "update-ref", "-d", ref)
}

// SetRef points ref at rev on the remote directly (a mirror push or a host
// rewriting history; side effect 28).
func (r *Remote) SetRef(ref, rev string) {
	r.w.T.Helper()
	r.w.Git(r.Dir, "update-ref", ref, rev)
}

// AllObjectsContain reports which of needles appear in any object of the
// repository, compressed or not (git cat-file over every object). This is
// what anyone who clones the repo can read.
func (r *Remote) AllObjectsContain(needles ...string) []string {
	r.w.T.Helper()
	return objectsContain(r.w, r.Dir, needles)
}

// objectsContain dumps every object of the repository at dir and returns the
// needles found in it.
func objectsContain(w *World, dir string, needles []string) []string {
	w.T.Helper()
	var found []string
	for _, n := range needles {
		if len(objectsLocate(w, dir, n)) > 0 {
			found = append(found, n)
		}
	}
	return found
}

// maxObjectsNamed caps how many objects a leak finding names: enough to
// point the owner at the writer, short enough to read in a failure line.
const maxObjectsNamed = 5

// objectsLocate names the objects of the repository at dir that contain
// needle, so a leak finding says where the text lives: "blob <path>" for a
// file (path from any reachable tree), "commit <sha> <subject>" for a commit
// message or author, "tree"/"tag" otherwise; "blob (unreachable)" when no
// ref reaches it.
func objectsLocate(w *World, dir, needle string) []string {
	w.T.Helper()
	if needle == "" {
		return nil
	}
	res := w.TryGit(dir, "cat-file", "--batch-all-objects", "--batch")
	if res.Code != 0 {
		w.T.Fatalf("cat-file in %s: %s", dir, res.Stderr)
	}
	type obj struct{ sha, kind, body string }
	var hits []obj
	rest := res.Stdout
	for rest != "" {
		header, after, ok := strings.Cut(rest, "\n")
		if !ok {
			break
		}
		f := strings.Fields(header)
		if len(f) != 3 {
			break
		}
		size, err := strconv.Atoi(f[2])
		if err != nil || size > len(after) {
			break
		}
		body := after[:size]
		rest = strings.TrimPrefix(after[size:], "\n")
		if strings.Contains(body, needle) {
			hits = append(hits, obj{sha: f[0], kind: f[1], body: body})
		}
	}
	if len(hits) == 0 {
		return nil
	}
	paths := map[string]string{}
	for _, line := range strings.Split(w.TryGit(dir, "rev-list", "--all", "--objects").Stdout, "\n") {
		if sha, path, ok := strings.Cut(line, " "); ok {
			if _, seen := paths[sha]; !seen {
				paths[sha] = path
			}
		}
	}
	var out []string
	for _, h := range hits {
		if len(out) == maxObjectsNamed {
			out = append(out, "…")
			break
		}
		switch h.kind {
		case "blob":
			if p, ok := paths[h.sha]; ok {
				out = append(out, "blob "+p)
			} else {
				out = append(out, "blob (unreachable) "+h.sha[:10])
			}
		case "commit":
			_, msg, _ := strings.Cut(h.body, "\n\n")
			subject, _, _ := strings.Cut(msg, "\n")
			out = append(out, "commit "+h.sha[:10]+" "+subject)
		default:
			out = append(out, h.kind+" "+h.sha[:10])
		}
	}
	return out
}
