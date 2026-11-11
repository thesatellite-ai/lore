package setup

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// SuggestFolders: the start folder, its git root and its parent come
// first ("where you ran lore setup", when StartFolderAllowed), then this identity's includeIf
// folders, then another identity's (dimmed); a folder another setup covers
// names that setup; the home folder and duplicates never show.
func TestSuggestFolders(t *testing.T) {
	// an absolute home on this system (/home/octo-user is not on Windows)
	home := "/home/octo-user"
	if runtime.GOOS == "windows" {
		home = "C:/Users/octo-user"
	}
	includes := []IncludeFolder{
		{Folder: "~/projects/acme/", Email: "work-user@acme.example"},
		{Folder: "~/projects/acme/sub/", Email: "work-user@acme.example"},
		{Folder: "~/personal/", Email: "octo-user@example.com"},
		{Folder: "~/elsewhere/"}, // no email: not this identity
	}
	type row struct {
		glob    string
		kind    FolderKind
		dim     bool
		claimed string
	}
	cases := []struct {
		name string
		f    FolderFacts
		want []row
	}{
		{"start in a repo, own and other identity's includeIf", FolderFacts{Home: home, Start: home + "/personal/lore1/src", GitRoot: home + "/personal/lore1",
			Includes: includes, Setup: "personal", Emails: []string{"Octo-User@example.com"}},
			[]row{
				{"~/personal/lore1/src/**", FolderHere, false, ""},
				{"~/personal/lore1/**", FolderGitRoot, false, ""}, // also the parent: listed once
				{"~/personal/**", FolderInclude, false, ""},
				{"~/projects/acme/**", FolderIncludeOther, true, ""},
				{"~/projects/acme/sub/**", FolderIncludeOther, true, ""},
				{"~/elsewhere/**", FolderIncludeOther, true, ""},
			}},
		{"start outside a repo; parent is home (never offered)", FolderFacts{Home: home, Start: home + "/Downloads", Setup: "personal"},
			[]row{{"~/Downloads/**", FolderHere, false, ""}}},
		{"no identity yet: every includeIf is another identity's", FolderFacts{Home: home, Start: home + "/code/proj", Includes: includes[2:3]},
			[]row{{"~/code/proj/**", FolderHere, false, ""}, {"~/code/**", FolderParent, false, ""}, {"~/personal/**", FolderIncludeOther, true, ""}}},
		{"claimed by another setup, the edited setup itself ignored", FolderFacts{Home: home, Start: home + "/code/proj", Setup: "work",
			Setups: []onboard.SetupEntry{{Name: "personal", Folders: []string{"~/code/**"}}, {Name: "work", Folders: []string{"~/code/proj/**"}}}},
			[]row{{"~/code/proj/**", FolderHere, true, "personal"}, {"~/code/**", FolderParent, true, "personal"}}}, // offered dimmed, labelled; never prefilled (StartFolderAllowed)
		{"own includeIf folder claimed by another setup: dimmed and labelled", FolderFacts{Home: home, Includes: includes[2:3], Emails: []string{"octo-user@example.com"}, Setup: "work",
			Setups: []onboard.SetupEntry{{Name: "personal", Folders: []string{"~/personal/**"}}}},
			[]row{{"~/personal/**", FolderInclude, true, "personal"}}},
		{"started at the disk root", FolderFacts{Home: home, Start: "/"}, nil},
		{"nothing known", FolderFacts{Home: home}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SuggestFolders(c.f)
			if len(got) != len(c.want) {
				t.Fatalf("got %d rows %+v, want %d", len(got), got, len(c.want))
			}
			for i, w := range c.want {
				g := got[i]
				if g.Glob != w.glob || g.Kind != w.kind || g.Dim != w.dim || g.ClaimedBy != w.claimed {
					t.Errorf("row %d = %+v, want %+v", i, g, w)
				}
				if g.Label == "" || (w.claimed != "") != (g.Note != "") || (w.claimed != "") != (g.ClaimedGlob != "") {
					t.Errorf("row %d: label, note and claimed glob do not match the claim: %+v", i, g)
				}
			}
		})
	}
	other := SuggestFolders(FolderFacts{Home: home, Includes: includes[:1]})
	if len(other) != 1 || other[0].Label != "used by your other identity work-user@acme.example" {
		t.Errorf("other-identity label = %+v", other)
	}
}

func TestSetupEmails(t *testing.T) {
	e := onboard.SetupEntry{Identity: onboard.SetupIdentity{Key: "human:octo-user@example.com", Emails: []string{"OCTO-USER@example.com", "alt@example.com"}}}
	got := SetupEmails(e, []string{"typed@example.com", "alt@example.com", " "})
	want := []string{"octo-user@example.com", "alt@example.com", "typed@example.com"}
	if len(got) != len(want) {
		t.Fatalf("emails = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("emails = %v, want %v", got, want)
		}
	}
}

