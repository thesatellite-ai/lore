package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// RepoStores reads each repo's .lore/stores.yaml as doctor --all checks it:
// every spelling of one remote shares a key, the branch comes from the
// `branch:` key or ?branch=, the probe url keeps no query,
// the org's roster store is marked with the org, a repo with
// no file lists nothing and one whose file does not parse is a warning.
func TestRepoStoresForDoctor(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeT(t, filepath.Join(home, ConfigFile), "orgs:\n  - name: agency\n    stores:\n      - url: git@github.com:agency/roster.git\n")
	root := t.TempDir()
	repo := func(name, yaml string) string {
		r := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(r, ".lore"), 0o700); err != nil {
			t.Fatal(err)
		}
		if yaml != "" {
			writeT(t, filepath.Join(r, ".lore", "stores.yaml"), yaml)
		}
		return r
	}
	alpha := repo("alpha", "stores:\n  - name: team_notes\n    url: git@github.com:acme/notes.git\n  - name: roster\n    url: https://github.com/agency/roster\n    read_only: true\n")
	beta := repo("beta", "stores:\n  - name: notes\n    url: https://github.com/acme/notes/\n  - name: staging\n    url: ssh://git@github.com/acme/notes.git?branch=staging\n  - name: pinned\n    url: https://git.acme.example/acme/notes.git\n    branch: release\n")
	gamma := repo("gamma", "")
	bad := repo("bad", "stores:\n  - name: Bad Name\n    url: x\n")

	got, warnings := cacheApp(home, time.Now).RepoStores(context.Background(), []string{alpha, beta, gamma, bad})
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], bad+": ") {
		t.Fatalf("warnings %v, want one for %s", warnings, bad)
	}
	byName := map[string]int{}
	for i, s := range got {
		byName[s.Repo+"|"+s.Name] = i
		if strings.Contains(s.URL, "?") {
			t.Errorf("%s: probe url %q keeps a query", s.Name, s.URL)
		}
	}
	if len(got) != 5 {
		t.Fatalf("%d stores, want 5: %+v", len(got), got)
	}
	at := func(repo, name string) (int, bool) { i, ok := byName[repo+"|"+name]; return i, ok }
	ia, _ := at(alpha, "team_notes")
	ib, _ := at(beta, "notes")
	is, _ := at(beta, "staging")
	ip, _ := at(beta, "pinned")
	ir, _ := at(alpha, "roster")
	if got[ia].Key == "" || got[ia].Key != got[ib].Key || got[ia].Key != got[is].Key {
		t.Errorf("one remote, three spellings: keys %q %q %q", got[ia].Key, got[ib].Key, got[is].Key)
	}
	if got[ia].Branch != "" || got[is].Branch != "staging" || got[ip].Branch != "release" {
		t.Errorf("branches %q %q %q", got[ia].Branch, got[is].Branch, got[ip].Branch)
	}
	if got[ir].Org != "agency" || !got[ir].ReadOnly || got[ia].Org != "" {
		t.Errorf("roster %+v, team_notes org %q", got[ir], got[ia].Org)
	}
}
