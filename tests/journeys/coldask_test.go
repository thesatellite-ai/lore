//go:build journeys && !windows

package journeys

// coldask_test.go — devco:D-1026 (hunt H-290): the first `lore ask` of a
// session took seconds longer than the next ones. Every fetch was due
// (past live.fetch_ttl) and the ask made two network round trips one
// after the other: the org's link index fetched the store clone while the
// repo's org settings resolved, and only then was the code repo's
// refs/lore/* fetched. Over ssh to a far git host each is about a second.
// Now a read starts the code repo's fetch at once, so the two overlap.
//
// The fake ssh waits a moment on each call and logs how many calls were
// in flight at the end of that wait: two calls of one command that
// overlap see each other. Counts, never wall times.
//
//	day 1  online: an org store holds a note of the code repo, linked to it
//	day 2  over a slow link, after a pause: the first ask fetches the store
//	       and the code repo at the same time, once each, and answers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/fakeexec"
)

// overlapSSH is a fake ssh that marks itself in flight, waits a second (a
// far host), logs "<calls in flight> <command>", and runs the command
// (git-upload-pack is a fetch) here.
const overlapSSH = "#!/bin/sh\nfor a; do cmd=$a; done\nd=$FAKE_SSH_DIR\n: > \"$d/run.$$\"\nsleep 1\nn=$(ls \"$d\" | grep -c '^run\\.')\necho \"$n $cmd\" >> \"$d/log\"\nrm -f \"$d/run.$$\"\nexec sh -c \"$cmd\"\n"

func TestJourneyColdFirstAskFetchesAtOnce(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ivo := w.Persona("Ivo Brant", "ivo@acme-co.example")
	lap := ivo.Laptop()
	notes := w.Remote("github.com/acme-co/lore-notes")
	tools := jPlainRepo(t, lap, w.Remote("github.com/acme-co/tools"), filepath.Join(lap.Home, "work", "tools"))
	code := jPlainRepo(t, lap, w.Remote("github.com/acme-co/orbit"), filepath.Join(lap.Home, "work", "orbit"))
	sshDir := t.TempDir()
	// calls returns the fetches logged since the last call, and how many
	// of them saw another call in flight.
	var last string
	calls := func() (fetches, overlapped int) {
		b, _ := os.ReadFile(filepath.Join(sshDir, "log")) // no file yet: no call
		_ = os.Remove(filepath.Join(sshDir, "log"))
		last = string(b)
		for _, l := range strings.Split(strings.TrimSpace(last), "\n") {
			if !strings.Contains(l, "git-upload-pack") {
				continue
			}
			fetches++
			if !strings.HasPrefix(l, "1 ") {
				overlapped++
			}
		}
		return fetches, overlapped
	}

	w.Step("day1/a-note-in-a-team-store", func() {
		lap.LoreIn(tools, "store", "init", "--name", "notes", "--url", notes.SSHURL()).OK(t)
		lap.LoreIn(tools, "org", "create", "--org", "acme", "--store", notes.SSHURL(), "--own", "github.com/acme-co/*",
			"--key", "human:ivo@acme-co.example", "--name", "Ivo", "--yes").OK(t)
		lap.LoreIn(code, "init").OK(t)
		lap.LoreIn(code, "org", "link", "--org", "acme", "--project", "orbit", "--yes").OK(t)
		lap.LoreIn(code, "memory", "add", "--body", "The orbit release train leaves every second tuesday.", "--yes").OK(t)
		code.CommitAll("add lore")
		code.Git("push", "-q", "origin", "HEAD:main")
		lap.LoreIn(code, "sync").OK(t)
		// from now on every git call to the fake GitHub goes over the slow ssh
		bin := fakeexec.Write(t, filepath.Join(t.TempDir(), "ssh"), overlapSSH, 0o755)
		lap.SetEnv("GIT_SSH_COMMAND", bin)
		lap.SetEnv("GIT_SSH_VARIANT", "simple")
		lap.SetEnv("FAKE_SSH_DIR", sshDir)
		gc := filepath.Join(lap.Home, ".gitconfig")
		hosts := filepath.Join(w.Root, "hosts", "github.com")
		for _, args := range [][]string{
			{"--remove-section", "url." + hosts + "/"},
			{"--add", "url.ssh://slowhost" + hosts + "/.insteadOf", "https://github.com/"},
			{"--add", "url.ssh://slowhost" + hosts + "/.insteadOf", "git@github.com:"},
		} {
			if r := w.TryGit(lap.Home, append([]string{"config", "--file", gc}, args...)...); r.Code != 0 {
				t.Fatalf("slow link %v: %s", args, r.Stderr)
			}
		}
		lap.LoreIn(code, "sync").OK(t)
		if fe, _ := calls(); fe == 0 {
			t.Fatal("the fake ssh logged no fetch: the counts below would prove nothing")
		}
	})

	w.Step("day2/first-ask-fetches-store-and-repo-at-once", func() {
		w.AdvanceDays(1) // long past live.fetch_ttl: every fetch is due
		calls()          // the invariants' own reads after the last step
		r := lap.LoreIn(code, "ask", "--query", "when does the orbit release train leave").OK(t).Says(t, "every second tuesday")
		fe, over := calls()
		if fe != 2 {
			t.Errorf("the first ask of the day made %d fetch(es), want 2 (the team store and the code repo, once each):\n%s\n%s", fe, last, r.Out())
		}
		if over == 0 {
			t.Errorf("the first ask of the day fetched the team store and the code repo one after the other, want both at once:\n%s", last)
		}
		// the next ask within live.fetch_ttl makes no call at all
		lap.LoreIn(code, "ask", "--query", "when does the orbit release train leave").OK(t).Says(t, "every second tuesday")
		if fe, _ := calls(); fe != 0 {
			t.Errorf("an ask right after made %d fetch(es), want none:\n%s", fe, last)
		}
	})
}