// PrefillFolder: the start folder (or its git repo), never its parent;
// nothing at the home folder or the disk root.
func TestPrefillFolder(t *testing.T) {
	const home = "/home/octo-user"
	for _, c := range []struct{ name, start, root, want string }{
		{"not a repo: the start folder itself", home + "/Downloads/lore1", "", "~/Downloads/lore1/**"},
		{"inside a repo: its git root", home + "/code/proj/src", home + "/code/proj", "~/code/proj/**"},
		{"the home folder itself", home, "", ""},
		{"home with a trailing slash", home + "/", "", ""},
		{"the disk root", "/", "", ""},
		{"unknown", "", "", ""},
		{"outside home", "/srv/work", "", "/srv/work/**"},
	} {
		if got := PrefillFolder(home, c.start, c.root); got != c.want {
			t.Errorf("%s: PrefillFolder = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestStartFolderMatrix: StartFolderAllowed (the prefill rule) and
// SuggestFolders over the start folder's place (under this identity's
// includeIf folder, under another identity's, claimed by another setup,
// claimed by the setup being edited, free) × this identity with and
// without includeIf folders. A start folder is offered when it is not
// another identity's and (when this identity has includeIf folders) under
// one of them; one another setup claims is offered dimmed with that setup
// and its glob, and never allowed as the prefill; this identity's own
// unclaimed includeIf folders are always offered, undimmed.
func TestStartFolderMatrix(t *testing.T) {
	// a home of this system's own shape: on Windows a drive path, where
	// /home/octo-user is not absolute
	home := filepath.FromSlash("/home/octo-user")
	if runtime.GOOS == "windows" {
		home = `C:\Users\octo-user`
	}
	const (
		workMail  = "octo-work@example.org"
		otherMail = "octo-personal@example.com"
	)
	// wantOwn / wantNone: offered when this identity has includeIf
	// folders (~/work/) / when it has none (then ~/work is a free folder).
	// claim / claimGlob: the setup (and its glob) the start row names.
	starts := []struct {
		name, start       string
		setups            []onboard.SetupEntry
		wantOwn, wantNone bool
		claim, claimGlob  string
	}{
		{"under own includeIf", filepath.Join(home, "work/acme/proj"), nil, true, true, "", ""},
		{"under another identity's includeIf", filepath.Join(home, "personal/proj"), nil, false, false, "", ""},
		{"claimed by another setup", filepath.Join(home, "Downloads/lore1"), []onboard.SetupEntry{{Name: "personal", Folders: []string{"~/Downloads/**"}}}, false, true, "personal", "~/Downloads/**"},
		{"claimed, under own includeIf", filepath.Join(home, "work/acme/proj"), []onboard.SetupEntry{{Name: "client", Folders: []string{"~/work/acme/**"}}}, true, true, "client", "~/work/acme/**"},
		{"claimed by the setup being edited (not a conflict)", filepath.Join(home, "Downloads/lore1"), []onboard.SetupEntry{{Name: "work", Folders: []string{"~/Downloads/lore1/**"}}}, false, true, "", ""},
		{"claimed, under another identity's includeIf", filepath.Join(home, "personal/proj"), []onboard.SetupEntry{{Name: "personal", Folders: []string{"~/personal/**"}}}, false, false, "", ""},
		{"free (the realmac ~/Downloads case)", filepath.Join(home, "Downloads/lore1"), nil, false, true, "", ""},
	}
	for _, ownIncludes := range []bool{true, false} {
		for _, st := range starts {
			includes := []IncludeFolder{{Folder: "~/personal/", Email: otherMail}}
			if ownIncludes {
				includes = append(includes, IncludeFolder{Folder: "~/work/", Email: workMail})
			}
			f := FolderFacts{Home: home, Start: st.start, Includes: includes, Setup: "work", Emails: []string{workMail}, Setups: st.setups}
			// the start folder is offered only when its place allows it;
			// it is the prefill only when no other setup claims it too
			offer := st.wantNone
			if ownIncludes {
				offer = st.wantOwn
			}
			want := offer && st.claim == ""
			name := st.name + map[bool]string{true: "/own includeIf", false: "/no own includeIf"}[ownIncludes]
			t.Run(name, func(t *testing.T) {
				if got := StartFolderAllowed(f, st.start); got != want {
					t.Fatalf("StartFolderAllowed (prefill) = %v, want %v", got, want)
				}
				rows := SuggestFolders(f)
				here, own := false, false
				for _, r := range rows {
					switch r.Kind {
					case FolderHere:
						here = true
						if r.ClaimedBy != st.claim || r.ClaimedGlob != st.claimGlob || r.Dim != (st.claim != "") || (r.Note != "") != (st.claim != "") {
							t.Errorf("start row %+v, want claimed by %q with %q (dimmed when claimed)", r, st.claim, st.claimGlob)
						}
					case FolderInclude:
						own = own || (r.Glob == "~/work/**" && !r.Dim)
					case FolderGitRoot, FolderParent:
						if !startFolderFits(f.slashed(), expandSlashed(r.Folder, home)) {
							t.Errorf("row %+v offered against the rule", r)
						}
					case FolderIncludeOther:
						if !r.Dim {
							t.Errorf("another identity's folder not dimmed: %+v", r)
						}
					}
				}
				if here != offer {
					t.Errorf("start folder row offered = %v, want %v: %+v", here, offer, rows)
				}
				if own != ownIncludes {
					t.Errorf("own includeIf ~/work/** offered = %v, want %v: %+v", own, ownIncludes, rows)
				}
			})
		}
	}
	// the parent follows the same rule: started in ~/work/proj with an own
	// includeIf at ~/work/proj/, the parent ~/work is not under it
	f := FolderFacts{Home: home, Start: filepath.Join(home, "work/proj"), Includes: []IncludeFolder{{Folder: "~/work/proj/", Email: workMail}}, Emails: []string{workMail}}
	for _, r := range SuggestFolders(f) {
		if r.Kind == FolderParent {
			t.Errorf("parent outside the own includeIf folder offered: %+v", r)
		}
	}
}
