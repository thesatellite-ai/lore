package wire_test

// setupfolders_regress_test.go — the Folders step prefills the folder
// `lore setup` started in (or its git repo), never its parent and never
// another identity's includeIf folders.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// Finding a27fd932 (realmac M1): started in ~/Downloads/lore1, the Folders
// step prefilled ~/Downloads/** (the parent of the process's folder, not
// the page's start folder), so the new setup claimed every folder there;
// the owner also saw folders of another identity's includeIf block offered
// for this setup. The prefill is exactly the start folder or its repo.
func TestRegress_FoldersSuggestStartFolderNotOtherIdentity(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	for name, plain := range map[string]bool{"git repo": false, "plain folder": true} {
		t.Run(name, func(t *testing.T) {
			w := newRegressWorld(t, rwShape{GHAccounts: 1, SSH: "default_only", IncludeIf: 2, StartPlain: plain})
			a := rwAllAccounts[0]
			for _, s := range []struct {
				id      string
				answers rwAnswers
			}{
				{"kind", rwAnswers{"kind": str("solo"), "name": str("personal")}},
				{"identity", rwAnswers{"name": str("Pat Example"), "email": str(a.Email)}},
			} {
				if err := w.Apply("personal", s.id, s.answers); err != nil {
					t.Fatalf("step %s: %v", s.id, err)
				}
			}
			st, ok := w.Plan("personal").Step("folders")
			if !ok {
				t.Fatal("no folders step")
			}
			var got []string
			if v, has := st.Prefilled()["folders"]; has {
				if err := json.Unmarshal(v, &got); err != nil {
					t.Fatal(err)
				}
			}
			want := map[string]bool{"~/www/proj/**": true, filepath.Join(w.Start, "**"): true}
			if len(got) != 1 || !want[got[0]] {
				t.Fatalf("folders prefill %q, want exactly the start folder ~/www/proj/**", got)
			}
			for _, g := range got {
				for _, d := range w.IncludeDirs {
					if strings.Contains(g, "/clients/") || strings.HasPrefix(d, strings.TrimSuffix(strings.Replace(g, "~", w.Home, 1), "/**")) {
						t.Errorf("prefill %s covers another identity's includeIf folder %s", g, d)
					}
				}
			}
		})
	}
}

// Owner 2026-11-07: a work setup whose identity has a gitconfig includeIf
// folder elsewhere (~/clients/a/) was offered and prefilled the start
// folder's area. The start folder (~/www/proj) is outside that identity's
// own includeIf folder, so the Folders step neither prefills it nor
// suggests it, its parent or its repo; it suggests ~/clients/a/** (this
// identity's own, undimmed) instead. With the main identity (no includeIf
// folder of its own) the start folder stays the prefill.
func TestRegress_FoldersOwnIncludeIfNotStartFolder(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	for _, c := range []struct {
		name, email string
		wantStart   bool
	}{
		{"identity with its own includeIf folder", "client-a@example.org", false},
		{"identity without includeIf folders", rwAllAccounts[0].Email, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newRegressWorld(t, rwShape{GHAccounts: 1, SSH: "default_only", IncludeIf: 1})
			for _, s := range []struct {
				id      string
				answers rwAnswers
			}{
				{"kind", rwAnswers{"kind": str("solo"), "name": str("work")}},
				{"identity", rwAnswers{"name": str("Pat Example"), "email": str(c.email)}},
			} {
				if err := w.Apply("work", s.id, s.answers); err != nil {
					t.Fatalf("step %s: %v", s.id, err)
				}
			}
			st, ok := w.Plan("work").Step("folders")
			if !ok {
				t.Fatal("no folders step")
			}
			var got []string
			if v, has := st.Prefilled()["folders"]; has {
				if err := json.Unmarshal(v, &got); err != nil {
					t.Fatal(err)
				}
			}
			startGlob := len(got) == 1 && (got[0] == "~/www/proj/**" || got[0] == filepath.Join(w.Start, "**"))
			if startGlob != c.wantStart || (!c.wantStart && len(got) != 0) {
				t.Fatalf("folders prefill %q, want the start folder: %v", got, c.wantStart)
			}
			var out struct {
				Suggestions []struct {
					Glob, Kind string
					Dim        bool
				}
			}
			raw := w.H.JSON(w.H.Must("setup.folders", `{"setup":"work"}`))
			if err := json.Unmarshal([]byte(raw), &out); err != nil || len(out.Suggestions) == 0 {
				t.Fatalf("setup.folders %s: %v", raw, err)
			}
			var startRows int
			own := false
			for _, r := range out.Suggestions {
				switch r.Kind {
				case "here", "git_root", "parent":
					startRows++
				case "include":
					own = own || (r.Glob == "~/clients/a/**" && !r.Dim)
				}
			}
			if (startRows > 0) != c.wantStart || own == c.wantStart {
				t.Fatalf("suggestions %+v: start rows %d (want any: %v), own includeIf offered %v", out.Suggestions, startRows, c.wantStart, own)
			}
		})
	}
}

