//go:build userscen

package userscen

// Knowledge campaign follow-ups (batch 2026-11-08): the gaps the knowledge
// campaign left open, each driven through the built binary.
//   - `lore explain --id <id> --json` carries every copy (place, ref, present,
//     version stamps, winner), the winning place, its rule and the land
//     decision, not only the id;
//   - editing a row that landed on origin/main after this checkout's last
//     pull never leaves an untracked file the next `git pull` refuses to
//     overwrite: with live on the edit goes live, with --no-live it is
//     refused E_REFUSED naming git pull;
//   - a name stays unique on edit as on add: every kind whose add refuses
//     a taken name refuses an edit onto it with the same code and hint;
//   - `lore directive install --target` outside the repository (../x, an
//     absolute path elsewhere, a folder symlink leading out) is refused
//     E_BAD_PATH with a hint and writes nothing; inside it still writes;
//   - refusals never show the internal "engine: " prefix (text or --json),
//     and an unknown store in memory move/list/add names `lore store list`.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// fkExplain is `lore explain --id <id> --json`.
type fkExplain struct {
	Items  []string `json:"items"`
	ID     string   `json:"id"`
	Folder string   `json:"folder"`
	Copies []struct {
		Place     string `json:"place"`
		Ref       string `json:"ref"`
		Present   bool   `json:"present"`
		State     string `json:"state"`
		Path      string `json:"path"`
		HLC       string `json:"hlc"`
		LiveSince string `json:"live_since"`
		Winner    bool   `json:"winner"`
	} `json:"copies"`
	Winner string `json:"winner"`
	Reason string `json:"reason"`
	Land   struct {
		Action string `json:"action"`
		Text   string `json:"text"`
	} `json:"land"`
}

// present is the places holding a copy, and the one marked winner.
func (e fkExplain) present() (map[string]bool, string) {
	have, win := map[string]bool{}, ""
	for _, c := range e.Copies {
		if c.Present {
			have[c.Place] = true
		}
		if c.Winner {
			if win != "" {
				win += ","
			}
			win += c.Place
		}
	}
	return have, win
}

func TestCampaignKnowledge_ExplainJSONCopies(t *testing.T) {
	st := newSyncTeam(t, 2)
	a, b := st.clones[0], st.clones[1]
	liveID := th.DecodeJSON[knowAdded](t, a.Lore("memory", "add", "--body", "live row", "--live", "--json")).ID
	branchID := th.DecodeJSON[knowAdded](t, a.Lore("memory", "add", "--body", "kept with the code", "--no-live", "--json")).ID
	a.Lore("sync").OK(t) // lands the finished live row (never by itself, U-2)
	cases := []struct {
		name      string
		c         *th.Clone
		id        string
		have      []string
		winner    string
		reasonHas string
		land      bool
	}{
		// the sync above landed the live row, so main holds it too; live
		// still wins until main is fetched
		{"live copy wins", a, liveID, []string{"live", "default_branch"}, "live", "rule 4", true},
		{"work tree copy (not committed)", a, branchID, []string{"work_tree"}, "work_tree", "this branch's copy", false},
		{"found nowhere", b, branchID, nil, "", "not found in live", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := c.c.Lore("explain", c.id, "--json").OK(t)
			e := th.DecodeJSON[fkExplain](t, r)
			if e.ID != c.id || len(e.Items) != 1 || e.Items[0] != c.id || e.Folder == "" {
				t.Fatalf("id/items/folder: %s", r.Stdout)
			}
			have, win := e.present()
			if len(have) != len(c.have) {
				t.Fatalf("present places %v, want %v: %s", have, c.have, r.Stdout)
			}
			for _, p := range c.have {
				if !have[p] {
					t.Fatalf("no %s copy: %s", p, r.Stdout)
				}
			}
			if e.Winner != c.winner || win != c.winner {
				t.Fatalf("winner %q (copy marked %q), want %q: %s", e.Winner, win, c.winner, r.Stdout)
			}
			if !strings.Contains(e.Reason, c.reasonHas) {
				t.Fatalf("reason %q lacks %q", e.Reason, c.reasonHas)
			}
			if (e.Land.Action != "") != c.land {
				t.Fatalf("land %+v, want present=%v", e.Land, c.land)
			}
			// the text keeps its lines: the reason is the wins line
			text := c.c.Lore("explain", c.id).OK(t)
			if !strings.Contains(text.Stdout, c.reasonHas) || strings.HasPrefix(strings.TrimSpace(text.Stdout), "{") {
				t.Fatalf("text explain changed: %s", text.Stdout)
			}
		})
	}
	t.Run("live copy stamps", func(t *testing.T) {
		e := th.DecodeJSON[fkExplain](t, a.Lore("explain", liveID, "--json").OK(t))
		for _, c := range e.Copies {
			if c.Place == "live" && (c.HLC == "" || c.LiveSince == "" || c.State != "row" || c.Ref == "") {
				t.Fatalf("live copy lacks its stamps: %+v", c)
			}
		}
	})
	t.Run("landed, not pulled", func(t *testing.T) {
		a.Lore("sync").OK(t) // the finished live row leaves live
		e := th.DecodeJSON[fkExplain](t, b.Lore("explain", liveID, "--json").OK(t))
		have, win := e.present()
		if !have["default_branch"] || e.Winner != "default_branch" || win != "default_branch" {
			t.Fatalf("a row on origin/main only: %+v", e)
		}
	})
}

