package orgcfg

// storecheck.go — the F12 check of an org store branch read for links
// (LORE_TEAM_SPEC E10, F12), ported from v0.1.17's stores.CheckTree: a
// repo listed under stores: that holds anything but lore files (a code
// repo) is refused with E_NOT_A_STORE, never read as a store.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
)

const (
	// storeLoreDir is the old layout's lore folder (<folder>/.lore/).
	storeLoreDir = rowfs.LoreDir
	// maxListedOffenders caps the files the refusal names; the rest are
	// counted ("and 3 more").
	maxListedOffenders = 5
	// hintStoreInit is the refusal's hint (v0.1.17 storeHintInit).
	hintStoreInit = "point stores: at a repo made with `lore store init <name> <url>`, or remove the entry from ~/.lore/config.yaml"
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
	if len(bad) == 0 {
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
		WithHint(hintStoreInit)
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
