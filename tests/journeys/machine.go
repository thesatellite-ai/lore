//go:build journeys && !windows

package journeys

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Machine is one computer of a persona: a HOME, its own clone folders, its
// network state. It is a teamharness Actor whose lore runs are recorded so
// the readable-output invariant can check every answer a step printed.
type Machine struct {
	*teamharness.Actor
	Persona *Persona
	Label   string
	w       *World
	offline bool
	asleep  bool
	clones  []*teamharness.Clone
	results []teamharness.Result // this machine's lore runs in the current step
}

// Lore runs lore in dir (relative to HOME) and records the result for the
// step's invariants. Every journey lore run goes through here or LoreIn.
func (m *Machine) Lore(dir string, args ...string) teamharness.Result {
	m.w.T.Helper()
	r := m.Actor.Lore(dir, args...)
	m.results = append(m.results, r)
	return r
}

// LoreIn runs lore inside a clone of this machine.
func (m *Machine) LoreIn(c *teamharness.Clone, args ...string) teamharness.Result {
	m.w.T.Helper()
	return m.Lore(c.Dir, args...)
}

// Offline cuts the machine off the network the way a dead network does:
// every fetch and push of github.com goes to a host that cannot be resolved,
// so git fails with "Could not resolve host", as with the wifi off. (The
// teamharness Offline points at a missing folder instead, which git reports
// as "not a git repository" and lore rightly reads as a missing repo.)
func (m *Machine) Offline() {
	m.offline = true
	m.Actor.Online() // the baseline ~/.gitconfig, then swap the route
	gc := filepath.Join(m.Home, ".gitconfig")
	fake := "url." + filepath.Join(m.w.Root, "hosts", "github.com") + "/"
	for _, args := range [][]string{
		{"--remove-section", fake},
		{"--add", "url." + unresolvable + ".insteadOf", "https://github.com/"},
		{"--add", "url." + unresolvable + ".insteadOf", "git@github.com:"},
	} {
		if r := m.w.TryGit(m.Home, append([]string{"config", "--file", gc}, args...)...); r.Code != 0 {
			m.w.T.Fatalf("offline %v: %s", args, r.Stderr)
		}
	}
	m.setGitIdentity(m.w.T, m.Persona.Name, m.Persona.Email)
}

// unresolvable is where an offline machine's github.com traffic goes: the
// .invalid TLD never resolves (RFC 6761), so the lookup fails at once.
const unresolvable = "https://github.com.invalid/"

// Online puts it back.
func (m *Machine) Online() {
	m.offline = false
	m.Actor.Online()
	m.setGitIdentity(m.w.T, m.Persona.Name, m.Persona.Email)
}

// IsOffline reports the machine's network state; invariants that need the
// network skip an offline machine.
func (m *Machine) IsOffline() bool { return m.offline }

// Sleep puts the machine away (lid closed): it stays as it is, and the
// invariants skip it until Wake. A machine left behind for months is asleep.
func (m *Machine) Sleep() { m.asleep = true }

// Wake opens the lid again, at the World's current time.
func (m *Machine) Wake() { m.asleep = false; m.SetNow(m.w.now) }

// Asleep reports whether the machine is put away.
func (m *Machine) Asleep() bool { return m.asleep }

// SleepMidPush runs lore in dir and kills it the moment it starts a git push
// (the laptop lid closes mid-push): the push never reaches the remote.
// after=true lets the push land and kills lore right after, before it can
// record that it pushed.
func (m *Machine) SleepMidPush(dir string, after bool, args ...string) teamharness.Result {
	m.w.T.Helper()
	when := teamharness.KillBefore
	if after {
		when = teamharness.KillAfter
	}
	counter := filepath.Join(m.w.Root, "kill-"+m.Name)
	_ = os.Remove(counter)
	m.SetEnv(teamharness.EnvKillOn, "push")
	m.SetEnv(teamharness.EnvKillWhen, when)
	m.SetEnv(teamharness.EnvKillCounter, counter)
	defer func() {
		m.SetEnv(teamharness.EnvKillOn, "")
		m.SetEnv(teamharness.EnvKillWhen, "")
		m.SetEnv(teamharness.EnvKillCounter, "")
	}()
	return m.Lore(dir, args...)
}

// MoveFolder renames a folder on this machine (a repo folder renamed or
// moved, a store folder moved by hand).
func (m *Machine) MoveFolder(t testing.TB, from, to string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); err != nil {
		t.Fatalf("move %s → %s: %v", from, to, err)
	}
}

// setGitIdentity rewrites user.name/user.email in the machine's ~/.gitconfig.
func (m *Machine) setGitIdentity(t testing.TB, name, email string) {
	t.Helper()
	m.Email = email
	gc := filepath.Join(m.Home, ".gitconfig")
	for k, v := range map[string]string{"user.name": name, "user.email": email} {
		if r := m.w.TryGit(m.Home, "config", "--file", gc, k, v); r.Code != 0 {
			t.Fatalf("set %s: %s", k, r.Stderr)
		}
	}
}

// Clone clones a remote into this machine's work folder and keeps it in the
// machine's list, so the invariants run doctor and the mode checks in it too.
func (m *Machine) Clone(r *teamharness.Remote, name string) *teamharness.Clone {
	m.w.T.Helper()
	c := m.Actor.Clone(r, name)
	m.clones = append(m.clones, c)
	return c
}

// Clones is every clone of this machine still on disk.
func (m *Machine) Clones() []*teamharness.Clone {
	var out []*teamharness.Clone
	for _, c := range m.clones {
		if _, err := os.Stat(c.Dir); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// MoveClone renames a clone's folder (the person renames or moves the repo
// folder) and keeps the clone pointing at its new place.
func (m *Machine) MoveClone(t testing.TB, c *teamharness.Clone, to string) {
	t.Helper()
	if !filepath.IsAbs(to) {
		to = filepath.Join(filepath.Dir(c.Dir), to)
	}
	m.MoveFolder(t, c.Dir, to)
	c.Dir = to
}
