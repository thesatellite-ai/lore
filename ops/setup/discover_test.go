package setup

import (
	"slices"
	"testing"
)

// twoAccounts is the owner's machine in miniature: a personal login
// (active, emails readable) and a work login whose token lacks
// user:email, a global identity, an includeIf work identity, aliases
// (one per account, one broken) and keys tested alone.
func twoAccounts() (DiscoverFacts, AliasesOut) {
	f := DiscoverFacts{
		GH: []GHAccountFacts{
			{Login: "octo-personal", Active: true, ID: 7, Name: "Pat Example", Status: "ok", EmailScope: EmailScopeOK,
				Emails: []GHEmail{{Email: "person@example.com", Primary: true, Verified: true}, {Email: "alt@example.com", Verified: true}},
				Repos:  []GHRepo{{FullName: "octo-personal/lore-me", SSHURL: "git@github.com:octo-personal/lore-me.git", Private: true}}},
			{Login: "octo-work", ID: 9, Status: "ok", EmailScope: EmailScopeMissing},
		},
		GitGlobal: GitIdentityFact{Name: "Pat Example", Email: "person@example.com"},
		GitIncludes: []GitIncludeFact{{Condition: "gitdir:~/work/acme/", Folder: "~/work/acme/",
			Path: "~/work/acme/.gitconfig-acme", Name: "octo-work", Email: "work@example.org"}},
		KnownStores: []KnownStore{{URL: "git@github.com:acme/hub.git", Kind: SrcLoreOrg, Detail: "acme"}},
		SetupNames:  []string{"personal"},
	}
	t := AliasesOut{
		Aliases: []GitHubAlias{
			{Alias: "github.com", PubKey: "~/.ssh/id_personal.pub", LogsInAs: "octo-personal", Status: "ok"},
			{Alias: "github-work", PubKey: "~/.ssh/id_work.pub", LogsInAs: "octo-work", Status: "ok"},
			{Alias: "github-old", IdentityFile: "/Users/x/.ssh/gone", KeyMissing: true, Status: "failed"},
		},
		Keys: []GitHubKey{{Path: "~/.ssh/spare", LogsInAs: "octo-personal", Status: "ok"}, {Path: "~/.ssh/nobody", Status: "auth_denied"}},
	}
	return f, t
}

func TestMergeDiscoveryCards(t *testing.T) {
	out := MergeDiscovery(twoAccounts())
	if len(out.Accounts) != 2 {
		t.Fatalf("accounts = %d", len(out.Accounts))
	}
	k, w := out.Accounts[0], out.Accounts[1]
	// Personal: API primary email first, the other GitHub email kept; the
	// alias that logs in as it; "personal" is taken by an existing setup.
	if k.Email != "person@example.com" || !slices.Contains(k.Emails, "alt@example.com") || k.Alias != "github.com" ||
		k.Key != "~/.ssh/id_personal.pub" || k.SetupName != "personal-2" || k.RefreshCommand != "" {
		t.Errorf("octo-personal card = %+v", k)
	}
	// Work: no API emails (scope missing) → the includeIf email linked by
	// name ("work" ↔ "octo-work"), its folder, the setup named after
	// the folder, a refresh line that switches account and back.
	if w.Email != "work@example.org" || w.Alias != "github-work" || len(w.Folders) != 1 || w.Folders[0].Folder != "~/work/acme/" ||
		w.SetupName != "acme" || w.EmailScope != EmailScopeMissing || w.Name != "octo-work" ||
		w.RefreshCommand != "gh auth switch -h github.com -u octo-work && gh auth refresh -h github.com -s user:email && gh auth switch -h github.com -u octo-personal" {
		t.Errorf("work card = %+v", w)
	}
	if w.NoreplyEmail != "9+octo-work@users.noreply.github.com" {
		t.Errorf("noreply = %q", w.NoreplyEmail)
	}
}

