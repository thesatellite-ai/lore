//go:build journeys && !windows

package journeys

// sessionstart_norepofile_test.go — the always-on rules reach an agent in
// a repo that never gets a LORE.md (devco D-494).
//
// A linked own repo with no .lore folder and a client repo hold no
// LORE.md, so their must rules and critical/high hotfixes lived only in
// the private render file, which a tool read only if someone pointed it
// there by hand; the SessionStart context hook served in-flight tasks and
// changed rows, never the pinned rules, and `lore sync` refreshed the
// private file only in repos with a LORE.md. Now the SessionStart hook
// serves the pinned set once, a sync refreshes the private file, and
// `lore setup --agent-hooks` installs in the own repo but writes nothing
// in the client repo (it says lore context is the way).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ssHookArgs is the SessionStart context hook command (toolprofile).
var ssHookArgs = []string{"context", "--hook", "claude_code", "--hook-stdin", "--session-start"}

// ssHookIn is what the tool sends the SessionStart hook.
const ssHookIn = `{"hook_event_name":"SessionStart","session_id":"sess-1"}`

func TestJourneySessionStartServesPinnedWithoutLoreMD(t *testing.T) {
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
		mira.LoreIn(sync, "hotfix", "add", "--title", "worker does not hot-reload", "--severity", "critical", "--to", lgOrg+"/sync",
			"--body", "Restart the worker after a backend change.").OK(t)
		mira.LoreIn(sync, "rule", "add", "--body", "Prefer short commit subjects.", "--severity", "should", "--to", lgOrg+"/sync").OK(t)
	})

	w.Step("day1/session-start-hook-serves-the-pinned-rules-once", func() {
		r := mira.LoreStdinIn(sync, ssHookIn, ssHookArgs...).OK(t)
		t.Logf("sync SessionStart:\n%s", r.Stdout)
		for _, s := range []string{"Always run the migrations before deploy.", "Restart the worker after a backend change.", "Always-on rules and hotfixes"} {
			if n := strings.Count(r.Stdout, s); n != 1 {
				t.Fatalf("sync SessionStart: %q appears %d times, want once:\n%s", s, n, r.Stdout)
			}
		}
		// the private render's order: rules, then hotfixes; a should rule
		// is not always-on
		if strings.Index(r.Stdout, "Always run the migrations") > strings.Index(r.Stdout, "Restart the worker") {
			t.Fatalf("sync SessionStart: the hotfix came before the rule:\n%s", r.Stdout)
		}
		r.Never(t, "Prefer short commit subjects.")

		c := mira.LoreStdinIn(shop, ssHookIn, ssHookArgs...).OK(t)
		t.Logf("shop SessionStart:\n%s", c.Stdout)
		if n := strings.Count(c.Stdout, "Never log card numbers."); n != 1 {
			t.Fatalf("shop SessionStart: the client's must rule appears %d times, want once:\n%s", n, c.Stdout)
		}
		c.Never(t, "Always run the migrations")
		if st := shop.Git("status", "--porcelain", "--ignored"); st != "" {
			t.Fatalf("the hook put files into the client repo:\n%s", st)
		}
	})

	w.Step("day1/sync-refreshes-the-private-file", func() {
		mira.LoreIn(sync, "render", "--yes").OK(t)
		mira.LoreIn(shop, "render", "--yes").OK(t)
		mira.LoreIn(sync, "rule", "add", "--body", "Tag every release by hand.", "--severity", "must", "--to", lgOrg+"/sync").OK(t)
		mira.LoreIn(shop, "rule", "add", "--body", "Mask the client's emails in logs.", "--severity", "must", "--to", lgOrg+"/shop").OK(t)
		if privateRendersSay(t, mira.LorePath("render"), "Tag every release by hand.") {
			t.Fatal("the private file already holds the new rule before the sync: the step proves nothing")
		}
		mira.LoreIn(sync, "sync").OK(t)
		if !privateRendersSay(t, mira.LorePath("render"), "Tag every release by hand.") {
			t.Fatal("lore sync in the linked own repo left its private file stale")
		}
		mira.LoreIn(shop, "sync").OK(t)
		if !privateRendersSay(t, mira.LorePath("render"), "Mask the client's emails in logs.") {
			t.Fatal("lore sync in the client repo left its private file stale")
		}
		if st := shop.Git("status", "--porcelain", "--ignored"); st != "" {
			t.Fatalf("the sync put files into the client repo:\n%s", st)
		}
	})

	w.Step("day1/agent-hooks-own-repo-yes-client-repo-no", func() {
		if err := os.MkdirAll(filepath.Join(sync.Dir, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		mira.LoreIn(sync, "setup", "--agent-hooks", "--yes").OK(t)
		b, err := os.ReadFile(filepath.Join(sync.Dir, ".claude", "settings.local.json"))
		if err != nil || !strings.Contains(string(b), "--session-start") {
			t.Fatalf("the own repo got no SessionStart context hook (%v):\n%s", err, b)
		}
		mira.LoreIn(shop, "setup", "--agent-hooks", "--yes").Refused(t, "writes nothing in a client repo", "lore context --for-file <path>", "--session-start")
		if st := shop.Git("status", "--porcelain", "--ignored"); st != "" {
			t.Fatalf("setup --agent-hooks put files into the client repo:\n%s", st)
		}
	})
}

// privateRendersSay reports whether any private render file under dir
// holds text.
func privateRendersSay(t *testing.T, dir, text string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		found = strings.Contains(string(b), text)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read the private renders: %v", err)
	}
	return found
}
