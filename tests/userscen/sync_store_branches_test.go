//go:build userscen

package userscen

// sync_store_branches_test.go — `lore store list` names the branch of every
// attached store the way `lore store open` checks it out, through the real
// binary (docs/team/use-cases/staging.md, INTEG-SYNC-5). The three
// spellings of a store branch:
//
//   - an unpinned store: the branch its clone follows, the remote's default
//     (main here), by name and not blank
//   - `store init stag '<url>?branch=staging'`: the url's ?branch=
//   - `store add stag2 <url> --branch staging`: the entry's branch: key
//
// Each entry prints once, by its own name, in text and --json (two entries
// on one url and branch both show), and `store open <name>` lands on the
// very branch the list names.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// syncStoreListOut is `lore store list --json`.
type syncStoreListOut struct {
	Stores []struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		Branch string `json:"branch"`
	} `json:"stores"`
	Count int `json:"count"`
}

func TestSyncStores_ListBranchColumn(t *testing.T) {
	st := newSyncTeam(t, 1)
	a := st.clones[0]
	r := st.w.Remote(syncSlugStore)
	a.MustLore("store", "init", "notes", r.URL(), "--yes")
	a.MustLore("store", "init", "stag", r.URL()+"?branch=staging", "--yes")
	attachTwice(t, a, r.URL())

	want := []struct{ name, url, branch string }{
		{"notes", r.URL(), "main"},
		{"stag", r.URL() + "?branch=staging", "staging"},
		{"stag2", r.URL(), "staging"},
	}

	// text: one line per entry, its own name first, its url and branch in
	// their columns (NAME URL FOLDER READ_ONLY BRANCH …; FOLDER is empty)
	text := a.MustLore("store", "list").Stdout
	for _, w := range want {
		var hits []string
		for _, l := range strings.Split(text, "\n") {
			if f := strings.Fields(l); len(f) > 0 && f[0] == w.name {
				hits = append(hits, l)
			}
		}
		if len(hits) != 1 {
			t.Fatalf("store list: %d lines for %s, want 1:\n%s", len(hits), w.name, text)
		}
		f := strings.Fields(hits[0])
		if len(f) < 4 || f[1] != w.url || f[3] != w.branch {
			t.Fatalf("store list: %s line = %q, want url %s and BRANCH %s:\n%s", w.name, hits[0], w.url, w.branch, text)
		}
	}

	// --json: the same entries, the same branches
	got := th.DecodeJSON[syncStoreListOut](t, a.MustLore("store", "list", "--json"))
	if got.Count != len(want) || len(got.Stores) != len(want) {
		t.Fatalf("store list --json: %+v", got)
	}
	for i, w := range want {
		s := got.Stores[i]
		if s.Name != w.name || s.URL != w.url || s.Branch != w.branch {
			t.Fatalf("store list --json [%d] = %+v, want %+v", i, s, w)
		}
		// one resolution for both commands: open lands where list points
		open := th.DecodeJSON[syncOpenOut](t, a.MustLore("store", "open", w.name, "--json"))
		if open.Branch != s.Branch {
			t.Fatalf("store open %s checked out %s, store list names %s", w.name, open.Branch, s.Branch)
		}
	}
}
