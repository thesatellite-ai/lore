//go:build journeys && !windows

package journeys

// blobless_clone_test.go — store growth SG-3b (devco:D-1001): with
// store.clone_filter blob:none a teammate's new store clone holds every
// commit and tree but only the blobs it needs; new history a sync brings
// comes with every blob; an old version the clone lacks is fetched when a
// read needs it, and offline that read stops with E_OFFLINE naming `lore
// store prefetch`, never fewer versions.
//
//	lee: task in three versions, sync, invite
//	ann: store.clone_filter blob:none, joins ─▶ her clone is a partial clone
//	lee: two more versions ─▶ ann syncs: she reads them, nothing new is missing
//	ann: store reclone --filter blob:none ─▶ old versions not in the clone
//	ann offline: task history --git ─▶ E_OFFLINE + lore store prefetch; task show works
//	ann: store prefetch ─▶ offline history reads every version
//	ann: a task not pushed ─▶ store reclone refuses (lore sync first)

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Titles of the blobless clone journey (placeholders).
var jbcTitles = []string{"draft the gadget plan", "review the gadget plan", "ship the gadget plan", "measure the gadget plan", "retire the gadget plan"}

func TestJourneyBloblessStoreClone(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	org := tlOrg + "/org"
	var id, line, top string
	leeDir := filepath.Join(lee.Home, "code")

	w.Step("day1/lead-writes-three-versions", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		out := lee.Lore(leeDir, "task", "add", jbcTitles[0], "--to", org, "--json").OK(t).Out()
		m := regexp.MustCompile(`"id": "(tsk_[0-9a-f]{32})"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no task id in %s", out)
		}
		id = m[1]
		// a sync after each version: a push squashes unpushed writes into
		// one commit per author (devco:D-1000), and the journey needs each
		// version in its own commit
		lee.Lore(leeDir, "sync").OK(t)
		for _, title := range jbcTitles[1:3] {
			lee.Lore(leeDir, "task", "edit", "--id", id, "--title", title).OK(t)
			lee.Lore(leeDir, "sync").OK(t)
		}
		_, line = tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
	})

	ann := tt.person(tlAnn)
	annDir := filepath.Join(ann.Home, "code")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = top, append(ann.Environ(), "GIT_NO_LAZY_FETCH=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s: %v: %s", args, top, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	missing := func() int {
		t.Helper()
		return strings.Count(git("rev-list", "--objects", "--missing=print", "--all")+"\n", "\n?")
	}

	w.Step("day1/ann-joins-with-blobless-clones", func() {
		if err := os.MkdirAll(filepath.Dir(ann.LorePath("config.yaml")), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ann.LorePath("config.yaml"), []byte("store:\n  clone_filter: blob:none\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		ann.Lore(annDir, "task", "show", "--id", id, "--store", org).OK(t).Says(t, jbcTitles[2])
		p := jPath(t, ann.Lore(annDir, "path", id, "--json").OK(t))
		top = filepath.Dir(p.Path)
		top = git("rev-parse", "--show-toplevel")
		if got := git("config", "--get", "remote.origin.promisor"); got != "true" {
			t.Fatalf("ann's store clone is not blobless: remote.origin.promisor = %q", got)
		}
	})

	w.Step("day2/new-history-comes-with-every-blob", func() {
		before := missing()
		for _, title := range jbcTitles[3:] {
			lee.Lore(leeDir, "task", "edit", "--id", id, "--title", title).OK(t)
			lee.Lore(leeDir, "sync").OK(t)
		}
		ann.Lore(annDir, "sync").OK(t)
		ann.Lore(annDir, "task", "show", "--id", id, "--store", org).OK(t).Says(t, jbcTitles[4])
		if after := missing(); after != before {
			t.Fatalf("ann's clone lacked %d objects before the sync and %d after: the new versions came without their blobs", before, after)
		}
	})

	w.Step("day2/reclone-blobless-offline-history-refuses", func() {
		ann.Lore(annDir, "store", "reclone", "--store", org, "--filter", "blob:none").OK(t).Says(t, "blobless clone", "replaced-clones")
		if missing() == 0 {
			t.Fatal("the remade blobless clone holds every old version; the step needs them missing")
		}
		ann.SetEnv("LORE_OFFLINE", "1")
		ann.Lore(annDir, "task", "show", "--id", id, "--store", org).OK(t).Says(t, jbcTitles[4])
		ann.Lore(annDir, "task", "history", "--id", id, "--git", "--store", org).Refused(t, "E_OFFLINE", "lore store prefetch --store").Never(t, jbcTitles[0])
		ann.Lore(annDir, "store", "prefetch", "--store", org).Refused(t, "E_OFFLINE")
		ann.SetEnv("LORE_OFFLINE", "")
	})

	w.Step("day2/prefetch-then-offline-history-reads-every-version", func() {
		ann.Lore(annDir, "store", "prefetch", "--store", org).OK(t).Says(t, "every history read now works offline")
		if n := missing(); n != 0 {
			t.Fatalf("after prefetch ann's clone still lacks %d objects", n)
		}
		ann.SetEnv("LORE_OFFLINE", "1")
		ann.Lore(annDir, "task", "history", "--id", id, "--git", "--store", org).OK(t).Says(t, jbcTitles...)
		ann.SetEnv("LORE_OFFLINE", "")
	})

	w.Step("day3/reclone-refuses-unpushed-work", func() {
		ann.Lore(annDir, "task", "add", "second review of the gadget plan", "--to", org).OK(t)
		ann.Lore(annDir, "store", "reclone", "--store", org).Refused(t, "E_REFUSED", "the remote does not have", "lore sync")
		ann.Lore(annDir, "sync").OK(t)
		ann.Lore(annDir, "store", "reclone", "--store", org, "--filter", "none").OK(t).Says(t, "full clone")
		ann.Lore(annDir, "task", "list", "--store", org).OK(t).Says(t, "second review of the gadget plan")
	})
}
