package onboard

import (
	"reflect"
	"strings"
	"testing"
)

// TestResolveHere walks the four states and the edges where two facts
// meet: a default setup of the same vs another org, a refused resolution,
// a taken proposal name, a key that is not an email, a repo outside home.
func TestResolveHere(t *testing.T) {
	const home = "/home/pat"
	personal := SetupEntry{Name: "personal", Kind: KindSolo, Default: true, Folders: []string{"~/code/me/**"}}
	work := SetupEntry{Name: "work", Kind: KindMember, Org: "acme", Folders: []string{"~/code/acme/**"}}
	lore := HereFacts{Git: true, Lore: true, Root: home + "/code/acme/app", UserHome: home,
		Org: "acme", OrgSource: "own: github.com/acme/*", Stores: []string{"team_notes"}, StoresSource: ".lore/stores.yaml",
		IdentityName: "Pat Example", IdentityKey: "human:work@example.org", IdentitySource: "org entry (config.yaml)",
		GitEmail: "work@example.org", Remote: "github.com/acme/app"}
	with := func(f HereFacts, edit func(*HereFacts)) HereFacts { edit(&f); return f }

	cases := []struct {
		name     string
		facts    HereFacts
		setups   []SetupEntry
		state    HereState
		setup    string
		proposal HereProposal
		note     string
	}{
		{name: "not a git repo", facts: HereFacts{Root: "/tmp/x"}, setups: []SetupEntry{personal}, state: HereNotGit},
		{name: "git repo without lore", facts: HereFacts{Git: true, Root: home + "/code/plain"}, setups: []SetupEntry{personal}, state: HereNotLore},
		{name: "folder match", facts: with(lore, func(f *HereFacts) { f.ActiveName, f.ActiveWhy, f.ActiveDetail = "work", "folder", "~/code/acme/**" }),
			setups: []SetupEntry{personal, work}, state: HereMatch, setup: "work"},
		{name: "org match", facts: with(lore, func(f *HereFacts) { f.ActiveName, f.ActiveWhy = "work", "org" }),
			setups: []SetupEntry{personal, work}, state: HereMatch, setup: "work"},
		{name: "repo pin", facts: with(lore, func(f *HereFacts) { f.ActiveName, f.ActiveWhy = "personal", "repo" }),
			setups: []SetupEntry{personal, work}, state: HereMatch, setup: "personal"},
		{name: "default of the same (no) org matches", facts: with(lore, func(f *HereFacts) { f.Org, f.OrgSource, f.ActiveName, f.ActiveWhy = "", "", "personal", "default" }),
			setups: []SetupEntry{personal}, state: HereMatch, setup: "personal"},
		{name: "default of another org does not match", facts: with(lore, func(f *HereFacts) { f.ActiveName, f.ActiveWhy = "personal", "default" }),
			setups: []SetupEntry{personal}, state: HereNoMatch, note: "the default setup personal is for no org, this repo is org acme",
			proposal: HereProposal{Kind: KindMember, Name: "acme", Org: "acme", IdentityName: "Pat Example", IdentityEmail: "work@example.org", Folders: []string{"~/code/acme/app/**"}}},
		{name: "no setups at all, no org", facts: with(lore, func(f *HereFacts) { f.Org, f.ActiveWhy = "", "none" }),
			state: HereNoMatch, proposal: HereProposal{Kind: KindSolo, Name: "personal", IdentityName: "Pat Example", IdentityEmail: "work@example.org", Folders: []string{"~/code/acme/app/**"}}},
		{name: "solo name taken: repo folder name", facts: with(lore, func(f *HereFacts) { f.Org, f.ActiveWhy = "", "none" }),
			setups: []SetupEntry{{Name: "personal", Kind: KindSolo, Folders: []string{"~/elsewhere/**"}}}, state: HereNoMatch,
			proposal: HereProposal{Kind: KindSolo, Name: "app", IdentityName: "Pat Example", IdentityEmail: "work@example.org", Folders: []string{"~/code/acme/app/**"}}},
		{name: "org name taken: suffix", facts: with(lore, func(f *HereFacts) { f.ActiveWhy = "none" }),
			setups: []SetupEntry{{Name: "acme", Kind: KindSolo}}, state: HereNoMatch,
			proposal: HereProposal{Kind: KindMember, Name: "acme-2", Org: "acme", IdentityName: "Pat Example", IdentityEmail: "work@example.org", Folders: []string{"~/code/acme/app/**"}}},
		{name: "refused resolution", facts: with(lore, func(f *HereFacts) { f.ActiveWhy, f.ActiveError = "none", "two setups tie" }),
			setups: []SetupEntry{work, {Name: "work2", Kind: KindMember, Org: "acme"}}, state: HereNoMatch, note: "two setups tie",
			proposal: HereProposal{Kind: KindMember, Name: "acme", Org: "acme", IdentityName: "Pat Example", IdentityEmail: "work@example.org", Folders: []string{"~/code/acme/app/**"}}},
		{name: "agent key: email from git, repo outside home", facts: with(lore, func(f *HereFacts) {
			f.Org, f.ActiveWhy, f.IdentityKey, f.IdentityName, f.Root, f.GitEmail = "", "none", "agent:claude-code", "", "/srv/app", "person@example.com"
		}), state: HereNoMatch, proposal: HereProposal{Kind: KindSolo, Name: "personal", IdentityEmail: "person@example.com", Folders: []string{"/srv/app/**"}}},
		{name: "active names a setup that is gone", facts: with(lore, func(f *HereFacts) { f.ActiveName, f.ActiveWhy = "ghost", "folder" }),
			state: HereNoMatch, proposal: HereProposal{Kind: KindMember, Name: "acme", Org: "acme", IdentityName: "Pat Example", IdentityEmail: "work@example.org", Folders: []string{"~/code/acme/app/**"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := ResolveHere(c.facts, c.setups)
			if h.State != c.state || h.Setup != c.setup || h.Note != c.note {
				t.Fatalf("state/setup/note = %s/%q/%q, want %s/%q/%q", h.State, h.Setup, h.Note, c.state, c.setup, c.note)
			}
			if !reflect.DeepEqual(h.Proposal, c.proposal) {
				t.Fatalf("proposal = %+v, want %+v", h.Proposal, c.proposal)
			}
			if h.Stores == nil {
				t.Fatal("Stores must be [] (the page reads a list), not null")
			}
			if (c.state == HereNotGit || c.state == HereNotLore) && (h.Org != "" || h.IdentityName != "") {
				t.Fatalf("facts leaked into %s: %+v", c.state, h)
			}
			if line := HereLine(h); !strings.HasPrefix(line, HereLinePrefix) || strings.Contains(line, "\n") {
				t.Fatalf("HereLine = %q", line)
			}
		})
	}
}

// TestHereLine pins the exact wording of the one CLI line per state.
func TestHereLine(t *testing.T) {
	base := Here{Root: "/r", Org: "acme", Stores: []string{"team_notes"}, IdentityName: "Pat Example", IdentityKey: "human:work@example.org"}
	match := base
	match.State, match.Setup, match.Why, match.Detail = HereMatch, "work", "folder", "~/code/acme/**"
	nomatch := base
	nomatch.State, nomatch.Proposal = HereNoMatch, HereProposal{Kind: KindMember, Name: "acme"}
	none := Here{State: HereNoMatch, Stores: []string{"a", "b"}, IdentityKey: "agent:x", Note: "two setups tie"}
	for _, c := range []struct {
		h    Here
		want string
	}{
		{Here{State: HereNotGit}, "This folder: not a git repo"},
		{Here{State: HereNotLore}, "This folder: a git repo lore is not set up in (lore init sets it up)"},
		{Here{State: HereNotLore, ClientOrg: "acme"}, "This folder: a client repo of acme (lore keeps its notes in acme's store: lore org link --org acme --project <folder>; not lore init, which writes into the client's repository)"}, // I-13
		{match, "This folder: org acme, store team_notes, written as Pat Example <work@example.org> — matches setup work (folder ~/code/acme/**)"},
		{nomatch, "This folder: org acme, store team_notes, written as Pat Example <work@example.org> — no setup matches; lore setup offers a member setup acme from this repo"},
		{none, "This folder: no org, stores a, b, written as agent:x — no setup matches (two setups tie)"},
		{Here{State: HereNoMatch}, "This folder: no org, no attached store, written as an unknown identity — no setup matches"},
	} {
		if got := HereLine(c.h); got != c.want {
			t.Errorf("HereLine = %q\n want %q", got, c.want)
		}
	}
}

// TestHereProposalEntry: solo carries the identity and no org; member
// carries the org and never an identity (3.2).
func TestHereProposalEntry(t *testing.T) {
	solo := HereProposal{Kind: KindSolo, Name: "personal", Org: "x", IdentityName: "Pat Example", IdentityEmail: "person@example.com", Folders: []string{"~/a/**"}}
	if e := solo.Entry(); e.Org != "" || e.Identity.Key != "human:person@example.com" || e.Identity.Name != "Pat Example" || e.Folders[0] != "~/a/**" {
		t.Fatalf("solo entry = %+v", e)
	}
	mem := HereProposal{Kind: KindMember, Name: "acme", Org: "acme", IdentityName: "Pat Example", IdentityEmail: "work@example.org"}
	if e := mem.Entry(); e.Org != "acme" || !e.Identity.IsZero() {
		t.Fatalf("member entry = %+v", e)
	}
	noMail := HereProposal{Kind: KindSolo, Name: "personal"}
	if e := noMail.Entry(); !e.Identity.IsZero() {
		t.Fatalf("solo without email must carry no identity: %+v", e)
	}
}

// TestResolveHereFolderFacts pins what the this_folder step reads in every
// state: the start folder always, the folder rule for a git repo (home as
// ~, absolute outside home), and the pointer files only for a lore repo.
func TestResolveHereFolderFacts(t *testing.T) {
	const home = "/home/pat"
	cases := []struct {
		name     string
		facts    HereFacts
		root     string
		glob     string
		pointers []string
	}{
		{name: "not a git repo", facts: HereFacts{Dir: "/tmp/x", UserHome: home}},
		{name: "plain repo in home", facts: HereFacts{Dir: home + "/w/app/sub", Git: true, Root: home + "/w/app", UserHome: home, Pointers: []string{"AGENTS.md"}},
			root: home + "/w/app", glob: "~/w/app/**"},
		{name: "lore repo outside home", facts: HereFacts{Dir: "/srv/app", Git: true, Lore: true, Root: "/srv/app", UserHome: home, Pointers: []string{"AGENTS.md", "CLAUDE.md"}},
			root: "/srv/app", glob: "/srv/app/**", pointers: []string{"AGENTS.md", "CLAUDE.md"}},
		{name: "lore repo, no pointer", facts: HereFacts{Dir: home + "/w/b", Git: true, Lore: true, Root: home + "/w/b", UserHome: home},
			root: home + "/w/b", glob: "~/w/b/**"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := ResolveHere(c.facts, nil)
			if h.Dir != c.facts.Dir || h.Root != c.root || h.Glob != c.glob {
				t.Fatalf("dir/root/glob = %q/%q/%q, want %q/%q/%q", h.Dir, h.Root, h.Glob, c.facts.Dir, c.root, c.glob)
			}
			want := c.pointers
			if want == nil {
				want = []string{}
			}
			if !reflect.DeepEqual(h.Pointers, want) {
				t.Fatalf("pointers = %#v, want %#v (never null: the page reads a list)", h.Pointers, want)
			}
		})
	}
}
