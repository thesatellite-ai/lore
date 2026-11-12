package migrate

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
)

// journaled is fakeRewriter with a Journal whose Finish reports a change
// only when the run counted rows (a no-op run drops its backup).
type journaled struct {
	*fakeRewriter
}

func (j journaled) Begin(context.Context, bool) (Begun, error) {
	return Begun{BackupRef: "refs/lore-backup/migrate/x", Lines: []string{"backup: refs/lore-backup/migrate/x"}}, nil
}

func (j journaled) RewriteRepoV1(ctx context.Context, t row.Table, dry bool) ([]ids.AnyID, error) {
	return j.RewriteV1(ctx, t, dry)
}

func (j journaled) Finish(_ context.Context, _ bool, count int) (Finished, error) {
	if count == 0 {
		return Finished{}, nil
	}
	return Finished{Lines: []string{"pushed"}}, nil
}

var _ Journal = journaled{} // a signature change fails the build, not the run

func (j journaled) Status(context.Context) (StatusOut, error)     { return StatusOut{}, nil }
func (j journaled) Rollback(context.Context) (RollbackOut, error) { return RollbackOut{}, nil }
func (j journaled) Unlock(context.Context) (UnlockOut, error)     { return UnlockOut{}, nil }

// TestRunTeamLockJournal (OV38): the journal says whether a team lock
// guarded the run, on a run that changed rows AND on a no-op run (whose
// backup and other lines are dropped); "team lock released" follows only a
// lock that was taken, never the no-lock line of a surface without one;
// --dry-run / --no-push / --offline take none and print none of it.
func TestRunTeamLockJournal(t *testing.T) {
	mem, err := ids.ParseAny(fixedMem)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name    string
		lock    bool
		changed bool
		input   string
		want    []string
	}{
		{"lock, rows changed", true, true, `{}`, []string{"team lock: taken", "backup: refs/lore-backup/migrate/x", "pushed", teamLockReleasedLine}},
		{"lock, no-op", true, false, `{}`, []string{"team lock: taken", teamLockReleasedLine}},
		{"no lock surface, rows changed", false, true, `{}`, []string{noTeamLockLine, "backup: refs/lore-backup/migrate/x", "pushed"}},
		{"no lock surface, no-op", false, false, `{}`, []string{noTeamLockLine}},
		{"lock, --no-push no-op", true, false, `{"no_push":true}`, nil},
		{"lock, --dry-run", true, true, `{"dry_run":true}`, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeRewriter{tables: []row.Table{"memories"}}
			if c.changed {
				f.v1 = map[row.Table][]ids.AnyID{"memories": {mem}}
			}
			ctx := context.Background()
			l := &fakeTeamLock{}
			if c.lock {
				ctx = WithTeamLock(ctx, l)
			}
			e := Ops(journaled{f})[0]
			in, derr := e.Decode(op.JSONDecoder{Body: []byte(c.input)})
			if derr != nil {
				t.Fatal(derr)
			}
			res, ierr := e.Invoke(ctx, in)
			if ierr != nil {
				t.Fatal(ierr)
			}
			b, jerr := res.JSON()
			if jerr != nil {
				t.Fatal(jerr)
			}
			var out RunOut
			if uerr := json.Unmarshal(b, &out); uerr != nil {
				t.Fatal(uerr)
			}
			if !slices.Equal(out.Journal, c.want) {
				t.Fatalf("journal %q, want %q", out.Journal, c.want)
			}
			if c.lock && l.acquired != l.released {
				t.Fatalf("acquired %d released %d", l.acquired, l.released)
			}
		})
	}
}