func TestMergeDiscoveryFields(t *testing.T) {
	out := MergeDiscovery(twoAccounts())
	top := func(cs []Candidate) string {
		if len(cs) == 0 {
			return ""
		}
		return cs[0].Value
	}
	cases := []struct {
		field string
		cs    []Candidate
		want  string
		n     int
	}{
		{"email", out.Fields.Email, "person@example.com", 5}, // primary+global, other, include, 2 noreply
		{"username", out.Fields.Username, "octo-personal", 2},
		{"handle", out.Fields.Handle, "octo-personal", 2},
		{"alias", out.Fields.Alias, "github.com", 3},
		{"key", out.Fields.Key, "~/.ssh/id_personal.pub", 4},
		{"store_url", out.Fields.StoreURL, "git@github.com:acme/hub.git", 2},
		{"folder", out.Fields.Folder, "~/work/acme/", 1},
		{"name", out.Fields.Name, "Pat Example", 2},
	}
	for _, c := range cases {
		if top(c.cs) != c.want || len(c.cs) < c.n {
			t.Errorf("%s: top %q (want %q), %d candidates (want ≥ %d): %+v", c.field, top(c.cs), c.want, len(c.cs), c.n, c.cs)
		}
	}
	// The global email has two sources and belongs to octo-personal.
	e := out.Fields.Email[0]
	if len(e.Sources) < 2 || !slices.Contains(e.Accounts, "octo-personal") {
		t.Errorf("top email = %+v", e)
	}
	// The broken alias sorts last.
	if a := out.Fields.Alias[len(out.Fields.Alias)-1]; a.Value != "github-old" || a.Score != scoreBroken {
		t.Errorf("last alias = %+v", a)
	}
}

// Empty facts: no cards, every field an empty (not nil) list.
func TestMergeDiscoveryEmpty(t *testing.T) {
	out := MergeDiscovery(DiscoverFacts{}, AliasesOut{})
	if len(out.Accounts) != 0 || out.Fields.Email == nil || out.Fields.StoreURL == nil {
		t.Errorf("%+v", out)
	}
}

