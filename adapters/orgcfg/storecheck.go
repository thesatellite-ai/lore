package orgcfg

// storecheck.go — the F12 check of an org store branch read for links
// (LORE_TEAM_SPEC E10, F12), ported from v0.1.17's stores.CheckTree: a
// repo listed under stores: that holds anything but lore files (a code
// repo) is refused with E_NOT_A_STORE, never read as a store.

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Files a lore store may hold (v0.1.17 stores.Allowed).
const (
	allowedGitattributes = ".gitattributes"
	allowedReadmePrefix  = "README"
	allowedLicensePrefix = "LICENSE"
	allowedWorkflowDir   = ".github/workflows/"
	allowedWorkflowName  = "lore-"
	allowedWorkflowExt   = ".yml"
	storeLoreDir         = ".lore"
	// maxListedOffenders caps the files the refusal names.
	maxListedOffenders = 5
	// hintStoreInit is the refusal's hint (v0.1.17 storeHintInit).
	hintStoreInit = "point stores: at a repo made with `lore store init <name> <url>`, or remove the entry from ~/.lore/config.yaml"
)

// storeAllowed reports whether one path may exist in a store: the marker,
// README*, LICENSE*, .gitattributes, .github/workflows/lore-*.yml, and
// anything under a project's .lore/ folder (one path segment deep).
// ds:def id=team-store-allowed-a7qb5uuj owner=@khanakia stability=stable desc="files a lore store may hold"
func storeAllowed(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return false
	}
	if p == storeMarkerFile || p == allowedGitattributes {
		return true
	}
	if !strings.Contains(p, "/") {
		return strings.HasPrefix(p, allowedReadmePrefix) || strings.HasPrefix(p, allowedLicensePrefix)
	}
	if rest, ok := strings.CutPrefix(p, allowedWorkflowDir); ok {
		return !strings.Contains(rest, "/") && strings.HasPrefix(rest, allowedWorkflowName) && strings.HasSuffix(rest, allowedWorkflowExt)
	}
	if strings.HasPrefix(p, storeLoreDir+"/") {
		return true
	}
	first, rest, _ := strings.Cut(p, "/")
	return first != "" && first != storeLoreDir && strings.HasPrefix(rest, storeLoreDir+"/")
}

// checkStoreTree refuses a branch tip that has no marker or holds files a
// store may not (E_NOT_A_STORE naming up to maxListedOffenders of them).
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
	if marker && len(bad) == 0 {
		return nil
	}
	var parts []string
	if !marker {
		parts = append(parts, "no "+storeMarkerFile)
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		shown := summariseOffenders(bad)
		more := ""
		if len(bad) > len(shown) {
			more = fmt.Sprintf(" and %d more", len(bad)-len(shown))
		}
		parts = append(parts, "it holds non-lore files such as "+strings.Join(shown, ", ")+more)
	}
	return errs.New(errs.NotAStore, fmt.Sprintf("%s (branch %s) is listed under stores: but is not a lore store (%s)", norm, branch, strings.Join(parts, "; "))).
		WithHint(hintStoreInit)
}

// summariseOffenders shows a top-level folder once ("src/").
func summariseOffenders(bad []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range bad {
		show := p
		if i := strings.IndexByte(p, '/'); i >= 0 {
			show = p[:i+1]
		}
		if seen[show] {
			continue
		}
		seen[show] = true
		out = append(out, show)
		if len(out) == maxListedOffenders {
			break
		}
	}
	return out
}
