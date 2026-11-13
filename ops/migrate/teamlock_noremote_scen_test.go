package migrate

// User scenario (migrate area): `lore migrate run` in a solo repo with no
// remote. The CLI host's team lock answers "nobody to race, none taken"
// with a nil release; the run's journal must then say exactly that and
// never "team lock released" for a hold it never took, on a run that
// changed rows and on a no-op run alike. A rollback on such a host must
// work the same (it takes T too).

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
)

// noRemoteLine is what the CLI host says for a repo with no remote
// (wire/migrate_teamlock_standalone.go).
const noRemoteLine = "team lock: this repo has no remote origin; nobody to race, none taken"

// noRemoteTeamLock is a host lock that takes no hold: nil release, a line.
type noRemoteTeamLock struct{ acquired int }

func (l *noRemoteTeamLock) Acquire(context.Context) (func() error, string, error) {
	l.acquired++
	return nil, noRemoteLine, nil
}

func (l *noRemoteTeamLock) Unlock(context.Context) (UnlockOut, error) {
	return UnlockOut{}, nil
}

// TestMigrateScenNoRemoteNeverSaysReleased: rows changed or not, the
// journal names the "none taken" line and no release line.
func TestMigrateScenNoRemoteNeverSaysReleased(t *testing.T) {
	mem, err := ids.ParseAny(fixedMem)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name    string
		changed bool
		want    []string
	}{
		{"rows changed", true, []string{noRemoteLine, "backup: refs/lore-backup/migrate/x", "pushed"}},
		{"no-op", false, []string{noRemoteLine}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeRewriter{tables: []row.Table{"memories"}}
			if c.changed {
				f.v1 = map[row.Table][]ids.AnyID{"memories": {mem}}
			}
			l := &noRemoteTeamLock{}
			e := Ops(journaled{f})[0]
			in, derr := e.Decode(op.JSONDecoder{Body: []byte(`{"team_lock":true}`)})
			if derr != nil {
				t.Fatal(derr)
			}
			res, ierr := e.Invoke(WithTeamLock(context.Background(), l), in)
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
			if slices.Contains(out.Journal, teamLockReleasedLine) {
				t.Fatalf("a run that took no team lock says %q", teamLockReleasedLine)
			}
			if l.acquired != 1 {
				t.Fatalf("acquire called %d times, want 1", l.acquired)
			}
		})
	}
}

// TestMigrateScenNoRemoteRollback: a rollback on a host whose lock takes no
// hold goes through (the nil release is never called).
func TestMigrateScenNoRemoteRollback(t *testing.T) {
	f := &fakeRewriter{tables: []row.Table{"memories"}}
	var rb op.Entry
	for _, e := range Ops(journaled{f}) {
		if e.Spec().Name == OpRollback {
			rb = e
		}
	}
	if rb == nil {
		t.Fatal("no rollback operation")
	}
	in, err := rb.Decode(op.JSONDecoder{Body: []byte(`{"team_lock":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rb.Invoke(WithTeamLock(context.Background(), &noRemoteTeamLock{}), in); err != nil {
		t.Fatalf("rollback with a no-remote team lock: %v", err)
	}
}
