package orgcfg

import (
	"fmt"
	"strings"
	"testing"
)

// TestGitMemoStaysBounded (devco:D-845): every commit makes new immutable
// reads, so a long-lived process (the local read server) kept every
// answer it ever read. The memo holds at most gitMemoMaxBytes and still
// answers what it holds. Not parallel: the memo is process state.
func TestGitMemoStaysBounded(t *testing.T) {
	gitMemo.mu.Lock()
	saved, savedBytes := gitMemo.runs, gitMemo.bytes
	gitMemo.runs, gitMemo.bytes = map[string]string{}, 0
	gitMemo.mu.Unlock()
	t.Cleanup(func() {
		gitMemo.mu.Lock()
		gitMemo.runs, gitMemo.bytes = saved, savedBytes
		gitMemo.mu.Unlock()
	})
	answer := strings.Repeat("x", 1<<20)
	argsOf := func(i int) []string {
		return []string{"-C", "/work/acme", "cat-file", "blob", fmt.Sprintf("%040x:_meta.json", i)}
	}
	const reads = 3 * gitMemoMaxBytes / (1 << 20)
	for i := range reads {
		keepRun(argsOf(i), answer)
		gitMemo.mu.Lock()
		held, n := gitMemo.bytes, len(gitMemo.runs)
		gitMemo.mu.Unlock()
		if held > gitMemoMaxBytes || n*len(answer) > gitMemoMaxBytes {
			t.Fatalf("after %d reads the memo holds %d answers (%d bytes), over its bound of %d bytes", i+1, n, held, gitMemoMaxBytes)
		}
	}
	if out, ok := memoRun(argsOf(reads - 1)); !ok || out != answer {
		t.Fatal("the memo lost the answer it kept last")
	}
}
