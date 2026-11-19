package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
)

// TestPreCommitTakesRowsAdoptMovedToAStore (devco:D-710): `lore store
// adopt --remove-source` moved a repo's rows into a store and deleted
// their row files; lore's own pre-commit then refused the commit ("deletes
// 204 lore rows by removing the file, not with lore"). Now the files adopt
// recorded (wire.RecordMovedOut) commit with one line naming the store,
// doctor notes them instead of warning, a file removed by hand next to
// them is still refused, and once the removal is committed the marker
// lets go of the path: deleting the same file by hand later is refused.
func TestPreCommitTakesRowsAdoptMovedToAStore(t *testing.T) {
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
	remove := func(rel string) {
		t.Helper()
		if err := os.Remove(filepath.Join(top, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	const rule = ".lore/data/rules/rul_01a1dd52216a707387008bfd35ae45ba.json"
	const memo = ".lore/data/memories/mem_01a1dd5c74427dec80871af648c3aaef.json"
	write(rule, `{"_v":2,"_table":"rules","id":"rul_01a1dd52216a707387008bfd35ae45ba","severity":"must","summary":"tests green before push","body":"Run the full test suite before every push"}`+"\n")
	write(memo, `{"_v":2,"_table":"memories","id":"mem_01a1dd5c74427dec80871af648c3aaef","body":"retry the flaky job once"}`+"\n")
	gitT(t, top, "add", "-A")
	gitT(t, top, "commit", "-q", "--no-verify", "-m", "rows")
	t.Chdir(top)
	ctx := context.Background()

	// adopt moved the rule to acme/client_a; the memory went by hand
	remove(rule)
	if err := wire.RecordMovedOut(ctx, top, "acme/client_a", []string{rule}); err != nil {
		t.Fatal(err)
	}
	remove(memo)
	gitT(t, top, "add", "-A")
	err := f.s.h.preCommit(ctx)
	if !errs.Has(err, errs.NeedsConfirm) || !strings.Contains(err.Error(), "mem_01a1dd5c74427dec80871af648c3aaef") || strings.Contains(err.Error(), "rul_01a1dd52216a707387008bfd35ae45ba") {
		t.Fatalf("want the hand-deleted memory refused and the moved rule left out: %v", err)
	}
	gitT(t, top, "checkout", "-q", "HEAD", "--", memo)

	// only the moved rule: doctor notes it, the commit takes it
	base, derr := liveDoctor{h: f.s.h}.Base(f.ctx, false)
	if derr != nil {
		t.Fatal(derr)
	}
	if w := strings.Join(base.Warnings, "\n"); strings.Contains(w, "by removing the file") {
		t.Fatalf("doctor warns about a row adopt moved: %s", w)
	}
	if n := strings.Join(base.Notes, "\n"); !strings.Contains(n, "live in acme/client_a now") || !strings.Contains(n, "commit -m") {
		t.Fatalf("doctor does not say where the row went and how to commit it: %q", base.Notes)
	}
	out, _ := f.s.h.cfg.Stderr.(*strings.Builder)
	out.Reset()
	if err := f.s.h.preCommit(ctx); err != nil {
		t.Fatalf("lore refused the removal its own adopt made: %v", err)
	}
	if !strings.Contains(out.String(), "moved to acme/client_a") {
		t.Fatalf("the commit does not say where the row went: %q", out.String())
	}
	gitT(t, top, "commit", "-q", "--no-verify", "-m", "lore: move the notes to acme/client_a")

	// the path comes back and is removed by hand later: refused again
	write(rule, `{"_v":2,"_table":"rules","id":"rul_01a1dd52216a707387008bfd35ae45ba","severity":"must","summary":"tests green before push","body":"Run the full test suite before every push"}`+"\n")
	gitT(t, top, "add", "-A")
	if err := f.s.h.preCommit(ctx); err != nil {
		t.Fatal(err)
	}
	gitT(t, top, "commit", "-q", "--no-verify", "-m", "back")
	remove(rule)
	gitT(t, top, "add", "-A")
	if err := f.s.h.preCommit(ctx); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("a later hand delete of a path adopt once moved was committed: %v", err)
	}
}
