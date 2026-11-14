//go:build stories && !windows

// Package stories replays every user story lore was ever tried against (the
// bug-hunt journeys J1-J4 and the dev-company tracks A-I) end to end through
// the real binary, so a feature added later cannot quietly break a scenario
// that once worked or bring back a bug that was fixed. Each story is a plain
// words page (tests/stories/<id>-<name>.md) plus a test here that walks it.
//
// The harness is the journeys World (tests/journeys, imported with the
// `journeys` build tag): fake GitHub remotes that are bare repositories on
// disk, one sandbox HOME per machine with a made-up git identity, the LORE_NOW
// clock, and the shared invariants after every step. On top it adds what the
// stories need: the owner's standing rules as checks (rules.go) and fixture
// generators that build the repo SHAPES the scenarios ran on (fixtures.go).
// Every name, email, URL and row text here is made up.
package stories

import (
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Story is one replayed story: a journeys World plus the story's id, which
// names its page (tests/stories/<id>-*.md) in every failure.
type Story struct {
	*journeys.World
	ID string
}

// New starts a story on a fresh World at the journeys' day 1. Stories run in
// parallel with each other (each has its own World under t.TempDir).
func New(t *testing.T, id string) *Story {
	t.Helper()
	t.Parallel()
	return &Story{World: journeys.NewWorld(t), ID: id}
}

// Person adds a persona with a made-up name and email (and their first
// laptop) and returns that laptop: most stories need only the machine.
func (s *Story) Person(name, email string) *journeys.Machine {
	s.T.Helper()
	return s.Persona(name, email).Laptop()
}

// Seed fills a remote with a first commit: fill writes the files into a
// scratch clone (made by the harness's own git, so no persona's lore runs),
// which is committed as "seed" and pushed to main. It returns the remote.
func (s *Story) Seed(r *teamharness.Remote, fill func(dir string)) *teamharness.Remote {
	s.T.Helper()
	dir := s.T.TempDir()
	s.Git(dir, "clone", "-q", r.Dir, "seed")
	work := dir + "/seed"
	s.Git(work, "checkout", "-q", "-B", "main")
	fill(work)
	s.Git(work, "add", "-A")
	s.Git(work, "-c", "user.name=Seed Bot", "-c", "user.email=seed@example.test", "commit", "-q", "--allow-empty", "-m", "seed")
	s.Git(work, "push", "-q", "origin", "main")
	return r
}
