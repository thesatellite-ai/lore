//go:build journeys && !windows

package journeys

// render_norepofile_test.go — render's preview tells the truth in a repo
// that never gets a repo file (devco D-481, D-482).
//
// D-481: in a client repo linked to the org, `render --report` showed a
// `lore_md .lore/LORE.md` row and `render --dry-run` printed a LORE.md
// body, while `render --yes` (rightly) wrote nothing there. Now the
// report, the dry run and the write take one decision: no LORE.md row
// or body, one line saying why, and the same reply in all three.
// D-482: a linked own repo with no .lore folder gets only the private
// render, and the reply only said "lore init adds a .lore folder"; now
// it says how a tool gets these notes (the agent hook, lore context, the
// private file).

import (
	"path/filepath"
	"testing"
)

func TestJourneyRenderNoRepoFileAgrees(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mira := w.Persona("Mira Castell", "mira@agency.example").Laptop()
	store := w.Remote("github.com/agency-co/lore-store")
	tools := jPlainRepo(t, mira, w.Remote("github.com/agency-co/tools"), filepath.Join(mira.Home, "work", "tools"))
	shop := jPlainRepo(t, mira, w.Remote(lgClient), filepath.Join(mira.Home, "work", "shopapp"))
	sync := jPlainRepo(t, mira, w.Remote("github.com/agency-co/syncapp"), filepath.Join(mira.Home, "work", "syncapp"))

	w.Step("day1/org-with-a-client-and-an-own-repo", func() {
		mira.LoreIn(tools, "store", "init", "--name", "agencystore", "--url", store.SSHURL()).OK(t)
		mira.LoreIn(tools, "org", "create", "--org", lgOrg, "--store", store.SSHURL(), "--own", lgOwn, "--repos", lgClient,
			"--key", "human:mira@agency.example", "--name", "Mira", "--yes").OK(t)
		mira.LoreIn(tools, "store", "folder", "add", "agencystore", "shop").OK(t)
		mira.LoreIn(tools, "store", "folder", "add", "agencystore", "sync").OK(t)
		mira.LoreIn(shop, "org", "link", "--org", lgOrg, "--project", "shop", "--yes").OK(t)
		mira.LoreIn(shop, "rule", "add", "--body", "Never log card numbers.", "--severity", "must", "--to", lgOrg+"/shop").OK(t)
		mira.LoreIn(sync, "org", "link", "--org", lgOrg, "--project", "sync", "--own", "--yes").OK(t)
		mira.LoreIn(sync, "rule", "add", "--body", "Always run the migrations before deploy.", "--severity", "must", "--to", lgOrg+"/sync").OK(t)
		mira.LoreIn(sync, "rule", "add", "--body", "Migration files are append-only.", "--severity", "must", "--to", lgOrg+"/sync",
			"--activation", "glob", "--globs", `["deploy/**"]`).OK(t)
	})

	w.Step("day1/client-repo-report-dryrun-yes-agree", func() {
		for _, args := range [][]string{{"render", "--report"}, {"render", "--report", "--scopes"}, {"render", "--dry-run"}, {"render", "--yes"}} {
			r := mira.LoreIn(shop, args...).OK(t)
			t.Logf("shop: lore %v\n%s", args, r.Out())
			r.Says(t, "no LORE.md:", "is a client repo, which never holds lore files", "how a tool gets these notes").
				Never(t, "lore_md", "LORE.md (", "==> .lore", "lore init")
		}
		mira.LoreIn(shop, "render", "--report", "--json").OK(t).Says(t, `"no_lore_md"`, "is a client repo", `"pinned": 1`).Never(t, ".lore/LORE.md", `"before"`)
		mira.LoreIn(shop, "render", "--dry-run").OK(t).Says(t, "Never log card numbers.", "private, not written: --dry-run")
		mira.LoreIn(shop, "render", "--yes").OK(t).Says(t, "private context →", "no import file in a client repo", "nothing of lore's goes into a client repo")
		if st := shop.Git("status", "--porcelain", "--ignored"); st != "" {
			t.Fatalf("render put lore files into the client repo (ignored ones count):\n%s", st)
		}
		// render names lore context as the way agents read the client's
		// notes: it must really serve the linked folder's rows.
		mira.LoreIn(shop, "context", "--for-prompt", "may we log card numbers").OK(t).
			Says(t, "Never log card numbers.").Never(t, "(no rows apply here)")
	})

	w.Step("day1/linked-own-repo-says-how-a-tool-gets-its-notes", func() {
		for _, args := range [][]string{{"render", "--report"}, {"render", "--dry-run"}, {"render", "--yes"}} {
			r := mira.LoreIn(sync, args...).OK(t)
			t.Logf("sync: lore %v\n%s", args, r.Out())
			r.Says(t, "no LORE.md:", "nothing is written in the repo", "how a tool gets these notes", "CLAUDE.local.md",
				"lore context --for-file <path>", "lore setup --agent-hooks", "The always-on rules come with (1) and with the agent hooks' session start")
		}
		mira.LoreIn(sync, "render", "--yes").OK(t).Says(t, "private context →")
		// the per-file context (what the PreToolUse agent hook runs) serves
		// the linked folder's rule for a file it is about; the always-on
		// rule comes only from the private file, as the reply says.
		mira.LoreIn(sync, "context", "--for-file", "deploy/0001_init.sql").OK(t).Says(t, "Migration files are append-only.")
		if st := sync.Git("status", "--porcelain", "--ignored=no"); st != "" {
			t.Fatalf("render changed the linked own repo's work tree:\n%s", st)
		}
	})
}
