package stores

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestPinnedBranch (INTEG-SYNC-5): every url spelling git accepts × every
// way of naming the branch gives the one answer store list and store open
// share: the explicit/entry branch, else the url's ?branch=, else "" (the
// remote default); a doubled or empty ?branch= is refused unless the
// explicit branch already decides.
func TestPinnedBranch(t *testing.T) {
	spellings := []string{
		"https://github.com/acme/notes",
		"https://github.com/acme/notes.git",
		"ssh://git@github.com/acme/notes",
		"git@github.com:acme/notes.git",
		"file:///s/notes",
	}
	cases := []struct {
		name, query, branch, want string
		bad                       bool
	}{
		{name: "nothing: the remote default", want: ""},
		{name: "entry branch", branch: "staging", want: "staging"},
		{name: "url branch", query: "?branch=staging", want: "staging"},
		{name: "url branch with a folder", query: "?folder=golang&branch=staging", want: "staging"},
		{name: "folder only", query: "?folder=golang", want: ""},
		{name: "entry branch wins the url's", query: "?branch=staging", branch: "qa", want: "qa"},
		{name: "slashed branch", query: "?branch=release%2F1.2", want: "release/1.2"},
		{name: "two url branches", query: "?branch=a&branch=b", bad: true},
		{name: "empty url branch", query: "?branch=", bad: true},
		{name: "entry branch decides a doubled query", query: "?branch=a&branch=b", branch: "qa", want: "qa"},
		{name: "broken query", query: "?branch=%zz", bad: true},
	}
	for _, sp := range spellings {
		for _, c := range cases {
			got, err := PinnedBranch(sp+c.query, c.branch)
			if c.bad {
				if errs.CodeOf(err) != errs.InvalidInput || !strings.Contains(err.Error(), sp) {
					t.Errorf("%s / %s: got %q, %v; want E_INVALID_INPUT naming the url", sp, c.name, got, err)
				}
				continue
			}
			if err != nil || got != c.want {
				t.Errorf("%s / %s: got %q, %v; want %q", sp, c.name, got, err, c.want)
			}
		}
	}
}

// branchFake is fakeEnv plus DefaultBrancher, recording the branch each
// status read saw.
type branchFake struct {
	fakeEnv
	def  map[string]string // url → the local clone's branch
	seen map[string]string // name → Entry.Branch at the status read
}

func (b *branchFake) StoreStatus(_ context.Context, e Entry) Status {
	b.seen[e.Name] = e.Branch
	return Status{Visibility: VisibilityInternal, Reach: ReachLocalCopy}
}

func (b *branchFake) CloneDefaultBranch(_ context.Context, e Entry) string { return b.def[e.URL] }

// TestListBranchColumn: store.list fills the branch (the BRANCH column the
// CLI renders from it; tests/userscen/sync_store_branches_test.go pins the
// text) for
// every spelling: the entry's branch:, the url's ?branch=, and an unpinned
// store's local clone branch (the remote default, by name, not blank); a
// store with no local clone stays empty. The status read sees the pinned
// branch (so a ?branch= store is looked up in its own clone) and never
// the default one (there is no <repo>@main clone).
func TestListBranchColumn(t *testing.T) {
	const u = "https://github.com/acme/notes"
	f := &branchFake{seen: map[string]string{},
		def: map[string]string{u: "trunk"},
		fakeEnv: fakeEnv{stores: []Entry{
			{Name: "notes", URL: u, Source: SourceStoresFile},
			{Name: "stag", URL: u + "?branch=staging", Source: SourceStoresFile},
			{Name: "stag2", URL: u, Branch: "staging", Source: SourceStoresFile},
			{Name: "fresh", URL: "https://github.com/acme/never_cloned", Source: SourceStoresFile},
		}}}
	raw, _, err := invoke(t, f, OpList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var out ListOut
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"notes": "trunk", "stag": "staging", "stag2": "staging", "fresh": ""}
	seenWant := map[string]string{"notes": "", "stag": "staging", "stag2": "staging", "fresh": ""}
	if len(out.Stores) != len(want) {
		t.Fatalf("got %d stores: %s", len(out.Stores), raw)
	}
	for _, e := range out.Stores {
		if e.Branch != want[e.Name] {
			t.Errorf("--json %s: branch %q, want %q", e.Name, e.Branch, want[e.Name])
		}
		if f.seen[e.Name] != seenWant[e.Name] {
			t.Errorf("status read of %s saw branch %q, want %q", e.Name, f.seen[e.Name], seenWant[e.Name])
		}
	}
}