func TestCampaignKnowledge_EditLandedNotPulled(t *testing.T) {
	cases := []struct {
		name    string
		noun    string
		editArg []string
		env     string // set on bob's edit only
		code    int    // 0: the edit succeeds
		say     []string
	}{
		{"memory edit goes live", "memory", nil, "", 0, nil},
		{"rule edit goes live", "rule", nil, "", 0, nil},
		{"memory edit --no-live is refused", "memory", []string{"--no-live"}, "", 23, []string{"E_REFUSED", "git pull first", "landed on main after your last pull"}},
		{"rule edit --no-live is refused", "rule", []string{"--no-live"}, "", 23, []string{"E_REFUSED", "git pull first"}},
		// live off reads no live state, so the row is not found here
		// (already so before this fix); the hint names main
		{"memory edit with live off is not found", "memory", nil, "LORE_LIVE=0", 27, []string{"E_NOT_FOUND", "landed on main"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newSyncTeam(t, 2)
			a, b := st.clones[0], st.clones[1]
			k := knowKindByNoun(t, c.noun)
			id := th.DecodeJSON[knowAdded](t, a.Lore(c.noun, "add", "--body", "live at once", "--live", "--json")).ID
			a.Lore("sync").OK(t) // lands it (landing waits for sync, U-2)
			a.Lore("sync").OK(t) // main fetched: the landed row leaves live
			b.Lore("explain", id).OK(t).Says(t, "origin/main: present")
			// the row's own table: bob's first write also creates his actor
			// row (a new file alice does not have, so the pull is not hurt)
			rows := ".lore/data/" + fkTable(c.noun)
			before := b.Git("status", "--porcelain", "--untracked-files=all", "--", rows)
			editor := b
			if c.env != "" {
				editor = b.With(c.env)
			}
			edit := editor.Lore(append([]string{c.noun, "edit", id, "--body", "bob edits before pulling"}, c.editArg...)...)
			refused := c.code != 0
			if refused {
				edit.Fails(t, c.code, c.say...)
			} else {
				edit.OK(t)
			}
			if after := b.Git("status", "--porcelain", "--untracked-files=all", "--", rows); after != before {
				t.Fatalf("the edit touched .lore/data:\nbefore %q\nafter  %q", before, after)
			}
			b.Git("pull", "-q", "--no-rebase", "origin", "main")
			want := "live at once"
			if !refused {
				want = "bob edits before pulling"
			}
			if got := knowShowRow(t, b, k, id).Body; got != want {
				t.Fatalf("bob sees %q after the pull, want %q", got, want)
			}
			if got := knowShowRow(t, a, k, id).Body; got != want {
				t.Fatalf("alice sees %q, want %q", got, want)
			}
			if refused { // after the pull the same edit works
				b.Lore(c.noun, "edit", id, "--body", "after the pull", "--no-live").OK(t)
			}
		})
	}
}

// fkTable is the .lore/data folder of a noun's rows.
func fkTable(noun string) string {
	switch noun {
	case "memory":
		return "memories"
	case "rule":
		return "rules"
	}
	return noun + "s"
}

func TestCampaignKnowledge_EditKeepsNamesUnique(t *testing.T) {
	scenParallel(t)
	_, _, c := knowProject(t)
	refusing := map[string]bool{}
	for _, k := range campKnowKinds() {
		if !k.addTitle || !k.canEdit || k.editFlag == "" {
			continue
		}
		flags := []string{k.editFlag}
		if k.editFlag != "--title" && k.noun == "prompt" {
			flags = append(flags, "--title") // the flag the campaign typed
		}
		for _, flag := range flags {
			t.Run(k.noun+flag, func(t *testing.T) {
				alpha, beta := "Alpha "+k.noun+flag, "Beta "+k.noun+flag
				first := campMustAdd(t, c, k, alpha, "first")
				second := campMustAdd(t, c, k, beta, "second")
				add := c.Lore(campAddArgs(k, alpha, "again")...)
				edit := c.Lore(k.noun, "edit", second, flag, alpha)
				if add.Code == 0 {
					// add allows a repeated name here, so edit must too
					edit.OK(t)
					return
				}
				refusing[k.noun] = true
				add.Fails(t, 23, "E_NAME_TAKEN", first)
				edit.Fails(t, 23, "E_NAME_TAKEN", first, "pick another")
				if !strings.Contains(edit.Stderr, strings.SplitN(add.Stderr, "(", 2)[0]) {
					t.Fatalf("edit words the refusal differently from add:\nadd  %s\nedit %s", add.Stderr, edit.Stderr)
				}
				row := knowShowRow(t, c, k, second)
				if row.Title+row.Name != beta {
					t.Fatalf("the refused edit renamed the row: %+v", row)
				}
				// the row's own name, unchanged, and a free name both pass
				c.Lore(k.noun, "edit", second, flag, beta, "--body", "own name kept").OK(t)
				c.Lore(k.noun, "edit", second, flag, "Gamma "+k.noun+flag).OK(t)
				// once the holder is deleted the name is free again
				c.Lore(k.noun, "delete", first, "--yes").OK(t)
				c.Lore(k.noun, "edit", second, flag, alpha).OK(t)
			})
		}
	}
	for _, n := range []string{"prompt"} { // the kind with a unique name today
		if !refusing[n] {
			t.Fatalf("%s add no longer refuses a taken name: the matrix proves nothing for it", n)
		}
	}
}

