//go:build stories && !windows

package stories

// D_helpers_test.go — what story D needs on top of the shared harness: the
// remotes it watches for rule 1, and the files it compares byte for byte.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// dRefs is a refs snapshot of every remote story D watches, so a step can say
// "nobody pushed in here" in one line (standing rule 1).
type dRefs map[*teamharness.Remote]string

// dSnap snapshots the refs of every remote in rs.
func dSnap(s *Story, rs ...*teamharness.Remote) dRefs {
	s.T.Helper()
	out := dRefs{}
	for _, r := range rs {
		out[r] = s.RefsOf(r)
	}
	return out
}

// dNoPush fails when any remote of the snapshot moved since it was taken.
func dNoPush(t testing.TB, s *Story, before dRefs, what string) {
	t.Helper()
	for r, refs := range before {
		s.NoPushSince(t, r, refs, what)
	}
}

// dRead reads a file of a work tree ("" when it is missing): the pointer and
// agent files are compared as the person would see them.
func dRead(t testing.TB, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(b)
}

// dExists reports whether rel exists under dir (without following a final
// symlink, so a dangling link still counts).
func dExists(dir, rel string) bool {
	_, err := os.Lstat(filepath.Join(dir, rel))
	return err == nil
}
