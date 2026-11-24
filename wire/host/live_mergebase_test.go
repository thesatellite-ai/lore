package host

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/internal/testenv"
)

// countingGit is gitref.Exec that keeps every invocation's arguments.
type countingGit struct {
	mu   sync.Mutex
	runs [][]string
}

// Run implements gitref.Git.
func (c *countingGit) Run(ctx context.Context, inv gitref.Invocation) ([]byte, error) {
	c.mu.Lock()
	c.runs = append(c.runs, inv.Args)
	c.mu.Unlock()
	return gitref.Exec{}.Run(ctx, inv)
}

// mergeBases is the merge-base invocations run so far.
func (c *countingGit) mergeBases() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out [][]string
	for _, a := range c.runs {
		if len(a) > 0 && a[0] == "merge-base" {
			out = append(out, a)
		}
	}
	return out
}

// TestMergeBaseOfAMissingRefRunsNoMergeBase (bug hunt H-215, devco:D-956):
// in a repo with no fetched default branch (no remote at all) the merge
// base is "" without a `git merge-base` that can only fail: a warm lore ask
// ran one on every call. With the branch fetched, the merge base is asked
// of the two commit ids, an answer the in-process git reader keeps.
func TestMergeBaseOfAMissingRefRunsNoMergeBase(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	ctx := context.Background()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "two")
	g := &countingGit{}
	repo, err := gitref.Open(ctx, dir, g)
	if err != nil {
		t.Fatal(err)
	}
	s := &liveSession{repo: repo, top: dir, h: &liveHost{}}
	const remote = "refs/remotes/origin/main"
	if base := s.mergeBase(ctx, remote); base != "" {
		t.Fatalf("merge base with no %s = %q, want none", remote, base)
	}
	if mb := g.mergeBases(); len(mb) != 0 {
		t.Fatalf("a missing ref still ran git %q", mb)
	}
	raw, err := gitref.Exec{}.Run(ctx, gitref.Invocation{Dir: dir, Args: []string{"rev-parse", "HEAD~1"}})
	if err != nil {
		t.Fatal(err)
	}
	one := strings.TrimSpace(string(raw))
	gitT(t, dir, "update-ref", remote, one)
	if base := s.mergeBase(ctx, remote); base != one {
		t.Fatalf("merge base = %q, want %q", base, one)
	}
	mb := g.mergeBases()
	if len(mb) != 1 || len(mb[0]) != 3 || mb[0][2] != one || len(mb[0][1]) != len(one) {
		t.Fatalf("merge-base ran as %q, want once over two commit ids", mb)
	}
}
