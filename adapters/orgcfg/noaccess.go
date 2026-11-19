package orgcfg

// noaccess.go — an org store this person's login cannot reach (devco
// D-726). An org store whose audience is everyone is cloned by every
// member's lore, but the forge may give a member's login no access to it
// (or the store is gone). Its fetch fails with "repository not found",
// "permission denied" or the like, and the link reader read that as
// "offline, never fetched": the whole org entry became unknown, the
// repo's own folder vanished from whoami's reads and every read failed
// closed with "cannot read <org>: <store>".
//
// A refusal is not an outage, so it is told apart with gitfault:
//
//	fetch fails ─▶ gitfault.Classify(stderr)
//	  Unreachable              ─▶ offline, as before (stale copy, max age)
//	  Missing|Denied|Unreadable ─▶ no access: the store is skipped for this
//	                              person (its links read as none), the
//	                              result kept in <clone>/.git/lore/no-access
//	no-access record present  ─▶ skipped without a fetch (no retry storm)
//	                              until the next `lore sync` (GitLinks.Always)
//	                              tries once more; a fetch that works removes it
//
// whoami and doctor name each skipped store once (NoAccessStores).

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/gitfault"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// noAccessFile records, in a store clone's .git/lore, that the last fetch
// was refused: "<kind> <RFC 3339 time>".
const noAccessFile = "no-access"

// NoAccess is one org store this machine's login could not reach at its
// last fetch (devco D-726).
type NoAccess struct {
	// Org is the org entry that lists the store.
	Org string
	// Store is the store as the entry lists it.
	Store orgconf.Store
	// Kind is how the forge refused (Missing, Denied or Unreadable).
	Kind gitfault.Kind
	// Since is when the refusal was recorded.
	Since time.Time
}

// noAccessFault reports whether a failed fetch means the store refused
// this login or is gone (as opposed to no answer at all): only then is
// the store skipped instead of read as offline.
func noAccessFault(err error) (gitfault.Kind, bool) {
	if err == nil {
		return "", false
	}
	switch k := gitfault.Classify(err.Error()); k { // the whole text: ssh puts "Permission denied" before git's "fatal:"
	case gitfault.Missing, gitfault.Denied, gitfault.Unreadable:
		return k, true
	case gitfault.Unreachable:
	}
	return "", false
}

// noAccessPath is the record's file in clone dir.
func noAccessPath(dir string) string {
	return filepath.Join(gitbackend.StateDirOf(dir), noAccessFile)
}

// markNoAccess records a refused fetch of the clone at dir. Best effort:
// without the record the next read only fetches again.
func markNoAccess(dir string, k gitfault.Kind, now time.Time) {
	p := noAccessPath(dir)
	if err := os.MkdirAll(filepath.Dir(p), cloneDirMode); err != nil {
		return
	}
	_ = os.WriteFile(p, []byte(string(k)+" "+now.UTC().Format(time.RFC3339)+"\n"), cloneFileMode)
}

// clearNoAccess removes the record after a fetch that worked.
func clearNoAccess(dir string) {
	if err := os.Remove(noAccessPath(dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return // best effort: a stale record only skips the store until the next sync
	}
}

// noAccessAt reads the record of the clone at dir; ok is false when the
// last fetch was not refused.
func noAccessAt(dir string) (gitfault.Kind, time.Time, bool) {
	b, err := os.ReadFile(filepath.Clean(noAccessPath(dir)))
	if err != nil {
		return "", time.Time{}, false
	}
	kind, at, _ := strings.Cut(strings.TrimSpace(string(b)), " ")
	t, _ := time.Parse(time.RFC3339, at)
	return gitfault.Kind(kind), t, kind != ""
}

// NoAccessStores lists the stores of cfg's org entries (only those only
// names; every entry when only is nil) whose last fetch on this machine
// was refused, in config order. Admins-only stores a member never clones
// are not listed (D-442 says why they are skipped).
func NoAccessStores(home string, cfg *orgconf.Config, only map[string]bool) []NoAccess {
	if cfg == nil || home == "" {
		return nil
	}
	var out []NoAccess
	seen := map[string]bool{}
	for i := range cfg.Orgs {
		o := &cfg.Orgs[i]
		if only != nil && !only[o.Name] {
			continue
		}
		for _, sc := range o.Stores {
			norm, err := repourl.NormalizeStore(sc.URL)
			if err != nil || seen[o.Name+" "+norm] {
				continue
			}
			seen[o.Name+" "+norm] = true
			dir, _ := cloneDir(home, norm)
			if k, at, ok := noAccessAt(dir); ok {
				out = append(out, NoAccess{Org: o.Name, Store: sc, Kind: k, Since: at})
			}
		}
	}
	return out
}

// NoAccessNote is the one quiet line whoami and doctor show for a skipped
// store: what happened, that nothing else is affected, and what to ask.
func NoAccessNote(n NoAccess) string {
	why := "your git login has no access to it, or it is gone"
	if n.Kind == gitfault.Unreadable {
		why = "this user may not read its folder"
	}
	return n.Store.ShortName() + ": no access, skipped (" + repourl.ShownStore(n.Store.URL) + ": " + why +
		"; every other store reads as normal). Ask an admin of " + n.Org + " for read access to it, then run lore sync"
}
