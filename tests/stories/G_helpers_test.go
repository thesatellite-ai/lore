//go:build stories && !windows

package stories

// G_helpers_test.go — what story G (months of use) needs on top of the
// shared harness: a small team on one code repo, bulk writes the way a
// person's day adds them, counting rows through --json, and the "git pull
// --rebase leaves nothing behind" check every pull of the story makes.
// Every name, email and text is made up; accents are on purpose.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// gMember is one person of story G with their clone of the code repo.
type gMember struct {
	Name, Email string
	M           *journeys.Machine
	C           *teamharness.Clone
}

// gCount is how many rows a list --json answer holds (its data array).
func gCount(t testing.TB, r teamharness.Result) int {
	t.Helper()
	var out struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("not a --json list (%v): %s", err, r)
	}
	return len(out.Data)
}

// gVolume is how much one person writes over the story's "months": scaled
// down from the round-3 run (500 tasks, 200 memories, 50 decisions, 30
// plans by five people) so the story stays inside the gate's budget; the
// shapes (many writers, many kinds, accents, emoji) are the same.
type gVolume struct{ Tasks, Memories, Decisions, Plans int }

// gWrite has p write v's rows, each title and body unique to p, plus the
// extra memory bodies given (the accent and emoji texts search must find).
func gWrite(t testing.TB, p gMember, v gVolume, extra ...string) {
	t.Helper()
	who := strings.Fields(p.Name)[0]
	for i := 0; i < v.Tasks; i++ {
		p.M.LoreIn(p.C, "task", "add", "--title", fmt.Sprintf("%s task %02d: tidy the handler", who, i), "--due", fmt.Sprintf("in %dd", 10+i*3)).OK(t)
	}
	for i := 0; i < v.Memories; i++ {
		p.M.LoreIn(p.C, "memory", "add", fmt.Sprintf("%s memory %02d: the queue drains in order", who, i)).OK(t)
	}
	for _, b := range extra {
		p.M.LoreIn(p.C, "memory", "add", b).OK(t)
	}
	for i := 0; i < v.Decisions; i++ {
		p.M.LoreIn(p.C, "decision", "add", "--title", fmt.Sprintf("%s decision %02d", who, i), "--body", "keep the retries bounded").OK(t)
	}
	for i := 0; i < v.Plans; i++ {
		p.M.LoreIn(p.C, "plan", "add", "--title", fmt.Sprintf("%s plan %02d", who, i), "--body", "phase one, then phase two").OK(t)
	}
}

// gShare commits p's rows and brings them to main the way a person does:
// commit, git pull --rebase, git push. D-132: the pull leaves the tree
// clean (no LORE.md left modified), so the next pull works too.
func gShare(t testing.TB, p gMember, msg string) {
	t.Helper()
	p.C.CommitAll(msg)
	gPull(t, p)
	p.C.Git("push", "-q", "origin", "HEAD:main")
}

// gPull runs git pull --rebase and fails when it leaves anything behind.
func gPull(t testing.TB, p gMember) {
	t.Helper()
	p.C.Git("pull", "-q", "--rebase", "origin", "main")
	if st := p.C.Git("status", "--porcelain"); st != "" {
		t.Fatalf("%s: git pull --rebase left the tree modified (the next pull refuses):\n%s", p.Name, st)
	}
}

// gMemID matches the memory id memory add prints.
var gMemID = regexp.MustCompile(`mem_[0-9a-f]{32}`)
