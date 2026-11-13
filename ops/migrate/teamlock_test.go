package migrate

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
)

// fakeTeamLock records acquire/release and can answer a held lock.
type fakeTeamLock struct {
	held               bool
	acquired, released int
}

func (l *fakeTeamLock) Acquire(context.Context) (func() error, string, error) {
	if l.held {
		return nil, "", errs.New(errs.MigrateLocked, "mira is migrating")
	}
	l.acquired++
	return func() error { l.released++; return nil }, "team lock: taken", nil
}

func (l *fakeTeamLock) Unlock(context.Context) (UnlockOut, error) {
	return UnlockOut{Released: true}, nil
}

// runWith invokes migrate.run with ctx.
func runWith(t *testing.T, ctx context.Context, f *fakeRewriter, input string) error {
	t.Helper()
	e := Ops(f)[0]
	in, err := e.Decode(op.JSONDecoder{Body: []byte(input)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Invoke(ctx, in)
	return err
}

// TestRunTeamLock (FORMAT_EVOLUTION 7.2, 7.5): a pushing run asked for
// --team-lock takes T after M and releases it; a plain run takes none (A-7:
// T is a push to the remote, so only the flag takes it); --dry-run,
// --no-push and --offline take none even with the flag; a held T is
// E_MIGRATE_LOCKED before any step, with M still released.
func TestRunTeamLock(t *testing.T) {
	for in, want := range map[string]int{`{"team_lock":true}`: 1, `{}`: 0, `{"team_lock":true,"dry_run":true}`: 0, `{"team_lock":true,"no_push":true}`: 0, `{"team_lock":true,"offline":true}`: 0} {
		l := &fakeTeamLock{}
		f := &fakeRewriter{tables: []row.Table{"memories"}}
		if err := runWith(t, WithTeamLock(context.Background(), l), f, in); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if l.acquired != want || l.released != want {
			t.Fatalf("%s: acquired %d released %d, want %d", in, l.acquired, l.released, want)
		}
	}
	l := &fakeTeamLock{held: true}
	f := &fakeRewriter{tables: []row.Table{"memories"}}
	if err := runWith(t, WithTeamLock(context.Background(), l), f, `{"team_lock":true}`); !errs.Has(err, errs.MigrateLocked) {
		t.Fatalf("held: want E_MIGRATE_LOCKED, got %v", err)
	}
	if !slices.Equal(f.calls, []string{"lock"}) || f.unlocked != 1 {
		t.Fatalf("held: calls %v unlocked %d", f.calls, f.unlocked)
	}
}

// TestRunWithoutTeamLockSaysLocal (A-7, C-4): a pushing run without
// --team-lock never calls the host's lock (which pushes refs/lore/signals)
// and its journal says only this machine's lock guarded it.
func TestRunWithoutTeamLockSaysLocal(t *testing.T) {
	l := &fakeTeamLock{}
	f := &fakeRewriter{tables: []row.Table{"memories"}}
	e := Ops(journaled{f})[0]
	in, err := e.Decode(op.JSONDecoder{Body: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Invoke(WithTeamLock(context.Background(), l), in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var out RunOut
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if l.acquired != 0 {
		t.Fatalf("a run without --team-lock took the team lock %d time(s)", l.acquired)
	}
	if len(out.Journal) == 0 || out.Journal[0] != localLockLine || slices.Contains(out.Journal, teamLockReleasedLine) {
		t.Fatalf("journal %q: want %q first and no release", out.Journal, localLockLine)
	}
}
