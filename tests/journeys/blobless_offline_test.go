//go:build journeys && !windows

package journeys

// blobless_offline_test.go — store growth SG-3a (devco:D-997): a store
// clone that lacks old blobs (a partial, blobless clone) never reads a
// missing blob as "no such version". Before: lore's in-process git reader
// answered "missing" for a blob a partial clone had not fetched, so
// `task history --git` silently left out the old versions, and the roster
// replay and the org file check read the gap as "no change".
//
//	store clone ─▶ repack --filter=blob:none (old blobs gone, tip kept)
//	online  ─▶ task history --git ─▶ git fetches the old version: both titles
//	offline ─▶ task history --git ─▶ E_OFFLINE naming lore sync, never fewer versions
//	offline ─▶ task show           ─▶ the tip reads as before

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Names of the blobless journey (placeholders).
const (
	jblOld = "draft the widget report"
	jblNew = "ship the widget report"
)

func TestJourneyBloblessStoreOffline(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email].Dir
	org := tlOrg + "/org"
	var id, top, rel string
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = lee.Environ() // lee's ~/.gitconfig routes the store url to the fake GitHub
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	w.Step("day1/a-task-with-two-versions", func() {
		tlStartTeam(t, lee, app, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		out := lee.Lore(parent, "task", "add", jblOld, "--to", org, "--json").OK(t).Out()
		m := regexp.MustCompile(`"id": "(tsk_[0-9a-f]{32})"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no task id in %s", out)
		}
		id = m[1]
		// each version its own push: a push sends one commit per author
		// (devco:D-1000), which would fold the two versions into one
		lee.Lore(parent, "sync").OK(t)
		lee.Lore(parent, "task", "edit", "--id", id, "--title", jblNew).OK(t)
		lee.Lore(parent, "sync").OK(t)
		p := jPath(t, lee.Lore(parent, "path", id, "--json").OK(t))
		top = git(filepath.Dir(p.Path), "rev-parse", "--show-toplevel")
		var err error
		if rel, err = filepath.Rel(top, p.Path); err != nil {
			t.Fatal(err)
		}
	})

	w.Step("day1/blobless-clone-offline-history-refuses", func() {
		// what `git clone --filter=blob:none` leaves, made in place: the
		// promisor config, every old blob dropped (the checked-out ones stay)
		git(top, "config", "core.repositoryformatversion", "1")
		git(top, "config", "extensions.partialClone", "origin")
		git(top, "config", "remote.origin.promisor", "true")
		git(top, "config", "remote.origin.partialclonefilter", "blob:none")
		git(top, "repack", "-a", "-d", "-q")          // lore's commits leave loose objects; a filtered repack keeps loose ones
		dropped := filepath.Join(t.TempDir(), "pack") // the filtered-out blobs go here and are never read
		git(top, "repack", "-a", "-d", "-q", "--filter=blob:none", "--filter-to="+dropped)
		tip := git(top, "ls-tree", "-r", "--format=%(objectname)", "HEAD")
		cmd := exec.Command("git", "cat-file", "--batch-check")
		cmd.Dir, cmd.Env, cmd.Stdin = top, append(lee.Environ(), "GIT_NO_LAZY_FETCH=1"), strings.NewReader(tip+"\n")
		if out, err := cmd.CombinedOutput(); err != nil || strings.Contains(string(out), "missing") {
			t.Fatalf("the tip's blobs are not all in the clone: %v %s", err, out)
		}
		check := exec.Command("git", "log", "--format=%H", "--", filepath.ToSlash(rel))
		check.Dir, check.Env = top, lee.Environ()
		revs, err := check.Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range strings.Fields(string(revs))[1:] { // every version but the newest
			probe := exec.Command("git", "cat-file", "-e", c+":"+filepath.ToSlash(rel))
			probe.Dir, probe.Env = top, append(lee.Environ(), "GIT_NO_LAZY_FETCH=1")
			if probe.Run() == nil {
				t.Fatalf("the clone still holds the version of %s at %s; the journey needs it missing", rel, c)
			}
		}
		// offline in the same step: the invariants after a step read the
		// store online, and git would fetch the old blob for them
		lee.SetEnv("LORE_OFFLINE", "1")
		lee.Lore(parent, "task", "show", "--id", id, "--store", org).OK(t).Says(t, jblNew)
		lee.Lore(parent, "task", "history", "--id", id, "--git", "--store", org).Refused(t, "E_OFFLINE", "lore sync").Never(t, jblNew)
		lee.SetEnv("LORE_OFFLINE", "")
	})

	w.Step("day2/online-history-reads-every-version", func() {
		lee.Lore(parent, "task", "history", "--id", id, "--git", "--store", org).OK(t).Says(t, jblOld, jblNew)
	})
}
