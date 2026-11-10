package signals

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest"
	"github.com/thesatellite-ai/lore/logic/clock"
)

// TestOfflineErrorRacesLost (D-GRACE-B4): OfflineError.RacesLost is set
// only when every attempt reached the remote and lost a race (the retries
// ran out); an unreachable remote, autopush off, or a push that failed
// for another reason keep RacesLost false (E_OFFLINE stays theirs). Every
// case is still errors.Is(ErrOffline), so callers that test that (the
// migrate team lock) keep working. A run that loses fewer races than the
// retries succeeds and returns no error at all.
func TestOfflineErrorRacesLost(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		setup     func(f *gitremote.FaultRemote, s *Store)
		wantErr   bool
		racesLost bool
	}{
		{"every attempt lost (stale)", func(f *gitremote.FaultRemote, _ *Store) {
			for i := 1; i <= 10; i++ {
				f.RejectPush(i, gitremote.StatusStale)
			}
		}, true, true},
		{"lost fewer than the retries", func(f *gitremote.FaultRemote, _ *Store) { f.RejectPush(1, gitremote.StatusStale) }, false, false},
		{"offline", func(f *gitremote.FaultRemote, _ *Store) { f.SetOffline(true) }, true, false},
		{"autopush off", func(_ *gitremote.FaultRemote, s *Store) { s.Autopush = false }, true, false},
		{"offline at push", func(_ *gitremote.FaultRemote, s *Store) { s.Remote = unconfirmedPush{s.Remote} }, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := gitremotetest.NewWorld(t)
			a := w.Clone("a")
			f := gitremote.NewFaultRemote(a.Remote)
			s := newStore(t, a, f, clock.NewFake(t0))
			tc.setup(f, s)
			var plan ClaimPlan
			_, err := s.Write(ctx, claimDecide(s, me, task, &plan))
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
				return
			}
			var off *OfflineError
			if !errors.As(err, &off) || !errors.Is(err, ErrOffline) {
				t.Fatalf("want an *OfflineError that is ErrOffline, got %v", err)
			}
			if off.RacesLost != tc.racesLost {
				t.Fatalf("RacesLost = %v, want %v (%v)", off.RacesLost, tc.racesLost, err)
			}
		})
	}
}
