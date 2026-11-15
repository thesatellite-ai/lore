package orgcfg

// storecheck.go — the F12 check of an org store branch read for links
// (LORE_TEAM_SPEC E10, F12), ported from v0.1.17's stores.CheckTree: a
// repo listed under stores: that holds anything but lore files (a code
// repo) is refused with E_NOT_A_STORE, never read as a store.

import (
	"context"
	"fmt"
	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
)

const (
	// storeLoreDir is the old layout's lore folder (<folder>/.lore/).
	storeLoreDir = rowfs.LoreDir
	// maxListedOffenders caps the files the refusal names; the rest are
	// counted ("and 3 more").
	maxListedOffenders = 5
	// hintStoreInit is the refusal's hint (v0.1.17 storeHintInit).
	hintStoreInit = "point stores: at a repo made with `lore store init --name <name> --url <url>`, or remove the entry from <config>"
)

// storeAllowed reports whether one path may exist in a store: the one
// definition, rowfs.StoreFileAllowed.
func storeAllowed(p string) bool { return rowfs.StoreFileAllowed(p) }

// CheckStoreTree is checkStoreTree for callers outside orgcfg (the store
// writers' property test): nil when every file at rev of the clone at dir
// may be in a store, else E_NOT_A_STORE naming the files.
func CheckStoreTree(ctx context.Context, dir, rev, norm, branch string) error {
	return checkStoreTree(ctx, dir, rev, norm, branch)
}

// checkStoreTree refuses a branch tip that holds files a store may not
// (E_NOT_A_STORE naming up to maxListedOffenders of them, and a missing
// marker). A tip of lore files only passes with or without the marker.
func checkStoreTree(ctx context.Context, dir, rev, norm, branch string) error {
	out, err := gitRun(ctx, "-C", dir, "ls-tree", "-r", "-z", "--name-only", rev)
	if err != nil {
		return err
	}
	marker := false
	var bad []string
	for _, p := range strings.Split(out, "\x00") {
		switch {
		case p == "":
		case p == storeMarkerFile:
			marker = true
		case !storeAllowed(p):
			bad = append(bad, p)
		}
	}
	if len(bad) == 0 || marker {
		// devco R-2: a repo with the store marker is a lore store, and
		// files lore does not own in it (a member's plain-git push) never
		// make it "not a store": that locked every teammate out of the
		// org and told them to remove the org's store. The readers skip
		// such files (rowfs reads row paths only) and `lore sync` names
		// them with the commit that added them (StrayFiles).
		//
		// Lore files only. Without the marker it is a store whose first
		// write went out before lore wrote markers on a first write (a
		// folder's _meta.json in a brand-new repo): it is adopted, not
		// refused, and the next store write adds the marker.
		return nil
	}
	var parts []string
	if !marker {
		parts = append(parts, "no "+storeMarkerFile)
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		parts = append(parts, "it holds non-lore files: "+listOffenders(bad))
	}
	return errs.New(errs.NotAStore, fmt.Sprintf("%s (branch %s) is listed under stores: but is not a lore store (%s)", norm, branch, strings.Join(parts, "; "))).
		WithHint(strings.Replace(hintStoreInit, "<config>", lorehome.Shown()+"/config.yaml", 1))
}

// listOffenders names the first maxListedOffenders paths in full (never
// only their folder: "org/" hid which file lore refused, item 32b) and
// counts the rest.
func listOffenders(bad []string) string {
	if len(bad) <= maxListedOffenders {
		return strings.Join(bad, ", ")
	}
	return strings.Join(bad[:maxListedOffenders], ", ") + fmt.Sprintf(" and %d more", len(bad)-maxListedOffenders)
}

// StrayFile is a file in a store that lore does not own (a path no store
// writer makes), with the commit that added it.
type StrayFile struct {
	// Path is the file, slash separated from the store's top.
	Path string
	// Commit is the short id of the commit that added it, Author / Email
	// its git author ("" when git cannot say).
	Commit, Author, Email string
}

// String names the file and who added it.
func (s StrayFile) String() string {
	if s.Commit == "" {
		return s.Path
	}
	return fmt.Sprintf("%s (added in commit %s by %s <%s>)", s.Path, s.Commit, s.Author, s.Email)
}

// StrayFiles lists the files at rev of the clone at dir that a store may
// not hold (storeAllowed); the first maxListedOffenders carry the commit
// that added them (one git call each). A repo without the store marker is not asked
// (checkStoreTree refuses it).
func StrayFiles(ctx context.Context, dir, rev string) []StrayFile {
	out, err := gitRun(ctx, "-C", dir, "ls-tree", "-r", "-z", "--name-only", rev)
	if err != nil {
		return nil
	}
	var strays []StrayFile
	for _, p := range strings.Split(out, "\x00") {
		if p == "" || p == storeMarkerFile || storeAllowed(p) {
			continue
		}
		s := StrayFile{Path: p}
		if len(strays) < maxListedOffenders {
			if log, lerr := gitRun(ctx, "-C", dir, "log", "-1", "--diff-filter=A", "--format=%h%x1f%an%x1f%ae", rev, "--", p); lerr == nil {
				if f := strings.Split(strings.TrimSpace(log), "\x1f"); len(f) == 3 {
					s.Commit, s.Author, s.Email = f[0], f[1], f[2]
				}
			}
		}
		strays = append(strays, s)
	}
	return strays
}

// LocalStrays is StrayFiles of the store url's clone on this machine at
// branch ("" = its default branch), with the clone's folder; nothing when
// the store has no local copy.
func (g *GitLinks) LocalStrays(ctx context.Context, url, branch string) ([]StrayFile, string) {
	norm, err := repourl.NormalizeStore(url)
	if err != nil {
		return nil, ""
	}
	dir := g.cloneDirOf(norm)
	if lastFetch(dir).IsZero() {
		return nil, ""
	}
	if branch == "" {
		branch = g.DefaultBranch(ctx, norm)
	}
	tip, ok := branchTip(ctx, dir, branch)
	if !ok {
		return nil, ""
	}
	if _, merr := gitRun(ctx, "-C", dir, "cat-file", "-e", tip+":"+storeMarkerFile); merr != nil {
		return nil, ""
	}
	return StrayFiles(ctx, dir, tip), dir
}
