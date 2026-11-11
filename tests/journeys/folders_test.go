//go:build journeys && !windows

package journeys

// folders_test.go — an admin keeps their clients' folders in a plain parent
// directory, not a git repo (owner report, 2026-11-11). From that parent,
// with only the setup behind them, Lee creates a team folder per client,
// lists the stores and the folders, and only then does a client repo attach
// one of them. None of it needs a repo, and nothing run there touches a repo
// Lee did not ask to change.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The clients and North's repo (placeholders).
var jFolderClients = []string{"client_north", "client_south", "client_east"}

const (
	jFolderNorthSlug = "github.com/acme-co/north-web"
	// jFolderCompany is the org's company folder, which every linked repo
	// attaches beside its own folder.
	jFolderCompany = "org"
)

// jFolderRow is the part of a `store folder list --json` row the journey reads.
type jFolderRow struct {
	Store    string `json:"store"`
	Folder   string `json:"folder"`
	ID       string `json:"id"`
	Attached bool   `json:"attached"`
}

func TestJourneyFoldersNoRepo(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	// ~/code is the parent of Lee's clones: a plain folder inside the
	// setup's folders (~/code/**), not a git repo.
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email]

	w.Step("day1/start-team", func() {
		tlStartTeam(t, lee, app.Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
	})

	w.Step("day2/client-folders-from-the-parent", func() {
		w.AdvanceDays(1)
		if _, err := os.Stat(filepath.Join(parent, ".git")); err == nil {
			t.Fatalf("%s is a git repo; the journey needs a plain folder", parent)
		}
		before := jRepoSnapshot(t, app.Dir)

		lee.Lore(parent, "store", "list").OK(t).Says(t, tlOrg)
		for _, c := range jFolderClients {
			lee.Lore(parent, "store", "folder", "add", "team", c).OK(t).Says(t, c)
		}
		// the org store named by its url works the same
		lee.Lore(parent, "store", "folder", "add", tt.store.SSHURL(), "client_west").OK(t).Says(t, "client_west")
		for _, c := range append(slices.Clone(jFolderClients), "client_west") {
			if len(tt.store.FilesMatching("refs/heads/main", c+"/")) == 0 {
				t.Errorf("the team store has no %s folder after store folder add from %s", c, parent)
			}
		}

		rows := jFolderList(t, lee.Lore(parent, "store", "folder", "list", "--json").OK(t))
		for _, c := range append(slices.Clone(jFolderClients), "client_west") {
			i := slices.IndexFunc(rows, func(r jFolderRow) bool { return r.Folder == c })
			if i < 0 {
				t.Errorf("store folder list from %s does not list %s: %+v", parent, c, rows)
				continue
			}
			if !strings.HasPrefix(rows[i].ID, "prj_") || rows[i].Attached {
				t.Errorf("folder %s = %+v, want a prj_ id and not attached (no repo here)", c, rows[i])
			}
		}
		lee.Lore(parent, "store", "folder", "list", "team").OK(t).Says(t, jFolderClients...)

		// a stores.yaml name needs a repo, and the error says which names work here
		lee.Lore(parent, "store", "folder", "add", "golang", "x").Refused(t).Says(t, "golang", "team", "me")

		// running from the parent changed nothing in Lee's app repo
		if after := jRepoSnapshot(t, app.Dir); after != before {
			t.Errorf("store commands run from %s changed the app repo:\nbefore %s\nafter  %s", parent, before, after)
		}
	})

	w.Step("day3/a-client-repo-attaches-one", func() {
		w.AdvanceDays(1)
		// North's repo: cloned into the parent, made a lore project, linked
		// to its folder in the team store.
		north := lee.Clone(w.Remote(jFolderNorthSlug), "north-web")
		lee.MoveClone(t, north, filepath.Join(parent, "north-web"))
		lee.LoreIn(north, "init", "--non-interactive", "--name=north-web").OK(t)
		lee.LoreIn(north, "org", "link", tlOrg, "--project", jFolderClients[0], "--yes").OK(t)
		rows := jFolderList(t, lee.LoreIn(north, "store", "folder", "list", "team", "--json").OK(t))
		if !slices.ContainsFunc(rows, func(r jFolderRow) bool { return r.Folder == jFolderClients[0] }) {
			t.Errorf("in north-web, store folder list team does not list %s: %+v", jFolderClients[0], rows)
		}
		for _, r := range rows {
			// the linked folder and the org's company folder (org/) are attached
			if want := r.Folder == jFolderClients[0] || r.Folder == jFolderCompany; r.Attached != want {
				t.Errorf("in north-web, folder %s attached = %v, want %v", r.Folder, r.Attached, want)
			}
		}
		lee.LoreIn(north, "store", "list").OK(t).Says(t, tlOrg)

		// The owner's case: a store-only command run inside a lore repo it
		// is not about. A fresh clone of a lore repo has .lore/ but none of
		// lore's git wiring (hooks are never cloned), so a command that
		// wired the repo would show here; this one must change nothing (its
		// .gitignore, .gitattributes, hooks and working tree).
		north.CommitAll("lore: init")
		north.Git("push", "-q", "origin", "HEAD")
		other := lee.Clone(w.Remote(jFolderNorthSlug), "north-web-2")
		lee.MoveClone(t, other, filepath.Join(parent, "north-web-2"))
		before := jRepoSnapshot(t, other.Dir)
		lee.LoreIn(other, "store", "folder", "add", "team", "client_extra").OK(t).Says(t, "client_extra")
		lee.LoreIn(other, "store", "folder", "list").OK(t).Says(t, "client_extra")
		lee.LoreIn(other, "store", "list").OK(t).Says(t, tlOrg)
		if after := jRepoSnapshot(t, other.Dir); after != before {
			t.Errorf("store commands inside a fresh clone of north-web changed the repo:\nbefore %s\nafter  %s", before, after)
		}
	})
}

// jFolderList decodes `store folder list --json`.
func jFolderList(t testing.TB, r teamharness.Result) []jFolderRow {
	t.Helper()
	var out struct {
		Folders []jFolderRow `json:"folders"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("store folder list --json is not JSON (%v): %s", err, r)
	}
	return out.Folders
}

// jRepoSnapshot is a repo's git status (tracked changes and untracked
// files; lore's own ignored cache of a lore project may appear), its
// .gitignore and .gitattributes and its .git/hooks: what a command that
// only touches a store must leave alone.
func jRepoSnapshot(t testing.TB, dir string) string {
	t.Helper()
	var b strings.Builder
	st, err := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil {
		t.Fatalf("git status in %s: %v", dir, err)
	}
	b.WriteString("status:\n" + string(st) + ";")
	for _, f := range []string{".gitignore", ".gitattributes", filepath.Join(".git", "info", "attributes")} {
		data, err := os.ReadFile(filepath.Join(dir, f))
		b.WriteString(f + "=" + string(data) + ";")
		if err != nil {
			b.WriteString("(none);")
		}
	}
	hooks, _ := os.ReadDir(filepath.Join(dir, ".git", "hooks"))
	for _, h := range hooks {
		data, _ := os.ReadFile(filepath.Join(dir, ".git", "hooks", h.Name()))
		b.WriteString("hook:" + h.Name() + "=" + string(data) + ";")
	}
	return b.String()
}