func TestCampaignKnowledge_DirectiveTargetOutsideRepo(t *testing.T) {
	scenParallel(t)
	_, _, c := knowProject(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(c.Dir, "out-link")); err != nil {
		t.Fatal(err)
	}
	refused := []struct{ name, target, lands string }{
		{"parent folder", "../escaped.md", filepath.Join(filepath.Dir(c.Dir), "escaped.md")},
		{"absolute elsewhere", filepath.Join(outside, "abs.md"), filepath.Join(outside, "abs.md")},
		{"folder symlink out", "out-link/through.md", filepath.Join(outside, "through.md")},
	}
	for _, r := range refused {
		t.Run(r.name, func(t *testing.T) {
			got := c.Lore("directive", "install", "--target", r.target).Fails(t, 20, "E_BAD_PATH", "outside the repository", "hint:", "--target claude_code")
			e := knowJSONErr(t, c.Lore("directive", "install", "--target", r.target, "--json"))
			if e.Error.Code != "E_BAD_PATH" || e.Error.Hint == "" {
				t.Fatalf("--json error %+v (text: %s)", e.Error, got.Stderr)
			}
			if _, err := os.Stat(r.lands); err == nil {
				t.Fatalf("a refused install wrote %s", r.lands)
			}
		})
	}
	for _, in := range []string{"docs/AI.md", filepath.Join(c.Dir, "docs", "ABS.md"), "claude_code"} {
		t.Run("inside "+filepath.Base(in), func(t *testing.T) {
			c.Lore("directive", "install", "--target", in).OK(t)
		})
	}
}

func TestCampaignKnowledge_RefusalWording(t *testing.T) {
	scenParallel(t)
	_, _, c := knowProject(t)
	id := campMustAdd(t, c, knowKindByNoun(t, "memory"), "", "a memory")
	gone := campMustAdd(t, c, knowKindByNoun(t, "memory"), "", "deleted")
	c.Lore("memory", "delete", gone, "--yes").OK(t)
	cases := []struct {
		name string
		args []string
		exit int
		code string
		say  []string
	}{
		{"edit of an unknown id", []string{"memory", "edit", "mem_01a1b7f7a3a6735d94dcf8ad0bb4b1f5", "--body", "x"}, 27, "E_NOT_FOUND", []string{"no row mem_01a1b7f7a3a6735d94dcf8ad0bb4b1f5"}},
		{"edit of a trashed row", []string{"memory", "edit", gone, "--body", "x"}, 23, "E_IN_TRASH", []string{gone + " is in the trash", "lore trash restore"}},
		{"move to an unknown store", []string{"memory", "move", id, "--to", "nosuch", "--yes"}, 27, "E_NOT_FOUND", []string{"no store named nosuch", "lore store list"}},
		{"move to an unknown store folder", []string{"memory", "move", id, "--to", "nosuch/docs", "--yes"}, 27, "E_NOT_FOUND", []string{"no store named nosuch/docs", "lore store list"}},
		{"list of an unknown store", []string{"memory", "list", "--store", "nosuch"}, 27, "E_NOT_FOUND", []string{"no store named nosuch", "lore store list"}},
		{"add to an unknown store", []string{"memory", "add", "--body", "x", "--to", "nosuch"}, 27, "E_NOT_FOUND", []string{"no store named nosuch", "lore store list"}},
		{"move to its own store", []string{"memory", "move", id, "--to", "repo", "--yes"}, 20, "E_INVALID_INPUT", []string{"already in store repo", "lore store list"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := c.Lore(tc.args...).Fails(t, tc.exit, append([]string{tc.code, "hint:"}, tc.say...)...)
			if strings.Contains(r.Stderr, "engine:") {
				t.Fatalf("the internal engine: prefix reached the person:\n%s", r.Stderr)
			}
			e := knowJSONErr(t, c.Lore(append(tc.args, "--json")...))
			if e.Error.Code != tc.code || strings.HasPrefix(e.Error.Message, "engine:") || e.Error.Hint == "" {
				t.Fatalf("--json error %+v", e.Error)
			}
		})
	}
}
