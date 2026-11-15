package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestPreCommitRefusesARowFileDeletedByHand (devco S-8): an agent's `rm
// .lore/data/rules/<id>.json` and `git commit -a` dropped a must rule from
// every clone and from LORE.md with no trash copy and no audit entry, and
// doctor said nothing. Now the pre-commit hook refuses the commit, naming
// the rule (kind, summary, id, file) with `git checkout HEAD -- <file>` and
// `lore rule delete`, and leaves the index as it was; doctor warns about
// the same row; a row file renamed (its id still in the commit) and
// LORE_ALLOW_ROW_DROP=1 go through.
func TestPreCommitRefusesARowFileDeletedByHand(t *testing.T) {
	f := newLiveFixture(t)
	f.s.release()
	top := f.s.top
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(top, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const rule = ".lore/data/rules/rul_01a1dd52216a707387008bfd35ae45ba.json"
	const memo = ".lore/data/memories/mem_01a1dd5c74427dec80871af648c3aaef.json"
	write(rule, `{"_v":2,"_table":"rules","id":"rul_01a1dd52216a707387008bfd35ae45ba","severity":"must","summary":"tests green before push","body":"Run the full test suite before every push"}`+"\n")
	write(memo, `{"_v":2,"_table":"memories","id":"mem_01a1dd5c74427dec80871af648c3aaef","body":"retry the flaky job once"}`+"\n")
	write("README.md", "# demo\n")
	gitT(t, top, "add", "-A")
	gitT(t, top, "commit", "-q", "--no-verify", "-m", "rows")
	t.Chdir(top)
	ctx := context.Background()

	if err := os.Remove(filepath.Join(top, filepath.FromSlash(rule))); err != nil {
		t.Fatal(err)
	}
	gitT(t, top, "add", "-A") // git commit -a stages the deletion before the hook runs
	err := f.s.h.preCommit(ctx)
	if !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("a rule file removed by hand was committed: %v", err)
	}
	var e *errs.Error
	msg := err.Error()
	if errors.As(err, &e) {
		msg += "\n" + e.Hint
	}
	for _, want := range []string{`must rule "tests green before push"`, "rul_01a1dd52216a707387008bfd35ae45ba", "git checkout HEAD -- " + rule, "lore rule delete --id rul_01a1dd52216a707387008bfd35ae45ba --yes", EnvAllowRowDrop + "=1"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal lacks %q:\n%s", want, msg)
		}
	}
	base, derr := liveDoctor{h: f.s.h}.Base(f.ctx, false)
	if derr != nil {
		t.Fatal(derr)
	}
	if !strings.Contains(strings.Join(base.Warnings, "\n"), "rul_01a1dd52216a707387008bfd35ae45ba") {
		t.Fatalf("doctor does not name the row deleted by hand: %q", base.Warnings)
	}
	t.Setenv(EnvAllowRowDrop, envAllowOn)
	if err := f.s.h.preCommit(ctx); err != nil {
		t.Fatalf("%s=1 must let the deletion through: %v", EnvAllowRowDrop, err)
	}
	t.Setenv(EnvAllowRowDrop, "")
	gitT(t, top, "checkout", "-q", "HEAD", "--", rule)

	// a rename keeps the row: the id is still in the commit
	moved := ".lore/data/memories/mem_01a1dd5c74427dec80871af648c3aaef.json"
	if err := os.MkdirAll(filepath.Join(top, ".lore", "data", "memories", "x"), 0o750); err != nil {
		t.Fatal(err)
	}
	gitT(t, top, "mv", memo, strings.Replace(moved, "memories/", "memories/x/", 1))
	if err := f.s.h.preCommit(ctx); err != nil {
		t.Fatalf("a renamed row file is not a deletion: %v", err)
	}
	out, _ := (gitref.Exec{}).Run(ctx, gitref.Invocation{Dir: top, Args: []string{"status", "--porcelain"}})
	if strings.Contains(string(out), "D  "+rule) {
		t.Fatalf("the rule is still staged as deleted:\n%s", out)
	}
}
