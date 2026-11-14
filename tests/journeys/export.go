//go:build journeys && !windows

package journeys

// export.go — the part of this harness another suite builds on. tests/stories
// (build tags `stories journeys`) replays whole user stories with the same
// World, Personas, Machines, Steps and shared invariants as the journeys, so an
// invariant added here checks every story too. That suite has its own TestMain
// and binary, so it hands the binary in with UseBinary, and it starts teams
// with the same guided-setup answers the team-lead journey uses (StartTeam,
// Join, Invite) instead of a second copy of them.

import "testing"

// loreBin is the lore under test: this package's TestMain sets it, another
// suite sets it with UseBinary before its first NewWorld.
var loreBin string

// UseBinary makes every World made after it run bin (a lore built with
// -tags loretest, so LORE_NOW works). A suite that imports this package calls
// it from its own TestMain.
func UseBinary(bin string) { loreBin = bin }

// Person is a team member's name, email and GitHub login (all placeholders).
type Person = tlPerson

// GitHubLogin gives m a fake gh and ssh logged in as login, the way the
// guided setup's GitHub step expects a person's machine to look.
func GitHubLogin(t *testing.T, w *World, m *Machine, login string) {
	t.Helper()
	tlGitHub(t, w, m, login)
}

// StartTeam runs the guided setup as the admin of a new org whose team store
// is storeURL, over folders, then syncs (the team-lead journey's day 1).
func StartTeam(t testing.TB, m *Machine, dir, setup, org, storeURL string, p Person, folders []string) {
	t.Helper()
	tlStartTeam(t, m, dir, setup, org, storeURL, p, folders)
}

// Join runs the guided setup as a member who joins with the invite line the
// admin's `lore team invite` printed, then syncs.
func Join(t testing.TB, m *Machine, dir, setup, invite string, p Person, folders []string) {
	t.Helper()
	tlJoin(t, m, dir, setup, invite, p, folders)
}

// Invite has the admin on m invite email and returns the invite's id and the
// lore-invite: line to hand over.
func Invite(t testing.TB, m *Machine, dir, email string, extra ...string) (id, line string) {
	t.Helper()
	return tlInvite(t, m, dir, email, extra...)
}
