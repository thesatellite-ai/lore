//go:build journeys && !windows

package journeys

// pinned_asof_test.go — devco:D-952 (bug hunt H-211): the context lore
// renders for an agent (LORE.md, LORE.full.md, the private files and the
// SessionStart answer of a repo with no LORE.md) printed the pinned rules
// and hotfixes without saying when they were taken. A session that loaded
// them in the morning acted in the afternoon on a pinned warning that had
// been edited or retired since. Each now says "Pinned rows as of <time>"
// (the newest change among those rows, so a re-render is byte-identical)
// and names `lore show --id <id>`, which prints a row's current text.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// asOfLine is the line next to the pinned rows: a UTC minute and the
// command for a row's current text (up to its placeholder, which the
// hook's JSON answer escapes).
var asOfLine = regexp.MustCompile("_Pinned rows as of \\d{4}-\\d{2}-\\d{2} \\d{2}:\\d{2} UTC; a row's current text: `lore show --id ")

func TestJourneyPinnedRowsSayWhenTaken(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mira := w.Persona("Mira Castell", "mira@agency.example").Laptop()
	store := w.Remote("github.com/agency-co/lore-store")
	tools := jPlainRepo(t, mira, w.Remote("github.com/agency-co/tools"), filepath.Join(mira.Home, "work", "tools"))
	shop := jPlainRepo(t, mira, w.Remote(lgClient), filepath.Join(mira.Home, "work", "shopapp"))
	sync := jPlainRepo(t, mira, w.Remote("github.com/agency-co/syncapp"), filepath.Join(mira.Home, "work", "syncapp"))
	own := jPlainRepo(t, mira, w.Remote("github.com/agency-co/ownapp"), filepath.Join(mira.Home, "work", "ownapp"))
	var hotfix string

	w.Step("D-952/pinned-rows-in-a-store-folder-and-in-a-repo", func() {
		mira.LoreIn(tools, "store", "init", "--name", "agencystore", "--url", store.SSHURL()).OK(t)
		mira.LoreIn(tools, "org", "create", "--org", lgOrg, "--store", store.SSHURL(), "--own", lgOwn, "--repos", lgClient,
			"--key", "human:mira@agency.example", "--name", "Mira", "--yes").OK(t)
		mira.LoreIn(tools, "store", "folder", "add", "agencystore", "shop").OK(t)
		mira.LoreIn(tools, "store", "folder", "add", "agencystore", "sync").OK(t)
		mira.LoreIn(shop, "org", "link", "--org", lgOrg, "--project", "shop", "--yes").OK(t)
		mira.LoreIn(shop, "rule", "add", "--body", "Never log card numbers.", "--severity", "must", "--to", lgOrg+"/shop").OK(t)
		mira.LoreIn(sync, "org", "link", "--org", lgOrg, "--project", "sync", "--own", "--yes").OK(t)
		mira.LoreIn(sync, "hotfix", "add", "--title", "worker does not hot-reload", "--severity", "critical", "--to", lgOrg+"/sync",
			"--body", "Restart the worker after a backend change.").OK(t)
		mira.LoreIn(own, "init", "--yes").OK(t)
		mira.LoreIn(own, "rule", "add", "--body", "Mask emails in every log line.", "--severity", "must", "--yes").OK(t)
		r := mira.LoreIn(own, "hotfix", "add", "--title", "Retries double-charge", "--severity", "critical",
			"--body", "Do not retry the charge call.", "--yes").OK(t)
		hotfix = regexp.MustCompile(`hfx_[0-9a-f]{20,}`).FindString(r.Stdout + r.Stderr)
		if hotfix == "" {
			t.Fatalf("hotfix add printed no id:\n%s\n%s", r.Stdout, r.Stderr)
		}
	})

	w.Step("D-952/session-start-says-when-the-pinned-rows-were-taken", func() {
		for name, repo := range map[string]*teamharness.Clone{"own linked repo": sync, "client repo": shop} {
			out := mira.LoreStdinIn(repo, ssHookIn, ssHookArgs...).OK(t).Stdout
			if n := len(asOfLine.FindAllString(out, -1)); n != 1 {
				t.Fatalf("%s SessionStart: %d pinned-rows-as-of lines, want 1:\n%s", name, n, out)
			}
		}
		mira.LoreIn(sync, "render", "--yes").OK(t)
		if !privateRendersSay(t, mira.LorePath("render"), "_Pinned rows as of ") {
			t.Fatal("the private files do not say when their pinned rows were taken")
		}
	})

	w.Step("D-952/lore-md-and-the-full-file-say-it-and-re-render-the-same", func() {
		mira.LoreIn(own, "render").OK(t)
		read := func(rel string) string {
			b, err := os.ReadFile(filepath.Join(own.Dir, ".lore", rel))
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
		md, full := read("LORE.md"), read("LORE.full.md")
		for name, body := range map[string]string{"LORE.md": md, "LORE.full.md": full} {
			if n := len(asOfLine.FindAllString(body, -1)); n != 1 {
				t.Fatalf("%s: %d pinned-rows-as-of lines, want 1:\n%s", name, n, body)
			}
		}
		if strings.Index(md, "_Pinned rows as of ") > strings.Index(md, "## Critical hotfixes") {
			t.Fatalf("LORE.md says when its pinned rows were taken after them:\n%s", md)
		}
		mira.LoreIn(own, "render").OK(t)
		if read("LORE.md") != md || read("LORE.full.md") != full {
			t.Fatal("a re-render with no row changed is not byte-identical (the time must come from the rows, not the clock)")
		}
		// the printed command works where printed
		mira.LoreIn(own, "show", "--id", hotfix).OK(t).Says(t, "Do not retry the charge call.")
	})
}
