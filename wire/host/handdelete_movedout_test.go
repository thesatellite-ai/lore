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
	if err := wire.RecordMovedOut(ctx, top, "acme/client_a", wire.MovedByAdopt, []string{rule}); err != nil {
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

// TestPreCommitNamesTheCommandThatMovedRows (devco:D-736): the pre-commit
// and doctor lines for rows lore removed name the command that removed
// them (`lore doctor --fix`, `lore move`), not always `lore store adopt
// --remove-source`; a marker an older lore wrote (no command recorded)
// still reads as adopt's.
func TestPreCommitNamesTheCommandThatMovedRows(t *testing.T) {
	f := newLiveFixture(t)
	f.s.release()
	top := f.s.top
	const actor = ".lore/data/actors/act_cb98f8b36b637507a4cfe91eca849f00.json"
	const memo = ".lore/data/memories/mem_01a1dd5c74427dec80871af648c3aaef.json"
	for rel, body := range map[string]string{
		actor: `{"_v":2,"_table":"actors","id":"act_cb98f8b36b637507a4cfe91eca849f00","name":"Lee"}`,
		memo:  `{"_v":2,"_table":"memories","id":"mem_01a1dd5c74427dec80871af648c3aaef","body":"retry the flaky job once"}`,
	} {
		full := filepath.Join(top, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, top, "add", "-A")
	gitT(t, top, "commit", "-q", "--no-verify", "-m", "rows")
	t.Chdir(top)
	ctx := context.Background()
	for _, rel := range []string{actor, memo} {
		if err := os.Remove(filepath.Join(top, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	if err := wire.RecordMovedOut(ctx, top, "acme/client_billing", wire.MovedByDoctor, []string{actor}); err != nil {
		t.Fatal(err)
	}
	if err := wire.RecordMovedOut(ctx, top, "acme/client_billing", wire.MovedByMove, []string{memo}); err != nil {
		t.Fatal(err)
	}
	gitT(t, top, "add", "-A")
	base, derr := liveDoctor{h: f.s.h}.Base(f.ctx, false)
	if derr != nil {
		t.Fatal(derr)
	}
	notes := strings.Join(base.Notes, "\n")
	out, _ := f.s.h.cfg.Stderr.(*strings.Builder)
	out.Reset()
	if err := f.s.h.preCommit(ctx); err != nil {
		t.Fatalf("lore refused the removal its own doctor --fix and move made: %v", err)
	}
	for who, said := range map[string]string{"the pre-commit": out.String(), "doctor": notes} {
		for _, want := range []string{"`lore doctor --fix`", "`lore move`", "acme/client_billing"} {
			if !strings.Contains(said, want) {
				t.Errorf("%s does not name %s: %q", who, want, said)
			}
		}
		if strings.Contains(said, "adopt --remove-source") {
			t.Errorf("%s names adopt for rows doctor --fix and move removed: %q", who, said)
		}
	}
	// an entry without a command (an older lore's marker) is adopt's
	if by := wire.MovedOutBy(ctx, top); by[actor] != wire.MovedByDoctor || by[memo] != wire.MovedByMove {
		t.Errorf("MovedOutBy = %v", by)
	}
	marker := filepath.Join(top, ".git", "lore-moved-out.json")
	if err := os.WriteFile(marker, []byte(`{"rows":{"`+actor+`":"acme/client_billing"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if by := wire.MovedOutBy(ctx, top); by[actor] != wire.MovedByAdopt {
		t.Errorf("an older marker's entry reads as %q, want %q", by[actor], wire.MovedByAdopt)
	}
}
