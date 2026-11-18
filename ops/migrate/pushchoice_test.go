package migrate

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
)

// choiceJournal is journaled recording the PushChoice Finish got.
type choiceJournal struct {
	journaled
	got *[]PushChoice
}

func (j choiceJournal) Finish(ctx context.Context, c PushChoice, count int) (Finished, error) {
	*j.got = append(*j.got, c)
	return j.journaled.Finish(ctx, c, count)
}

// TestRunPushChoiceD612 (devco D-612): a plain `lore migrate run` hands
// Finish PushPerMode (each store's push mode decides; it used to be "push"
// past push mode manual), --push PushNow, --no-push and --offline
// PushNever; --push with --no-push, and --team-lock without --push, are
// refused before anything runs.
func TestRunPushChoiceD612(t *testing.T) {
	mem, err := ids.ParseAny(fixedMem)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		input string
		want  PushChoice
		code  errs.Code
	}{
		{`{}`, PushPerMode, ""},
		{`{"push":true}`, PushNow, ""},
		{`{"no_push":true}`, PushNever, ""},
		{`{"offline":true}`, PushNever, ""},
		{`{"push":true,"team_lock":true}`, PushNow, ""},
		{`{"push":true,"no_push":true}`, 0, errs.InvalidInput},
		{`{"team_lock":true}`, 0, errs.InvalidInput},
	} {
		t.Run(c.input, func(t *testing.T) {
			f := &fakeRewriter{tables: []row.Table{"memories"}, v1: map[row.Table][]ids.AnyID{"memories": {mem}}}
			var got []PushChoice
			l := &fakeTeamLock{}
			e := Ops(choiceJournal{journaled{f}, &got})[0]
			in, derr := e.Decode(op.JSONDecoder{Body: []byte(c.input)})
			if derr != nil {
				t.Fatal(derr)
			}
			_, ierr := e.Invoke(WithTeamLock(context.Background(), l), in)
			if c.code != "" {
				if !errs.Has(ierr, c.code) {
					t.Fatalf("err %v, want %s", ierr, c.code)
				}
				if len(got) != 0 || l.acquired != 0 || len(f.calls) != 0 {
					t.Fatalf("a refused run did work: finish %v, lock %d, calls %v", got, l.acquired, f.calls)
				}
				return
			}
			if ierr != nil {
				t.Fatal(ierr)
			}
			if len(got) != 1 || got[0] != c.want {
				t.Fatalf("Finish got %v, want [%v]", got, c.want)
			}
		})
	}
}

// TestRollbackPushChoiceD612: rollback hands the host PushPerMode unless
// --push; --team-lock without --push is refused (the lock is a push).
func TestRollbackPushChoiceD612(t *testing.T) {
	for _, c := range []struct {
		input string
		want  PushChoice
		code  errs.Code
	}{
		{`{}`, PushPerMode, ""},
		{`{"push":true}`, PushNow, ""},
		{`{"team_lock":true}`, 0, errs.InvalidInput},
	} {
		t.Run(c.input, func(t *testing.T) {
			var got []PushChoice
			j := rollbackChoice{journaled{&fakeRewriter{}}, &got}
			var e op.Entry
			for _, x := range Ops(j) {
				if x.Spec().Name == OpRollback {
					e = x
				}
			}
			in, derr := e.Decode(op.JSONDecoder{Body: []byte(c.input)})
			if derr != nil {
				t.Fatal(derr)
			}
			_, ierr := e.Invoke(WithTeamLock(context.Background(), &fakeTeamLock{}), in)
			if c.code != "" {
				if !errs.Has(ierr, c.code) || len(got) != 0 {
					t.Fatalf("err %v rollbacks %v, want %s and none", ierr, got, c.code)
				}
				return
			}
			if ierr != nil || len(got) != 1 || got[0] != c.want {
				t.Fatalf("err %v, Rollback saw %v, want [%v]", ierr, got, c.want)
			}
		})
	}
}

// rollbackChoice is journaled recording the PushChoice its Rollback saw.
type rollbackChoice struct {
	journaled
	got *[]PushChoice
}

func (j rollbackChoice) Rollback(ctx context.Context) (RollbackOut, error) {
	*j.got = append(*j.got, PushChoiceFrom(ctx))
	return RollbackOut{}, nil
}
