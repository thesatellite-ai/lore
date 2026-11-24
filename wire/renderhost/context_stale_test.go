package renderhost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/factstale"
	"github.com/thesatellite-ai/lore/store"
)

// TestContextPickMarksStaleFact (devco:D-953, bug hunt H-212): lore ask
// prints lore context --for-file's picks for a question naming a file; a
// pick stating a fact whose evidence file changed in git after it was
// verified carries the stale mark on its tag and its line, and the stale
// mark and the command that clears it in --json; a fresh pick none.
func TestContextPickMarksStaleFact(t *testing.T) {
	dir := t.TempDir()
	git := func(day string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+day+"T10:00:00Z", "GIT_COMMITTER_DATE="+day+"T10:00:00Z",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("2026-11-01", "init", "-q")
	write("billing/refund.go", "package billing\n")
	write("billing/card.go", "package billing\n")
	git("2026-11-01", "add", "-A")
	git("2026-11-01", "-c", "user.name=Ada", "-c", "user.email=ada@example.test", "commit", "-q", "-m", "billing")
	write("billing/refund.go", "package billing\n\n// no cap any more\n")
	git("2026-11-10", "add", "-A")
	git("2026-11-10", "-c", "user.name=Ada", "-c", "user.email=ada@example.test", "commit", "-q", "-m", "no cap")

	pick := func(id, evidence string, src store.Source) contextPick {
		cl, ok := factstale.ClaimOfItems([]string{evidence}, "2026-11-02")
		if !ok {
			t.Fatal("no claim")
		}
		full := "<memory id=\"" + id + "\" why=\"words\">\nbody\n</memory>\n\n"
		return contextPick{ID: id, Kind: "memory", Why: "words", full: full, line: "- `" + id + "` body\n", Bytes: len(full), fact: &cl, source: src}
	}
	picks := []contextPick{pick("mem_stale", "billing/refund.go:3", "kb/app"), pick("mem_fresh", "billing/card.go:1", store.SourceRepo)}
	checker := func(context.Context) *factstale.Checker {
		return factstale.NewChecker(func(ctx context.Context, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, "git", args...)
			cmd.Dir = dir
			return cmd.Output()
		})
	}
	markStale(context.Background(), picks, checker, time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC))

	s, f := picks[0], picks[1]
	note := "stale: evidence changed since 2026-11-02 (billing/refund.go)"
	if s.Stale != note || !strings.Contains(s.full, `why="words" stale="evidence changed since 2026-11-02 (billing/refund.go)">`) ||
		!strings.HasSuffix(s.line, "  ["+note+"]\n") || s.Bytes != len(s.full) {
		t.Fatalf("stale pick not marked: %+v", s)
	}
	if want := "lore meta set --id mem_stale --store kb/app --pair fact_verified_at=2026-11-24"; !strings.Contains(s.Reverify, want) {
		t.Fatalf("reverify %q misses %q", s.Reverify, want)
	}
	if f.Stale != "" || f.Reverify != "" || strings.Contains(f.full, "stale=") {
		t.Fatalf("fresh pick marked: %+v", f)
	}
}

// TestContextPicksAskGitOnce (bug hunt H-209, devco:D-950): lore context
// (and so lore ask for a question naming a file) checks every pick's
// evidence with one git log, not one per pick, and still marks each one.
func TestContextPicksAskGitOnce(t *testing.T) {
	var picks []contextPick
	for i := range 30 {
		cl, ok := factstale.ClaimOfItems([]string{fmt.Sprintf("svc/f%02d.go:1", i)}, "2026-11-02")
		if !ok {
			t.Fatal("no claim")
		}
		id := fmt.Sprintf("mem_%02d", i)
		full := "<memory id=\"" + id + "\">\nbody\n</memory>\n\n"
		picks = append(picks, contextPick{ID: id, full: full, line: "- body\n", fact: &cl, source: store.SourceRepo})
	}
	calls := 0
	checker := func(context.Context) *factstale.Checker {
		// every path answers "changed on 2026-11-10" in Prime's -z format
		return factstale.NewChecker(func(_ context.Context, args ...string) ([]byte, error) {
			calls++
			var out strings.Builder
			for i, a := range args {
				if a == "--" {
					for _, p := range args[i+1:] {
						out.WriteString("\x01" + "1234567890abcdef1234567890abcdef12345678\t1794304800\x00\n" + p + "\x00\x00")
					}
				}
			}
			if out.Len() == 0 {
				return []byte("1234567890abcdef1234567890abcdef12345678\t1794304800\n"), nil
			}
			return []byte(out.String()), nil
		})
	}
	markStale(context.Background(), picks, checker, time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC))
	if calls != 1 {
		t.Fatalf("git ran %d times for %d picks, want 1", calls, len(picks))
	}
	for _, p := range picks {
		if !strings.HasPrefix(p.Stale, "stale: evidence changed since 2026-11-02") {
			t.Fatalf("%s not marked: %q", p.ID, p.Stale)
		}
	}
}