// Owner 2026-11-07: a start folder another setup already claims is not
// hidden. setup.folders offers it dimmed with the claiming setup and its
// glob as typed fields (claimed_by, claimed_glob) and a conflict note; the
// prefill never picks it. A glob of the setup being edited is no claim.
func TestRegress_FoldersClaimedStartFolderOfferedNotPrefilled(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	for _, c := range []struct {
		name, owner string
		claimed     bool
	}{
		{"claimed by another setup", "personal", true},
		{"claimed by the setup being edited", "work", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newRegressWorld(t, rwShape{GHAccounts: 1, SSH: "default_only"})
			a := rwAllAccounts[0]
			cfg := rwPersonalConfig(a.Login, a.Email)
			if c.owner == "personal" {
				cfg += "    folders: [\"~/www/**\"]\n"
			} else {
				cfg += "  - name: work\n    kind: solo\n    folders: [\"~/www/**\"]\n"
			}
			w.WriteConfig(cfg)
			for _, s := range []struct {
				id      string
				answers rwAnswers
			}{
				{"kind", rwAnswers{"kind": str("solo"), "name": str("work")}},
				{"identity", rwAnswers{"name": str("Pat Example"), "email": str("pat-work@example.org")}},
			} {
				if s.id == "kind" && c.owner == "work" {
					continue // the saved work setup is edited, not started
				}
				if err := w.Apply("work", s.id, s.answers); err != nil {
					t.Fatalf("step %s: %v", s.id, err)
				}
			}
			var out struct {
				Suggestions []struct {
					Glob, Kind, Note string
					ClaimedBy        string `json:"claimed_by"`
					ClaimedGlob      string `json:"claimed_glob"`
					Dim              bool
				}
			}
			raw := w.H.JSON(w.H.Must("setup.folders", `{"setup":"work"}`))
			if err := json.Unmarshal([]byte(raw), &out); err != nil {
				t.Fatalf("setup.folders %s: %v", raw, err)
			}
			here := false
			for _, r := range out.Suggestions {
				if r.Kind != "here" {
					continue
				}
				here = true
				wantBy, wantGlob := "", ""
				if c.claimed {
					wantBy, wantGlob = "personal", "~/www/**"
				}
				if r.ClaimedBy != wantBy || r.ClaimedGlob != wantGlob || r.Dim != c.claimed || (r.Note != "") != c.claimed {
					t.Errorf("start row %+v, want claimed by %q with %q, dimmed %v", r, wantBy, wantGlob, c.claimed)
				}
				if c.claimed && !strings.Contains(r.Note, "two setups") {
					t.Errorf("note %q does not explain the conflict", r.Note)
				}
			}
			if !here {
				t.Fatalf("start folder not offered: %s", raw)
			}
			if !c.claimed {
				return
			}
			st, ok := w.Plan("work").Step("folders")
			if !ok {
				t.Fatal("no folders step")
			}
			var got []string
			if v, has := st.Prefilled()["folders"]; has {
				if err := json.Unmarshal(v, &got); err != nil {
					t.Fatal(err)
				}
			}
			if len(got) != 0 {
				t.Fatalf("folders prefill %q picks a folder setup personal claims", got)
			}
		})
	}
}
