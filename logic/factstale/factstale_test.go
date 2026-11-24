package factstale_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/factstale"
)

// repo is a throwaway git repository whose commits carry chosen dates.
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	r := repo{t: t, dir: t.TempDir()}
	r.git("", "init", "-q", "-b", "main")
	return r
}

// git runs git in the repo; day (YYYY-MM-DD) dates a commit.
func (r repo) git(day string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	env := []string{"GIT_CONFIG_NOSYSTEM=1", "HOME=" + r.dir, "GIT_AUTHOR_NAME=Ann", "GIT_AUTHOR_EMAIL=ann@example.test", "GIT_COMMITTER_NAME=Ann", "GIT_COMMITTER_EMAIL=ann@example.test"}
	if day != "" {
		env = append(env, "GIT_AUTHOR_DATE="+day+"T10:00:00Z", "GIT_COMMITTER_DATE="+day+"T10:00:00Z")
	}
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes files and commits them on day.
func (r repo) commit(day string, files map[string]string) string {
	r.t.Helper()
	for p, body := range files {
		full := filepath.Join(r.dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	r.git("", "add", "-A")
	r.git(day, "commit", "-qm", "change on "+day)
	return r.git("", "rev-parse", "--short", "HEAD")
}

func (r repo) checker() *factstale.Checker {
	return factstale.NewChecker(func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = r.dir
		return cmd.Output()
	})
}

func claim(t *testing.T, verified string, evidence ...string) factstale.Claim {
	t.Helper()
	c, ok := factstale.ClaimOfItems(evidence, verified)
	if !ok {
		t.Fatalf("no claim from %v", evidence)
	}
	return c
}

// TestStaleWhenEvidenceChangedAfterVerified (devco:D-941, H-190): a fact
// verified on a day is stale once a file it names changed in git on a later
// day, fresh when its files did not change, never stale when it was never
// verified (path evidence) or names nothing git knows; a commit item is
// stale when one of the commit's files changed after it, and the verified
// day moves that line forward.
func TestStaleWhenEvidenceChangedAfterVerified(t *testing.T) {
	r := newRepo(t)
	first := r.commit("2026-11-01", map[string]string{"billing/refund.go": "a\n", "billing/card.go": "b\n", "docs/notes.md": "n\n"})
	r.commit("2026-11-10", map[string]string{"billing/refund.go": "a2\n"})
	ctx := context.Background()
	chk := r.checker()

	cases := []struct {
		name    string
		c       factstale.Claim
		stale   bool
		changed []string
	}{
		{"changed after the verified day", claim(t, "2026-11-02", "billing/refund.go:3"), true, []string{"billing/refund.go"}},
		{"changed on the verified day counts as checked", claim(t, "2026-11-10", "billing/refund.go:3"), false, nil},
		{"unchanged file", claim(t, "2026-11-02", "billing/card.go:1-2"), false, nil},
		{"one of two items changed", claim(t, "2026-11-02", "billing/card.go", "./billing/refund.go:9"), true, []string{"billing/refund.go"}},
		{"datetime verified before the change", claim(t, "2026-11-10T09:00:00Z", "billing/refund.go"), true, []string{"billing/refund.go"}},
		{"never verified: path not checked", claim(t, "", "billing/refund.go:3"), false, nil},
		{"path outside the repo history", claim(t, "2026-11-02", "nothere/x.go:1"), false, nil},
		{"commit whose file changed after it", claim(t, "", first), true, []string{"billing/refund.go"}},
		{"commit verified after the change", claim(t, "2026-11-11", first), false, nil},
		{"unknown commit", claim(t, "", "abcdef1234567"), false, nil},
	}
	for _, tc := range cases {
		v := chk.Check(ctx, tc.c)
		var got []string
		for _, ch := range v.Changed {
			got = append(got, ch.Path)
		}
		if v.Stale != tc.stale || !slices.Equal(got, tc.changed) {
			t.Errorf("%s: stale=%v changed=%v, want stale=%v changed=%v", tc.name, v.Stale, got, tc.stale, tc.changed)
		}
		if tc.stale && !strings.HasPrefix(v.Note(), "stale: evidence changed since ") {
			t.Errorf("%s: note %q", tc.name, v.Note())
		}
		if !tc.stale && v.Note() != "" {
			t.Errorf("%s: a fresh fact has note %q", tc.name, v.Note())
		}
	}
}

// TestParseEvidenceRefusesUnsafePaths: an absolute path, a `..` step or an
// option-like item never reaches git; a sha is a commit item.
func TestParseEvidenceRefusesUnsafePaths(t *testing.T) {
	ev, bad := factstale.ParseEvidence([]string{"/etc/passwd:1", "../up.go", "a/../../b.go", "--output=x", "C:/x.go", "app/main.go:12", "3F9C2AB", " "})
	if want := []string{"/etc/passwd:1", "../up.go", "a/../../b.go", "--output=x", "C:/x.go"}; !slices.Equal(bad, want) {
		t.Fatalf("bad = %v, want %v", bad, want)
	}
	if len(ev) != 2 || ev[0].Kind != factstale.KindPath || ev[0].Path != "app/main.go" || ev[0].Line != 12 || ev[1].Kind != factstale.KindCommit || ev[1].Commit != "3f9c2ab" {
		t.Fatalf("evidence = %+v", ev)
	}
}

// TestCheckerAsksGitOncePerItem: the cache answers a second claim naming
// the same file (one git log per evidence item per command).
func TestCheckerAsksGitOncePerItem(t *testing.T) {
	calls := 0
	chk := factstale.NewChecker(func(context.Context, ...string) ([]byte, error) {
		calls++
		return []byte("1234567890abcdef\t1794304800\n"), nil
	})
	for range 3 {
		if v := chk.Check(context.Background(), claim(t, "2026-11-02", "a.go:1")); !v.Stale {
			t.Fatal("want stale")
		}
	}
	if calls != 1 {
		t.Fatalf("git ran %d times, want 1", calls)
	}
}

// TestReverify (devco:D-953, bug hunt H-212): the command lint, show, ask
// and context print for a stale fact: --store only for a row outside the
// repo store, today's day, or the newest change's day when the clock is
// behind it (so it always clears the verdict); nothing for a fresh one.
func TestReverify(t *testing.T) {
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	stale := factstale.Verdict{Stale: true, Since: "2026-11-02", Changed: []factstale.Change{{Path: "a.go", Commit: "abc1234", At: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)}}}
	for _, c := range []struct {
		name, store string
		v           factstale.Verdict
		now         time.Time
		want        string
	}{
		{"fresh", "", factstale.Verdict{}, now, ""},
		{"repo store", "", stale, now, "when it still holds: lore meta set --id mem_1 --pair fact_verified_at=2026-11-24; when it does not: edit the row and its fact_evidence"},
		{"a store folder", "kb/app", stale, now, "when it still holds: lore meta set --id mem_1 --store kb/app --pair fact_verified_at=2026-11-24; when it does not: edit the row and its fact_evidence"},
		{"clock behind", "", stale, time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), "when it still holds: lore meta set --id mem_1 --pair fact_verified_at=2026-11-10; when it does not: edit the row and its fact_evidence"},
	} {
		if got := factstale.Reverify("mem_1", c.store, c.v, c.now); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestPrimeAsksGitOnceForEveryPath (bug hunt H-209, devco:D-950): a command
// checking many rows primes the checker first, so all their path items
// cost one git log instead of one each (one per item made lore ask take
// ~15 s on a store whose every row names its evidence), and the verdicts
// are exactly those of the per-item checks: a folder item, a file name
// with a space, a change on the verified day, a path git never had, a
// commit item and an unknown commit.
func TestPrimeAsksGitOnceForEveryPath(t *testing.T) {
	r := newRepo(t)
	first := r.commit("2026-11-01", map[string]string{"billing/refund.go": "a\n", "billing/card.go": "b\n", "docs/my notes.md": "n\n", "svc/a/x.go": "x\n"})
	r.commit("2026-11-10", map[string]string{"billing/refund.go": "a2\n", "docs/my notes.md": "n2\n"})
	r.commit("2026-11-12", map[string]string{"svc/a/x.go": "x2\n"})
	claims := []factstale.Claim{
		claim(t, "2026-11-02", "billing/refund.go:3"),
		claim(t, "2026-11-10", "billing/refund.go:3"),
		claim(t, "2026-11-02", "billing/card.go:1"),
		claim(t, "2026-11-02", "docs/my notes.md"),
		claim(t, "2026-11-11", "svc/a"),
		claim(t, "2026-11-12", "svc"),
		claim(t, "2026-11-02", "nothere/x.go:1"),
		claim(t, "2026-11-05", "billing/card.go", "./billing/refund.go:9"),
		claim(t, "", first),
		claim(t, "2026-11-11", first),
	}
	for i := range 40 {
		claims = append(claims, claim(t, "2026-11-02", "gen/f"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+".go:1"))
	}
	ctx := context.Background()
	want := make([]factstale.Verdict, len(claims))
	plain := r.checker()
	for i, c := range claims {
		want[i] = plain.Check(ctx, c)
	}
	calls := 0
	primed := factstale.NewChecker(func(ctx context.Context, args ...string) ([]byte, error) {
		calls++
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = r.dir
		return cmd.Output()
	})
	primed.Prime(ctx, claims)
	for i, c := range claims {
		got := primed.Check(ctx, c)
		if got.Stale != want[i].Stale || got.Note() != want[i].Note() || !slices.Equal(got.Changed, want[i].Changed) {
			t.Errorf("claim %d (%v): primed %+v, per item %+v", i, c.Evidence, got, want[i])
		}
	}
	if !want[0].Stale || want[1].Stale || !want[3].Stale || !want[4].Stale || want[5].Stale || !want[8].Stale {
		t.Fatalf("the per-item verdicts this test relies on changed: %+v", want)
	}
	// one git log for the paths, one for the commit item's files, one for
	// the commit's later changes: never one per path
	if calls > 3 {
		t.Fatalf("git ran %d times for %d claims, want at most 3", calls, len(claims))
	}
}
