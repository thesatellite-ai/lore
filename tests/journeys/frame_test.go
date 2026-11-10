//go:build journeys && !windows

package journeys

// frame_test.go — the harness's own tests: a tiny journey through every
// harness action, and proof that each shared invariant bites (a checker that
// never fails checks nothing).

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestFrameJourney walks the harness: persona, clone, a note, days passing,
// offline, a second laptop, a killed push, a fact.
func TestFrameJourney(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	dev := w.Persona("Dev One", "dev.one@example.test")
	app := w.Remote("github.com/example-org/app")
	m := dev.Laptop()
	var repo *teamharness.Clone
	w.Step("day1/first-note", func() {
		repo = m.Clone(app, "app")
		m.LoreIn(repo, "init").OK(t)
		m.LoreIn(repo, "memory", "add", "--body", "frame note one").OK(t)
	})
	w.Fact("the note stays listed on the laptop", func(c Ctx) {
		if r := m.Actor.Lore(repo.Dir, "memory", "list", "--json"); !strings.Contains(r.Stdout, "frame note one") {
			c.Errorf("note gone: %s", r)
		}
	})
	w.Step("day3/clock", func() {
		w.AdvanceDays(2)
		if w.Day() != 3 || !m.Now().Equal(JourneyStart.Add(48*time.Hour)) {
			t.Errorf("clock: day %d, machine %v", w.Day(), m.Now())
		}
	})
	w.Step("day3/offline", func() {
		m.Offline()
		if got := repo.Git("config", "--global", "user.name"); got != "Dev One" {
			t.Errorf("offline lost the git identity: %q", got)
		}
		if r := repo.TryGit("fetch", "origin"); r.Code == 0 || !strings.Contains(r.Stderr, "Could not resolve host") {
			t.Errorf("offline fetch should fail like a dead network: exit %d %s", r.Code, r.Stderr)
		}
		m.Online()
		if r := repo.TryGit("fetch", "origin"); r.Code != 0 {
			t.Errorf("online again, fetch fails: %s", r.Stderr)
		}
	})
	w.Step("day3/new-laptop", func() {
		nl := dev.NewLaptop("desk")
		if !nl.Now().Equal(w.Now()) || nl.Home == m.Home || len(w.Machines()) != 2 {
			t.Errorf("new laptop: now %v home %s", nl.Now(), nl.Home)
		}
		nl.Sleep()
	})
	w.Step("day3/killed-push", func() {
		r := m.SleepMidPush(repo.Dir, false, "sync", "push")
		if r.Code != killed && r.Code == 0 {
			t.Logf("no push happened to kill (exit %d): %s", r.Code, r)
		}
		if m.Env(teamharness.EnvKillOn) != "" {
			t.Error("SleepMidPush left the kill switch on")
		}
	})
}

// TestFrameInvariantsBite breaks each shared invariant's subject on purpose
// and checks the invariant reports it.
func TestFrameInvariantsBite(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	m := w.Persona("Dev Two", "dev.two@example.test").Laptop()
	repo := m.Clone(w.Remote("github.com/example-org/bite"), "bite")
	m.LoreIn(repo, "init").OK(t)
	bites := func(name string, check func(Ctx)) {
		t.Helper()
		rec := &recorder{TB: t}
		done := make(chan struct{})
		go func() { defer close(done); check(Ctx{T: rec, W: w, M: m, Step: "bite"}) }()
		<-done
		if !rec.failed {
			t.Errorf("invariant %s did not bite", name)
		}
	}
	m.results = []teamharness.Result{{Args: []string{"x"}, Stdout: "panic: boom\ngoroutine 1"}}
	bites("readable-output/panic", checkReadable)
	m.results = []teamharness.Result{{Args: []string{"x"}, Code: 2}}
	bites("readable-output/silent-failure", checkReadable)
	m.results = nil

	if err := os.MkdirAll(m.LorePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(m.LorePath(), 0o755); err != nil { // open the folder, so the file's own mode counts
		t.Fatal(err)
	}
	leak := m.LorePath("leak.txt")
	if err := os.WriteFile(leak, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bites("modes", checkModes)
	_ = os.Remove(leak)
	_ = os.Chmod(m.LorePath(), 0o700)

	if err := os.WriteFile(m.LorePath("config.yaml"), []byte("no_such_key_ever: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bites("config", checkConfig)
	_ = os.Remove(m.LorePath("config.yaml"))

	if err := os.WriteFile(m.LorePath("config.yaml"), []byte(":\n\t- not yaml [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bites("doctor-or-config on a broken config", func(c Ctx) { checkConfig(c); checkDoctor(c) })
	_ = os.Remove(m.LorePath("config.yaml"))
}

// TestFramePending: a pending step's failure, Fatal included, is recorded
// and the journey goes on; a passing one records nothing.
func TestFramePending(t *testing.T) {
	t.Setenv(envStrict, "") // the recorder path, also in a strict run
	reached := false
	Pending(t, "nobody", "frame/fails", func(tb testing.TB) {
		tb.Errorf("first")
		tb.Fatalf("second")
		reached = true
	})
	if reached {
		t.Fatal("Fatalf inside a pending step did not stop it")
	}
	ok := true
	Pending(t, "nobody", "frame/passes", func(tb testing.TB) { ok = !tb.Failed() })
	if !ok {
		t.Fatal("a fresh pending step starts failed")
	}
}
