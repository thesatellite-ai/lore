//go:build journeys && !windows

package journeys

// readserver_test.go — devco:D-833 (bug hunt H-90): a warm `lore ask`
// could not answer in under 50 ms one-shot, so a local read server runs
// reads with its caches open. It runs each command in the caller's folder
// with the caller's environment: an ask from inside a code repo (the repo
// changes what a read covers), with --json, and a search must answer as
// the one-shot process does, and a write is never handed to it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyReadServerAnswersInTheCallersFolder(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	uma := w.Persona("Uma Reyes", "uma@acme-co.example")
	lap := uma.Laptop()
	me := w.Remote("github.com/uma-reyes/lore-me")
	code := lap.Clone(w.Remote("github.com/acme-co/comet"), "comet")
	w.Step("day1/notes-and-a-repo", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		lap.Lore(lap.Home, "store", "folder", "add", "me", "work").OK(t)
		lap.Lore(lap.Home, "memory", "add", "--body", "Status update comet: beta with 4 teams.", "--to", "me/work", "--yes").OK(t)
		lap.LoreIn(code, "init").OK(t)
		lap.LoreIn(code, "memory", "add", "--body", "comet builds with make release, never make all.").OK(t)
	})
	w.Step("day1/served-reads-answer-as-one-shot", func() {
		calls := [][]string{
			{"ask", "--store", "me", "--query", "what is the status of comet"},
			{"ask", "--store", "me", "--query", "what is the status of comet", "--json"},
			{"ask", "--query", "how does comet build"},
			{"search", "comet"},
			{"memory", "list"},
		}
		in := func(args []string) teamharness.Result { return lap.LoreIn(code, args...) }
		oneShot := make([]teamharness.Result, len(calls))
		for i, c := range calls {
			oneShot[i] = in(c)
		}
		log := filepath.Join(t.TempDir(), "read-server.log")
		sock := lap.LorePath("run", "read.sock")
		lap.SetEnv("LORE_READ_SERVER", "1")
		lap.SetEnv("LORE_TEST_READ_SERVER_LOG", log)
		defer func() {
			lap.SetEnv("LORE_READ_SERVER", "")
			lap.SetEnv("LORE_TEST_READ_SERVER_LOG", "")
			_ = os.Remove(sock) // the server stops once its socket is gone
		}()
		in(calls[0]) // starts the server
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(sock); err == nil {
				break
			}
			if time.Now().After(deadline) {
				b, _ := os.ReadFile(log)
				ents, _ := os.ReadDir(filepath.Dir(sock))
				names := []string{}
				for _, e := range ents {
					names = append(names, e.Name())
				}
				t.Fatalf("no read server listens on %s (run/ holds %v; the CLI logged %q)", sock, names, b)
			}
		}
		_ = os.Remove(log)
		for i, c := range calls {
			got := in(c)
			if got.Stdout != oneShot[i].Stdout || got.Stderr != oneShot[i].Stderr || got.Code != oneShot[i].Code {
				t.Errorf("served `lore %s` differs from one-shot:\nserved: %s\none-shot: %s", strings.Join(c, " "), got, oneShot[i])
			}
		}
		lap.LoreIn(code, "memory", "add", "--body", "comet ships on fridays only.").OK(t) // a write: never offered
		if b, _ := os.ReadFile(log); strings.Count(string(b), "served\n") != len(calls) || strings.Contains(string(b), "one-shot") {
			t.Errorf("want each read served and the write not offered, the log says:\n%s", b)
		}
		lap.LoreIn(code, "search", "fridays").OK(t).Says(t, "comet ships on fridays only")
	})
}
