//go:build journeys && !windows

package journeys

// livetask_clean_test.go — a live `lore task add` leaves the working folder
// alone (devco:K-6; team/getting-started.md step 4): the committed actor
// file of the writer is not rewritten behind the person's back, even when
// it no longer matches what this lore would write (a teammate's lore
// trimmed or stamped it differently). The row goes to refs/lore/live only.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyLiveTaskKeepsWorkingFolder(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	pia := w.Persona("Pia Park", "pia@widgets.example")
	remote := w.Remote("github.com/widgets/livetask")
	var a *teamharness.Clone

	w.Step("day1/a-committed-actor-file-another-lore-wrote", func() {
		a = pia.Laptop().Clone(remote, "livetask")
		a.WriteFile("README.md", "livetask\n")
		a.CommitAll("init")
		a.Git("push", "-q", "origin", "HEAD:main")
		pia.Laptop().LoreIn(a, "init", "--non-interactive", "--name=livetask").OK(t)
		pia.Laptop().LoreIn(a, "memory", "add", "--body", "Memory from pia").OK(t)
		a.CommitAll("lore")
		files := strings.Fields(a.Git("ls-files", ".lore/data/actors"))
		if len(files) == 0 {
			t.Fatal("no actor file committed")
		}
		// another lore wrote the copy with a different display name
		for _, f := range files {
			body, _ := a.ReadFile(f)
			var doc map[string]json.RawMessage
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			doc["display_name"] = json.RawMessage(`"P. Park"`)
			out, _ := json.MarshalIndent(doc, "", "  ")
			a.WriteFile(f, string(out)+"\n")
		}
		a.Git("add", "-A")
		a.Git("commit", "-q", "--no-verify", "-m", "actor copy from another lore")
		a.Git("push", "-q", "origin", "HEAD:main")
		if st := a.Status(); st != "" {
			t.Fatalf("not clean before the live write:\n%s", st)
		}
	})

	w.Step("day1/a-live-task-add-changes-no-file", func() {
		pia.Laptop().LoreIn(a, "task", "list").OK(t)
		pia.Laptop().LoreIn(a, "memory", "list").OK(t)
		if st := a.Status(); st != "" {
			t.Fatalf("reads changed files:\n%s", st)
		}
		r := pia.Laptop().LoreIn(a, "task", "add", "Probe live task").OK(t)
		r.Never(t, "wrote "+filepath.Join(a.Dir, ".lore", "data", "actors"))
		if st := a.Status(); st != "" {
			t.Fatalf("a live task add changed the working folder:\n%s", st)
		}
		pia.Laptop().LoreIn(a, "task", "list").OK(t).Says(t, "Probe live task")
	})
}
