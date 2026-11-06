package wire

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/model/ids"
)

// signalsFixture is a repo with a bare origin and this repo's signals
// tracking ref holding n claims, of which expired are past their lease.
type signalsFixture struct {
	repo, bare string
	tracking   string
}

// newSignalsFixture writes the claims onto the tracking ref (what a fetch
// leaves) and, with pushed, the same commit onto the bare remote's ref.
func newSignalsFixture(t *testing.T, n, expired int, pushed bool) signalsFixture {
	t.Helper()
	repo := gitRepo(t)
	bare := t.TempDir()
	gitIn(t, bare, nil, "init", "-q", "--bare")
	gitIn(t, repo, nil, "remote", "add", "origin", bare)
	tracking, ok := gitremote.TrackingRef("origin", signals.CodeRepoRef)
	if !ok {
		t.Fatal("no tracking ref for the signals ref")
	}
	now := time.Now().UTC()
	files := map[string]string{}
	for i := range n {
		tid := string(ids.PrefixTask) + "_" + fmt.Sprintf("%032x", i+1)
		exp := now.Add(time.Hour)
		if i < expired {
			exp = now.Add(-time.Hour)
		}
		b, err := signals.Encode(signals.Signal{Kind: signals.KindClaim, Task: tid, Holder: testActor,
			Session: "s-1", ClaimToken: fmt.Sprintf("tok-%d", i), Since: now.Add(-2 * time.Hour), Expires: exp})
		if err != nil {
			t.Fatal(err)
		}
		files[signals.ClaimPath(signalsRootFolder, tid)] = string(b)
	}
	writeLiveRef(t, repo, tracking, files)
	if pushed {
		gitIn(t, repo, nil, "push", "-q", "origin", tracking+":"+signals.CodeRepoRef)
	}
	return signalsFixture{repo: repo, bare: bare, tracking: tracking}
}

// store is the signals store over the fixture; remote nil means none.
func (f signalsFixture) store(t *testing.T, remote gitremote.Remote) *signals.Store {
	t.Helper()
	r, err := gitref.Open(background(), f.repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &signals.Store{Repo: r, Remote: remote, RemoteName: "origin", Ref: signals.CodeRepoRef,
		Seen:  signals.SeenStore{Path: filepath.Join(t.TempDir(), "seen.json")},
		Clock: newTestApp(t, t.TempDir()), Lease: signals.Lease{TTL: time.Hour},
		Autopush: remote != nil}
}

// collect runs signalsRows and returns its warnings and reports.
func collect(t *testing.T, st *signals.Store) (warns, reports []string) {
	t.Helper()
	signalsRows(background(), st, func(s string) { warns = append(warns, s) }, func(s string) { reports = append(reports, s) })
	return warns, reports
}

// TestDoctorSignalsRows: a small fresh board is quiet; more than
// signalsRefLargeFiles files is one warning naming the count; expired
// claims are one report (not a warning) naming how many; no fetched copy at
// all is quiet; a remote refusing --atomic is the TEAM-036 warning, one
// that accepts it (the no-op lease push) is quiet. Break-verified: with the
// ErrAtomicUnsupported check inverted the refusing and accepting cases swap.
func TestDoctorSignalsRows(t *testing.T) {
	t.Run("small fresh board", func(t *testing.T) {
		w, r := collect(t, newSignalsFixture(t, 3, 0, false).store(t, nil))
		if len(w)+len(r) != 0 {
			t.Errorf("warns %v reports %v, want none", w, r)
		}
	})
	t.Run("more than the limit", func(t *testing.T) {
		w, r := collect(t, newSignalsFixture(t, signalsRefLargeFiles+1, 0, false).store(t, nil))
		if len(w) != 1 || !strings.Contains(w[0], fmt.Sprintf("holds %d signal files", signalsRefLargeFiles+1)) {
			t.Errorf("warns = %v, want the size warning", w)
		}
		if len(r) != 0 {
			t.Errorf("reports = %v, want none", r)
		}
	})
	t.Run("at the limit", func(t *testing.T) {
		w, _ := collect(t, newSignalsFixture(t, signalsRefLargeFiles, 0, false).store(t, nil))
		if len(w) != 0 {
			t.Errorf("warns = %v, want none at exactly the limit", w)
		}
	})
	t.Run("expired claims", func(t *testing.T) {
		w, r := collect(t, newSignalsFixture(t, 5, 2, false).store(t, nil))
		if len(w) != 0 {
			t.Errorf("warns = %v, want none (expired only reports)", w)
		}
		if len(r) != 1 || !strings.Contains(r[0], "2 expired signal file(s)") {
			t.Errorf("reports = %v, want one naming 2 expired files", r)
		}
	})
	t.Run("nothing fetched", func(t *testing.T) {
		repo := gitRepo(t)
		rp, err := gitref.Open(background(), repo, nil)
		if err != nil {
			t.Fatal(err)
		}
		st := &signals.Store{Repo: rp, RemoteName: "origin", Ref: signals.CodeRepoRef,
			Seen: signals.SeenStore{Path: filepath.Join(t.TempDir(), "seen.json")}, Clock: newTestApp(t, t.TempDir())}
		if w, r := collect(t, st); len(w)+len(r) != 0 {
			t.Errorf("warns %v reports %v, want none", w, r)
		}
	})
	for _, refuse := range []bool{true, false} {
		t.Run(fmt.Sprintf("atomic refused=%v", refuse), func(t *testing.T) {
			f := newSignalsFixture(t, 1, 0, true)
			r, err := gitref.Open(background(), f.repo, nil)
			if err != nil {
				t.Fatal(err)
			}
			fr := gitremote.NewFaultRemote(&gitremote.Git{Repo: r, Name: "origin"})
			fr.SetNoAtomic(refuse)
			w, _ := collect(t, f.store(t, fr))
			got := len(w) == 1 && strings.Contains(w[0], "does not support atomic push")
			if got != refuse {
				t.Errorf("refuse=%v: warns = %v", refuse, w)
			}
			if !refuse && len(w) != 0 {
				t.Errorf("accepting remote: warns = %v, want none", w)
			}
		})
	}
}
