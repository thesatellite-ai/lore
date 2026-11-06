package wire

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// storeLinks reads the links of folder's _meta.json on the store's main.
func storeLinks(t *testing.T, w *orgWorld, folder string) []linkRow {
	t.Helper()
	raw := orgGit(t, filepath.Join(w.hosts, "agency/notes.git"), "show", "main:"+folder+"/.lore/data/_meta.json")
	var doc struct {
		Links []linkRow `json:"links"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Links
}

type linkRow struct {
	ProjectID string `json:"project_id"`
	Remote    string `json:"remote"`
	Own       bool   `json:"own"`
}

// TestFollowRepoLinks (PROVENANCE 10.4, Q7): after a confirmed address
// change the store folders that link this project get a link per confirmed
// address they lack, keeping the folder's own flag; lines name the org
// entry, never the store url. No org entries, or a project no folder
// links, is nothing to do; a store that refuses the push is a warning line
// (set-url itself still succeeds).
func TestFollowRepoLinks(t *testing.T) {
	ctx := context.Background()
	t.Run("no org entries", func(t *testing.T) {
		repo := gitRepo(t)
		a := openWith(t, repo, Options{})
		if lines := a.FollowRepoLinks(ctx, "prj_01a1a5aca7f57659be7320c5f4b70011", []string{"github.com/acme/app"}); len(lines) != 0 {
			t.Fatalf("lines %v", lines)
		}
	})

	w := newOrgWorld(t)
	mira := w.home(t, "mira")
	h := orgHostAt(t, w.repo, mira)
	if _, err := h.Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("create: %v", err)
	}
	app, err := New(StandaloneOptions(w.repo, mira, w.repo, "test"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	pid, ok := repoMetaProjectID(w.repo) // the link names _meta.json's project id
	if !ok {
		t.Fatal("no project id in _meta.json")
	}

	t.Run("unlinked project", func(t *testing.T) {
		if lines := app.FollowRepoLinks(ctx, pid, []string{"github.com/acme/app-two", "github.com/acme/app"}); len(lines) != 0 {
			t.Fatalf("lines %v", lines)
		}
	})

	if _, err = h.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "acme"}, true); err != nil {
		t.Fatalf("link: %v", err)
	}
	t.Run("linked folder follows", func(t *testing.T) {
		confirmed := []string{"github.com/acme/app-two", "mirror.example/acme/app", "github.com/acme/app"}
		lines := app.FollowRepoLinks(ctx, pid, confirmed)
		if len(lines) != 1 || lines[0] != "✓ store agency, folder acme: link updated (3 confirmed addresses)" {
			t.Fatalf("lines %v", lines)
		}
		got := map[string]bool{}
		for _, l := range storeLinks(t, w, "acme") {
			if l.ProjectID != pid || l.Own {
				t.Fatalf("link %+v", l)
			}
			got[l.Remote] = true
		}
		for _, r := range confirmed {
			if !got[r] {
				t.Fatalf("no link for %s: %v", r, got)
			}
		}
		if again := app.FollowRepoLinks(ctx, pid, confirmed); len(again) != 0 {
			t.Fatalf("a second follow with nothing missing printed %v", again)
		}
	})

	t.Run("a refusing store is a warning", func(t *testing.T) {
		falseBin, lerr := exec.LookPath("false")
		if lerr != nil {
			t.Fatal(lerr)
		}
		hook := filepath.Join(w.hosts, "agency/notes.git", "hooks", "pre-receive")
		if err := os.Symlink(falseBin, hook); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Remove(hook) }() // the next subtest pushes again
		lines := app.FollowRepoLinks(ctx, pid, []string{"github.com/acme/app-three", "github.com/acme/app"})
		if len(lines) != 1 || !strings.HasPrefix(lines[0], repoLinkWarning+"store agency, folder acme: link not updated: ") || strings.Contains(lines[0], "✓") {
			t.Fatalf("lines %v", lines)
		}
	})

	t.Run("own flag is copied", func(t *testing.T) {
		site := w.ownRepo(t, mira)
		hs := orgHostAt(t, site, mira)
		if _, lerr := hs.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "site", Own: true}, true); lerr != nil {
			t.Fatalf("link --own: %v", lerr)
		}
		spid, sok := repoMetaProjectID(site)
		if !sok {
			t.Fatal("no project id")
		}
		sa, serr := New(StandaloneOptions(site, mira, site, "test"))
		if serr != nil {
			t.Fatal(serr)
		}
		defer func() { _ = sa.Close() }()
		lines := sa.FollowRepoLinks(ctx, spid, []string{"github.com/agency/site-renamed", "github.com/agency/site"})
		if len(lines) != 1 || !strings.HasPrefix(lines[0], "✓ store agency, folder site:") {
			t.Fatalf("lines %v", lines)
		}
		for _, l := range storeLinks(t, w, "site") {
			if !l.Own {
				t.Fatalf("link %+v lost the own flag", l)
			}
		}
	})
}
