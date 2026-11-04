package stores

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"saas/pkg/aicoder/repourl"
)

// Folder names under ~/.lore (§1 "Files and folders", §5.3). Named once so
// doctor, forget, prune and the tests agree on where things are.
const (
	// ClonesDir holds this package's bare clone per store URL. It is NOT
	// the 0.1.18 core's folder (adapters/backend/git ClonesDir = "clones"):
	// the core checks the store branch out in <clone>/work, and this
	// package moves refs/heads/<branch> of its bare clone directly, which
	// git refuses for a branch checked out in a worktree ("refusing to move
	// a checked-out branch"). So the two never share a clone; both push to
	// the same remote and reconcile through it (this package replays on a
	// rejected push, the core merges on sync). A v0.1.17 clone left at
	// ~/.lore/clones/<host>/<owner>/<repo>.git is adopted by the core,
	// unpushed branch commits included (its worktree starts on the existing
	// local branch).
	ClonesDir = "clones-v017"
	// WorktreesDir holds optional browsing worktrees (`lore store open`).
	WorktreesDir = "worktrees"
	// CacheDir holds per-project caches, link indexes and store project
	// mirrors — everything derived, safe to delete.
	CacheDir = "cache"
	// RenderDir holds the internal render per code project (F20, D9).
	RenderDir = "render"
	// bareSuffix marks a bare clone directory, like git's own convention.
	bareSuffix = ".git"
	// stateDir is lore's per-clone machine state inside a bare clone
	// (§1: "bare clone: <clone>/lore/"): fetch stamp, check cache, lock.
	stateDir = "lore"
)

// ErrBadStoreURL is returned for a URL that cannot name a hosted store.
var ErrBadStoreURL = errors.New("stores: not a hosted store url")

// Layout maps store URLs to folders under one lore home (~/.lore). It is a
// value: no hidden state, the home is always passed in.
type Layout struct {
	// Home is the lore home, normally ~/.lore.
	Home string
}

// CloneDir returns the bare clone folder for a store URL:
// <home>/clones/<host>/<owner>/<repo>.git. Every form of one URL (ssh,
// https, with or without .git) maps to the same folder, so entries sharing a
// store share one clone (§5.3).
func (l Layout) CloneDir(url string) (string, error) {
	n, err := normalizeStoreURL(url)
	if err != nil {
		return "", err
	}
	return filepath.Join(l.Home, ClonesDir, filepath.FromSlash(n)+bareSuffix), nil
}

// WorktreeDir returns the optional browsing worktree for one store branch:
// <home>/worktrees/<host>/<owner>/<repo>/<branch>.
func (l Layout) WorktreeDir(url, branch string) (string, error) {
	n, err := normalizeStoreURL(url)
	if err != nil {
		return "", err
	}
	return filepath.Join(l.Home, WorktreesDir, filepath.FromSlash(n), branch), nil
}

// MirrorDir returns the folder that mirrors one store project for the
// existing row engine (its lore.db and .lore/data):
// <home>/cache/stores/<host>/<owner>/<repo>/<branch>/<folder>. It is a
// derived copy, always outside any code checkout (TEAM-019), rebuilt from
// the clone whenever the store commit changes.
func (l Layout) MirrorDir(url, branch, folder string) (string, error) {
	n, err := normalizeStoreURL(url)
	if err != nil {
		return "", err
	}
	f := folder
	if f == RootFolder {
		f = mirrorRootName
	}
	return filepath.Join(l.Home, CacheDir, mirrorsDir, filepath.FromSlash(n), branch, f), nil
}

// mirrorsDir is the cache sub-folder for store project mirrors.
const mirrorsDir = "stores"

// mirrorRootName names the mirror of a store's root project ("." as a
// folder name would collapse into its parent path).
const mirrorRootName = "_root"

// CacheFile returns <home>/cache/<name>.
func (l Layout) CacheFile(name string) string { return filepath.Join(l.Home, CacheDir, name) }

// normalizeStoreURL reduces a store URL to host/owner/repo and refuses
// anything that is not a hosted repo or that would escape the clones folder.
func normalizeStoreURL(url string) (string, error) {
	n, err := repourl.Normalize(url)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %w", ErrBadStoreURL, url, err)
	}
	for _, seg := range strings.Split(n, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("%w: %q", ErrBadStoreURL, url)
		}
	}
	return n, nil
}

// ErrBadBranch: a store branch name lore refuses.
var ErrBadBranch = errors.New("stores: bad store branch name")

// ValidateBranch applies fixed rule 3 (a store branch may not contain "/",
// so refs/lore/live/<branch> never clashes) and every rule of
// `git check-ref-format --branch` that can apply to one path segment, so a
// name lore accepts is always a branch git will create:
//
//   - not empty, not starting with "-" or ".", not ending with "." or ".lock";
//   - no "..", no "@{", no ASCII control character or DEL;
//   - none of space ~ ^ : ? * [ \ (and "/", rule 3);
//   - not "HEAD" (git refuses it as a branch name).
//
// "@" alone is refused too although git allows refs/heads/@: git reads a
// bare "@" as HEAD, so `git push origin @` would push the wrong thing.
func ValidateBranch(b string) error {
	bad := func() error {
		return fmt.Errorf("%w: %q (one path segment; no \"/\", spaces, control characters or git specials)", ErrBadBranch, b)
	}
	switch {
	case b == "", b == branchHEAD, b == branchAt,
		strings.HasPrefix(b, "-"), strings.HasPrefix(b, "."),
		strings.HasSuffix(b, "."), strings.HasSuffix(b, lockSuffix),
		strings.Contains(b, ".."), strings.Contains(b, "@{"),
		strings.ContainsAny(b, "/ \t~^:?*[\\"):
		return bad()
	}
	for _, r := range b {
		if r < asciiSpace || r == asciiDEL {
			return bad()
		}
	}
	return nil
}

// Names and bytes git's ref-name rules single out (see ValidateBranch).
const (
	branchHEAD = "HEAD"  // git refuses HEAD as a branch name
	branchAt   = "@"     // git's shorthand for HEAD
	lockSuffix = ".lock" // git's lock files; a ref may not end with it
	asciiSpace = 0x20    // bytes below this are control characters
	asciiDEL   = 0x7f    // DEL is refused like a control character
)