// StoreLikeRepo: a name whose word is a store word ranks above one that
// only contains a store or hint word; anything else is not offered.
func TestStoreLikeRepo(t *testing.T) {
	cases := []struct {
		name  string
		score int
		ok    bool
	}{
		{"octo-personal/lore", scoreFair, true},
		{"octo-personal/notes", scoreFair, true},
		{"store", scoreFair, true},
		{"acme/team-lore", scoreFair, true},
		{"acme/lore_notes.git", scoreFair, true},
		{"acme/Lore-Store", scoreFair, true},
		{"acme/lorekit", scoreWeak, true},
		{"acme/team-memory", scoreWeak, true},
		{"acme/second-brain", scoreWeak, true},
		{"acme/restore-tool", scoreWeak, true},
		{"acme/site", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		if sc, ok := StoreLikeRepo(c.name); sc != c.score || ok != c.ok {
			t.Errorf("StoreLikeRepo(%q) = %d, %v; want %d, %v", c.name, sc, ok, c.score, c.ok)
		}
	}
}

// StoreKey: every spelling of one GitHub repo is one key, whatever the
// host or ssh alias; local folders and bare names have none.
func TestStoreKey(t *testing.T) {
	same := []string{
		"git@github.com:acme/hub.git", "git@github-work:acme/hub.git", "github-work:acme/hub",
		"ssh://git@github.com/acme/hub.git", "https://github.com/Acme/hub.git", "https://github.com/acme/hub/",
	}
	for _, u := range same {
		if k := StoreKey(u); k != "acme/hub" {
			t.Errorf("StoreKey(%q) = %q", u, k)
		}
	}
	for _, u := range []string{"", "/home/pat/store", "file:///home/pat/store", "hub", "git@github.com:hub.git", "https://github.com"} {
		if k := StoreKey(u); k != "" {
			t.Errorf("StoreKey(%q) = %q, want none", u, k)
		}
	}
}

// Store url candidates: a store lore uses (any source) keeps its own
// spelling and absorbs the GitHub repo that is the same owner/repo; then
// store-word repos above store-ish ones; non-store repos are dropped.
func TestMergeDiscoveryStoreURLs(t *testing.T) {
	f := DiscoverFacts{
		GH: []GHAccountFacts{{Login: "octo-work", Active: true, Status: "ok", Repos: []GHRepo{
			{FullName: "acme/team-memory", SSHURL: "git@github.com:acme/team-memory.git"},
			{FullName: "acme/lore-hub", SSHURL: "git@github.com:acme/lore-hub.git", Private: true},
			{FullName: "octo-work/notes", SSHURL: "git@github.com:octo-work/notes.git"},
			{FullName: "acme/site", SSHURL: "git@github.com:acme/site.git"},
		}}},
		KnownStores: []KnownStore{
			{URL: "git@github-work:acme/lore-hub.git", Kind: SrcLoreRepo, Detail: "~/work/acme/app (team)"},
			{URL: "git@github-work:acme/lore-hub.git", Kind: SrcLoreOrg, Detail: "acme"},
		},
	}
	got := MergeDiscovery(f, AliasesOut{}).Fields.StoreURL
	var vals []string
	for _, c := range got {
		vals = append(vals, c.Value)
	}
	want := []string{"git@github-work:acme/lore-hub.git", "git@github.com:octo-work/notes.git", "git@github.com:acme/team-memory.git"}
	if !slices.Equal(vals, want) {
		t.Fatalf("store urls = %v, want %v", vals, want)
	}
	hub := got[0]
	kinds := []SourceKind{}
	for _, s := range hub.Sources {
		kinds = append(kinds, s.Kind)
	}
	if !slices.Equal(kinds, []SourceKind{SrcLoreRepo, SrcLoreOrg, SrcGitHubRepo}) || !slices.Contains(hub.Accounts, "octo-work") {
		t.Errorf("hub = %+v", hub)
	}
}

// A card whose login an existing setup already works as says which one
// (used_by), so Home does not suggest a second setup for it.
func TestMergeDiscoveryUsedBy(t *testing.T) {
	f := DiscoverFacts{GH: []GHAccountFacts{{Login: "octo-user", Status: "ok"}, {Login: "work-user", Status: "ok"}},
		SetupNames: []string{"personal"}, SetupUsers: map[string]string{"octo-user": "personal"}}
	out := MergeDiscovery(f, AliasesOut{})
	got := map[string]string{}
	for _, a := range out.Accounts {
		got[a.Login] = a.UsedBy
	}
	if got["octo-user"] != "personal" || got["work-user"] != "" {
		t.Fatalf("used_by = %v", got)
	}
}

// A store of a repo lore has not initialised (repo_scan) is offered with
// its own label, ranks below a store lore already uses and above a
// GitHub repo guessed by name, and merges into the same owner/repo.
func TestMergeDiscoveryRepoScan(t *testing.T) {
	f := DiscoverFacts{
		GH: []GHAccountFacts{{Login: "octo-work", Status: "ok", Repos: []GHRepo{
			{FullName: "acme/notes", SSHURL: "git@github.com:acme/notes.git"},
		}}},
		KnownStores: []KnownStore{
			{URL: "git@github.com:acme/team-lore.git", Kind: SrcRepoScan, Detail: "~/work/acme/app (team)"},
			{URL: "git@github.com:acme/notes.git", Kind: SrcRepoScan, Detail: "~/work/acme/web (notes)"},
			{URL: "git@github.com:acme/hub.git", Kind: SrcLoreRepo, Detail: "~/work/acme/api (hub)"},
		},
	}
	got := MergeDiscovery(f, AliasesOut{}).Fields.StoreURL
	var vals []string
	for _, c := range got {
		vals = append(vals, c.Value)
	}
	want := []string{"git@github.com:acme/hub.git", "git@github.com:acme/notes.git", "git@github.com:acme/team-lore.git"}
	if !slices.Equal(vals, want) {
		t.Fatalf("store urls = %v, want %v", vals, want)
	}
	scan := got[2]
	if scan.Score != scoreGood || len(scan.Sources) != 1 || scan.Sources[0].Kind != SrcRepoScan ||
		scan.Sources[0].Label != "stores.yaml of ~/work/acme/app (team), lore not run there yet" {
		t.Errorf("repo_scan candidate = %+v", scan)
	}
	if notes := got[1]; len(notes.Sources) != 2 || notes.Sources[0].Kind != SrcRepoScan || notes.Sources[1].Kind != SrcGitHubRepo {
		t.Errorf("notes = %+v", notes)
	}
}
