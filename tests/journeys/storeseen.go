//go:build journeys && !windows

package journeys

// storeseen.go — real negative checks for "this person never touches that
// store" (devco D-727). Looking for one guessed folder under ~/.lore/stores
// passes vacuously: the clone folder drops the host and follows the url a
// host alias gives, so a clone made under another name is never seen. These
// helpers look at what git itself records instead: every clone under
// ~/.lore/stores whose origin is the store (whatever host or alias the url
// uses), and every git process lore started on the machine (git's trace2
// log), so a clone, fetch or ls-remote of the store is caught even when it
// failed and left nothing on disk.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// storeRepoKey is "<owner>/<repo>" of a remote url in any form a person or
// lore writes it (git@host:o/r.git, git@alias:o/r, https://host/o/r, a local
// path), lower-cased; "" when the url has no owner/repo.
func storeRepoKey(url string) string {
	u := strings.TrimSuffix(strings.TrimSpace(url), "/")
	u = strings.TrimSuffix(u, ".git")
	u = strings.ReplaceAll(u, ":", "/")
	parts := strings.Split(u, "/")
	if len(parts) < 2 {
		return ""
	}
	return strings.ToLower(parts[len(parts)-2] + "/" + parts[len(parts)-1])
}

// ClonesOfStore lists every git clone under m's ~/.lore/stores whose
// remote.origin.url names r's owner/repo, whatever host or alias it uses.
// Why: a guessed path check passes when the clone sits under another name.
func (m *Machine) ClonesOfStore(r *teamharness.Remote) []string {
	want := storeRepoKey(r.SSHURL())
	var found []string
	root := filepath.Join(m.Home, ".lore", "stores")
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		gitDir := filepath.Join(p, ".git")
		if _, serr := os.Stat(gitDir); serr != nil {
			if _, herr := os.Stat(filepath.Join(p, "HEAD")); herr != nil {
				return nil // not a repo here: keep walking
			}
		}
		res := m.w.TryGit(p, "config", "--get", "remote.origin.url")
		if storeRepoKey(res.Stdout) == want {
			found = append(found, p)
		}
		return filepath.SkipDir
	})
	return found
}

// TraceGit turns on git's trace2 log for every git process run under m's
// HOME (trace2 settings are read from the global config), so a later
// GitTouched sees each clone, fetch or ls-remote lore started, even one
// that failed. Call it before the steps it guards.
func (m *Machine) TraceGit(t testing.TB) {
	t.Helper()
	gc := filepath.Join(m.Home, ".gitconfig")
	if r := m.w.TryGit(m.Home, "config", "--file", gc, "trace2.normalTarget", m.gitTracePath()); r.Code != 0 {
		t.Fatalf("trace git on %s: %s", m.Label, r.Stderr)
	}
}

func (m *Machine) gitTracePath() string {
	return filepath.Join(m.Home, ".git-trace2.log")
}

// GitTouched is every traced git line on m (see TraceGit) that names r's
// repo: the clone url, the fetch's upload-pack path, a url rewritten by
// insteadOf. Empty means m's git never reached for the store.
func (m *Machine) GitTouched(t testing.TB, r *teamharness.Remote) []string {
	t.Helper()
	b, err := os.ReadFile(m.gitTracePath())
	if err != nil {
		t.Fatalf("no git trace on %s (TraceGit first): %v", m.Label, err)
	}
	owner, repo, _ := strings.Cut(storeRepoKey(r.SSHURL()), "/")
	var hits []string
	for _, line := range strings.Split(string(b), "\n") {
		l := strings.ToLower(line)
		if strings.Contains(l, "git config ") {
			continue // the harness writing ~/.gitconfig (asDeny, TraceGit), not a read of the store
		}
		if strings.Contains(l, owner) && strings.Contains(l, repo) {
			hits = append(hits, line)
		}
	}
	return hits
}
