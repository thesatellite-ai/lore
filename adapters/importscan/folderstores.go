package importscan

// folderstores.go — the folder walk alone, for setup.discover's Store URL
// suggestions (issue 14): the stores a repo's committed .lore/stores.yaml
// attaches, for repos lore has not initialised on this machine (a
// teammate's repo cloned but never `lore init`ed is in no known_repos row,
// so Scan's machine.db half never sees it). Same walk as Scan's folders:
// depth 4 (at most 8), at most 200 repos, node_modules, vendor, .git and
// hidden folders skipped, every file opened for reading only
// (TestScanWritesNothing covers this path too).

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
)

// MaxStoreFolders bounds how many folders one FolderStores call walks
// (the discover call passes the gitconfig's includeIf folders and the
// setups' folders; a machine with more is cut, never slowed down).
const MaxStoreFolders = 8

// StoreScan is FolderStores' input.
type StoreScan struct {
	// Folders are walked in order (~ expanded with Home); duplicates and
	// folders inside an earlier one are walked once. At most MaxStoreFolders.
	Folders []string
	Home    string
	// Depth and MaxRepos are Scan's limits (0: 4 and 200; depth at most 8).
	Depth    int
	MaxRepos int
	// Known are repo roots already listed elsewhere (machine.db's
	// known_repos): they are walked past, not returned.
	Known []string
}

// StoreScanResult is FolderStores' answer: the repos found with their
// stores (path order), and whether the walk stopped at MaxRepos.
type StoreScanResult struct {
	Repos     []Repo
	Truncated bool
}

// FolderStores walks s.Folders for folders holding .lore/ that are not in
// s.Known and reads each one's .lore/stores.yaml. Read-only; a missing or
// unreadable folder or file adds nothing. Only a cancelled ctx stops it
// early (what was found so far is returned).
func FolderStores(ctx context.Context, s StoreScan) StoreScanResult {
	depth := s.Depth
	if depth <= 0 {
		depth = defaultDepth
	}
	depth = min(depth, maxDepth)
	limit := s.MaxRepos
	if limit <= 0 {
		limit = defaultMaxRepos
	}
	seen := map[string]bool{}
	for _, k := range s.Known {
		seen[filepath.Clean(k)] = true
	}
	var (
		res     StoreScanResult
		scratch Result // walkFolder and readRepoStores record sources here; FolderStores reports none
		walked  []string
	)
	for _, f := range s.Folders {
		if len(walked) >= MaxStoreFolders || ctx.Err() != nil {
			break
		}
		base := filepath.Clean(expandHome(strings.TrimSpace(f), s.Home))
		if strings.TrimSpace(f) == "" || inside(base, walked) {
			continue
		}
		walked = append(walked, base)
		found, truncated := walkFolder(ctx, base, depth, limit-len(res.Repos), seen, &scratch)
		res.Repos = append(res.Repos, found...)
		res.Truncated = res.Truncated || truncated
		if truncated {
			break
		}
	}
	slices.SortStableFunc(res.Repos, func(a, b Repo) int { return strings.Compare(a.Root, b.Root) })
	for i := range res.Repos {
		readRepoStores(&res.Repos[i], &scratch)
	}
	return res
}

// inside reports whether p is one of bases or below one.
func inside(p string, bases []string) bool {
	for _, b := range bases {
		if p == b || strings.HasPrefix(p, strings.TrimSuffix(b, string(filepath.Separator))+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
