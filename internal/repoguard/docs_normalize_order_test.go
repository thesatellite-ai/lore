package repoguard

// devco:D-412: the docs runners compare a fresh run's transcript with the
// committed one, and the tasks transcript changed on a re-run with nothing
// wrong. `lore tasklist list` and the ride-along landing line order rows by
// id; the default tasklist's id is a hash of the random project id, other ids
// start with their creation time, so in a few runs out of hundreds the default
// list sorted first instead of last. Lore's order never changes for the same
// rows: the normalisers must give the same text for both orders.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDocsNormaliseIDOrderedLists(t *testing.T) {
	t.Parallel()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed; the docs runners need it and run in the full gate")
	}
	const (
		launch = "tlt_019a8f2c11d27c3b9e0f4a5b6c7d8e9f" // a list made by `tasklist add` (time first)
		def    = "tlt_7f3e0a9b8c1d4e2f9a0b1c2d3e4f5a6b" // the default list (a hash)
		low    = "tlt_0042aa9b8c1d4e2f9a0b1c2d3e4f5a6b" // the same default list in a world where the hash is low
		task   = "tsk_019a8f2c11d27c3b9e0f4a5b6c7d8e90"
	)
	transcript := func(first, second, defID string) string {
		return "$ lore tasklist add --title 'Launch week'\n✓ " + launch + "\n" +
			"$ lore tasklist list\n" +
			first + "\n" + second + "\n" +
			"$ git push -q origin main\n" +
			`lore: added "lore: land 3 rows (_root)" to main (` + sorted2(launch, defID) + ", " + task + ")\n"
	}
	lineL := launch + "  Launch week [active]  (2 tasks)"
	for _, script := range []string{"tests/docs/normalize.py", "tests/team/docs/normalize.py"} {
		run := func(text string) string {
			p := filepath.Join(t.TempDir(), "t.txt")
			if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(py, filepath.Join(repoRoot, script), p).CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", script, err, out)
			}
			got, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			return string(got)
		}
		// The usual world: the default list's hash sorts after the time-first id.
		a := run(transcript(lineL, def+"  default [active]  (4 tasks)", def))
		// The rare world: the hash sorts first, so lore prints it first.
		b := run(transcript(low+"  default [active]  (4 tasks)", lineL, low))
		if a != b {
			t.Errorf("%s: one rule (by id), two worlds, two transcripts:\n%s\n---\n%s", script, a, b)
		}
	}
}

// sorted2 joins two ids in lore's order (by id), as the landing line prints them.
func sorted2(x, y string) string {
	if y < x {
		x, y = y, x
	}
	return x + ", " + y
}
