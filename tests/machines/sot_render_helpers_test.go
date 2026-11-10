//go:build machines && !windows

package machines

// sot_render_helpers_test.go — helpers of the rendered-copy proofs
// (sot_render_test.go): a repo cloned from a bare repo behind a GitHub url
// with lore set up and pushed, a second clone of it, and the doctor's and
// render --check's answers about the copies.

import (
	"path/filepath"
	"strings"
	"testing"
)

// sotrRepo clones url (a bare repo behind it, see sotRewrite) into
// ~/code/<name>; with first it runs lore init, one must rule and lore
// render, then commits and pushes all of it.
func sotrRepo(t *testing.T, m *Machine, url, name string, first bool) string {
	t.Helper()
	code := filepath.Join(m.Home, "code")
	sotGit(t, m, code, "clone", "-q", url, name)
	dir := filepath.Join(code, name)
	if !first {
		return dir
	}
	sotOK(t, m, dir, "init", "--name", "sot-render")
	sotOK(t, m, dir, "rule", "add", "--body", "sotr rule one", "--severity", "must")
	sotOK(t, m, dir, "render")
	sotGit(t, m, dir, "add", "-A")
	sotGit(t, m, dir, "commit", "-q", "-m", "lore init and render")
	sotGit(t, m, dir, "push", "-q", "origin", "main")
	return dir
}

// sotrCheck runs lore render --check in dir and returns its exit code and
// output.
func sotrCheck(t *testing.T, m *Machine, dir string) (int, string) {
	t.Helper()
	r := m.Lore(t, dir, "render", "--check")
	return r.Code, sotOut(r)
}

// sotrDoctor is lore doctor's output in dir (any exit: a warning is a
// degraded status, not a failure).
func sotrDoctor(t *testing.T, m *Machine, dir string) string {
	t.Helper()
	return sotOut(m.Lore(t, dir, "doctor"))
}

// sotrHas fails t unless got holds every want.
func sotrHas(t *testing.T, step, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s: no %q in:\n%s", step, w, got)
		}
	}
}

// sotrLacks fails t when got holds any of not.
func sotrLacks(t *testing.T, step, got string, not ...string) {
	t.Helper()
	for _, n := range not {
		if strings.Contains(got, n) {
			t.Errorf("%s: unexpected %q in:\n%s", step, n, got)
		}
	}
}
