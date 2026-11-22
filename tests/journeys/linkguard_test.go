//go:build journeys && !windows

package journeys

// linkguard_test.go — linking never weakens a client repo's guards (devco
// D-428). Mira's agency keeps its own code and one client's shop under
// the same GitHub owner: own: github.com/agency-co/* and repos:
// github.com/agency-co/shopapp. Before the fix `lore org link` without
// --own planned the shop as one of the agency's OWN repos (the own:
// pattern beat the explicit client entry), printed "leak guard installed"
// and then "the client leak guards stay off", and whoami flipped from
// client to own. Now the link is a client link, whoami says the same
// before and after, and --own on the shop is refused with the way out.

import (
	"path/filepath"
	"testing"
)

const (
	lgOrg    = "agency"
	lgOwn    = "github.com/agency-co/*"
	lgClient = "github.com/agency-co/shopapp"
)

func TestJourneyLinkNeverWeakensGuards(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mira := w.Persona("Mira Castell", "mira@agency.example").Laptop()
	store := w.Remote("github.com/agency-co/lore-store")
	tools := jPlainRepo(t, mira, w.Remote("github.com/agency-co/tools"), filepath.Join(mira.Home, "work", "tools"))
	shop := jPlainRepo(t, mira, w.Remote(lgClient), filepath.Join(mira.Home, "work", "shopapp"))

	w.Step("day1/org-lists-the-shop-as-a-client", func() {
		mira.LoreIn(tools, "store", "init", "--name", "agencystore", "--url", store.SSHURL()).OK(t)
		mira.LoreIn(tools, "org", "create", "--org", lgOrg, "--store", store.SSHURL(), "--own", lgOwn, "--repos", lgClient,
			"--key", "human:mira@agency.example", "--name", "Mira", "--yes").OK(t)
		mira.LoreIn(tools, "store", "folder", "add", "agencystore", "shop").OK(t)
		mira.LoreIn(shop, "whoami").OK(t).Says(t, "client repo").Never(t, "own repo")
	})

	w.Step("day1/link-without-own-stays-client", func() {
		mira.LoreIn(shop, "org", "link", "--org", lgOrg, "--project", "shop", "--yes").OK(t).
			Says(t, "as a CLIENT repo of "+lgOrg, "leak guard installed").
			Never(t, "OWN repos", "client leak guards stay off")
		mira.LoreIn(shop, "whoami").OK(t).Says(t, "client repo", "guards on").Never(t, "own repo")
	})

	w.Step("day1/own-on-the-shop-is-refused", func() {
		mira.LoreIn(shop, "org", "link", "--org", lgOrg, "--project", "shop", "--own", "--yes").
			Refused(t, "E_INVALID_INPUT", "stays a client repo", "removes it from repos:")
		mira.LoreIn(shop, "whoami").OK(t).Says(t, "client repo", "guards on")
	})
}

// TestJourneyLinkedOwnRepoGetsItsNotes (devco D-429): Mira links her own
// repo syncapp with --own; nothing is written into it, so its notes live
// in the store folder. Before the fix context --for-prompt said "nothing
// in the store applies here", render refused E_NOT_A_STORE with a hint to
// link (already linked), --report counted 0 rules, and the dry run's
// LORE.md said "No pinned knowledge yet". Now context finds the folder's
// rows, render writes the private render (no LORE.md, the work tree stays
// clean) and --report counts the pinned rows.
func TestJourneyLinkedOwnRepoGetsItsNotes(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mira := w.Persona("Mira Castell", "mira@agency.example").Laptop()
	store := w.Remote("github.com/agency-co/lore-store")
	tools := jPlainRepo(t, mira, w.Remote("github.com/agency-co/tools"), filepath.Join(mira.Home, "work", "tools"))
	sync := jPlainRepo(t, mira, w.Remote("github.com/agency-co/syncapp"), filepath.Join(mira.Home, "work", "syncapp"))

	w.Step("day1/link-own-repo-and-add-notes-to-its-folder", func() {
		mira.LoreIn(tools, "store", "init", "--name", "agencystore", "--url", store.SSHURL()).OK(t)
		mira.LoreIn(tools, "org", "create", "--org", lgOrg, "--store", store.SSHURL(), "--own", lgOwn,
			"--key", "human:mira@agency.example", "--name", "Mira", "--yes").OK(t)
		mira.LoreIn(tools, "store", "folder", "add", "agencystore", "sync").OK(t)
		mira.LoreIn(sync, "org", "link", "--org", lgOrg, "--project", "sync", "--own", "--yes").OK(t).Says(t, "no tracked file of this repo was written")
		mira.LoreIn(sync, "rule", "add", "--body", "Always run the migrations before deploy.", "--severity", "must", "--to", lgOrg+"/sync").OK(t)
		mira.LoreIn(sync, "hotfix", "add", "--title", "worker does not hot-reload", "--body", "the worker does not hot-reload; restart it after a backend change",
			"--severity", "critical", "--to", lgOrg+"/sync").OK(t)
	})

	w.Step("day1/context-finds-the-folder-rows", func() {
		mira.LoreIn(sync, "context", "--for-prompt", "restart the worker after a backend change").OK(t).
			Says(t, "worker does not hot-reload").Never(t, "(no rows apply here)")
	})

	w.Step("day1/render-writes-the-private-notes", func() {
		mira.LoreIn(sync, "render", "--report").OK(t).Says(t, "no LORE.md", "pinned rules/hotfixes     2")
		mira.LoreIn(sync, "render", "--dry-run").OK(t).Says(t, "Always run the migrations before deploy.").Never(t, "No pinned knowledge yet")
		mira.LoreIn(sync, "render", "--yes").OK(t).Says(t, "private context").Never(t, "E_NOT_A_STORE")
		if st := sync.Git("status", "--porcelain", "--ignored=no"); st != "" {
			t.Fatalf("render changed the linked own repo's work tree:\n%s", st)
		}
	})

	// devco D-430: the private render states its real limit, and drops the
	// noise (a default trust="0.50", a Source line naming another repo's
	// row file). devco D-909: the limit is hard, pinned rows included, so
	// a 300-byte budget lists what fits and names the commands for the
	// rest; the hotfix's text, noise dropped, is one command away.
	w.Step("day2/private-render-budget-and-noise", func() {
		mira.LoreIn(sync, "hotfix", "add", "--title", "queue drains slowly", "--severity", "high", "--to", lgOrg+"/sync",
			"--body", "drain the queue before a deploy\nSource: oldapp/.lore/data/hotfixes/hfx_old.json").OK(t)
		mira.LoreIn(sync, "memory", "add", "--body", "staging lives on the second box", "--to", lgOrg+"/sync").OK(t)
		mira.LoreIn(sync, "render", "--dry-run").OK(t).
			Says(t, "drain the queue before a deploy").
			Never(t, `trust="0.50"`, "Source: oldapp/.lore/data")
		mira.LoreIn(sync, "policy", "set", "--key", "context.budget.internal", "--value", "300", "--to", lgOrg, "--yes").OK(t)
		mira.LoreIn(sync, "render", "--dry-run").OK(t).
			Says(t, "not listed to stay within context.budget.internal 300 bytes").
			Never(t, "never cut", `trust="0.50"`, "Source: oldapp/.lore/data")
	})
}
