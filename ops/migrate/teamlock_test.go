package migrate

import (
	"context"
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

// TestRunTeamLock (FORMAT_EVOLUTION 7.2, 7.5): a pushing run takes T after
// M and releases it; --dry-run, --no-push and --offline take none; a held
// T is E_MIGRATE_LOCKED before any step, with M still released.
func TestRunTeamLock(t *testing.T) {
	for in, want := range map[string]int{`{}`: 1, `{"dry_run":true}`: 0, `{"no_push":true}`: 0, `{"offline":true}`: 0} {
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
	if err := runWith(t, WithTeamLock(context.Background(), l), f, `{}`); !errs.Has(err, errs.MigrateLocked) {
		t.Fatalf("held: want E_MIGRATE_LOCKED, got %v", err)
	}
	if !slices.Equal(f.calls, []string{"lock"}) || f.unlocked != 1 {
		t.Fatalf("held: calls %v unlocked %d", f.calls, f.unlocked)
	}
}
