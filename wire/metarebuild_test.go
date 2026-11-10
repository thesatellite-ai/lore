package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestKeyLoosenings: a staged key row that drops internal without a new
// changed_by entry is E_LEAK_BLOCKED (RX7); the same drop with one passes.
func TestKeyLoosenings(t *testing.T) {
	ctx := context.Background()
	const rel = ".lore/data/meta_keys/mky_x.json"
	head := `{"name":"margin_pct","internal":true}`
	cases := []struct {
		name, staged string
		blocked      bool
	}{
		{"no record", `{"name":"margin_pct","internal":false}`, true},
		{"recorded", `{"name":"margin_pct","internal":false,"changed_by":[{"id":"the_1","field":"internal","to":false,"at":"x"}]}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("GIT_AUTHOR_NAME", "t")
			t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
			t.Setenv("GIT_COMMITTER_NAME", "t")
			t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
			git := func(args ...string) {
				t.Helper()
				if _, err := liveGit(ctx, dir, args...); err != nil {
					t.Fatal(err)
				}
			}
			write := func(s string) {
				t.Helper()
				p := filepath.Join(dir, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			git("init", "-q", "-b", "main")
			write(head)
			git("add", "-A")
			git("commit", "-q", "--no-verify", "-m", "one")
			write(c.staged)
			git("add", "-A")
			found, err := KeyLoosenings(ctx, dir, "HEAD", "")
			if err != nil {
				t.Fatal(err)
			}
			blocked := len(found) == 1 && errs.Has(found[0], errs.LeakBlocked)
			if blocked != c.blocked || (!c.blocked && len(found) > 0) {
				t.Fatalf("found %v, want blocked=%v", found, c.blocked)
			}
			// the landing form (merge base..HEAD) agrees once committed
			git("commit", "-q", "--no-verify", "-m", "two")
			since, err := KeyLooseningsSince(ctx, dir, "HEAD~1")
			if err != nil {
				t.Fatal(err)
			}
			if (len(since) > 0) != c.blocked {
				t.Fatalf("since: %v, want blocked=%v", since, c.blocked)
			}
		})
	}
}
