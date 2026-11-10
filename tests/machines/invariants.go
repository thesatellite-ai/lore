//go:build machines && !windows

package machines

// invariants.go — what must hold on every machine after every step,
// checked through the real binary's --json output.

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// listOut is the part of `lore setup list --json` the invariants read.
type listOut struct {
	Setups []struct {
		Name     string `json:"name"`
		Disabled bool   `json:"disabled"`
	} `json:"setups"`
	Active struct {
		Name string `json:"name"`
		Why  string `json:"why"`
	} `json:"active"`
	Trashed int `json:"trashed"`
}

type trashOut struct {
	Setups []struct {
		Setup struct {
			Name string `json:"name"`
		} `json:"setup"`
	} `json:"setups"`
}

type scanOut struct {
	Proposals []struct {
		Setup struct {
			Name     string `json:"name"`
			Identity struct {
				Key string `json:"key"`
			} `json:"identity"`
		} `json:"setup"`
	} `json:"proposals"`
	Banner string `json:"banner"`
}

type checkOut struct {
	// Warnings are lines like "unknown key, ignored: line 4: trash".
	Warnings []string `json:"warnings"`
}

type doctorOut struct {
	Status string `json:"status"`
}

// outputs is every --json shape decode reads (a closed set, so no any).
type outputs interface {
	listOut | trashOut | scanOut | checkOut | doctorOut | aliasesOut
}

func decode[T outputs](t testing.TB, r Result) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("not JSON (%v): %s", err, r)
	}
	return v
}

// List is `lore setup list --json` run in cwd.
func (m *Machine) List(t testing.TB, cwd string) listOut {
	t.Helper()
	r := m.Lore(t, cwd, "setup", "list", "--json")
	if r.Code != 0 {
		t.Fatalf("setup list: %s", r)
	}
	return decode[listOut](t, r)
}

// Trash is `lore setup trash list --json`.
func (m *Machine) Trash(t testing.TB) []string {
	t.Helper()
	r := m.Lore(t, "", "setup", "trash", "list", "--json")
	if r.Code != 0 {
		t.Fatalf("setup trash list: %s", r)
	}
	var names []string
	for _, s := range decode[trashOut](t, r).Setups {
		names = append(names, s.Setup.Name)
	}
	return names
}

// Gone names setups that must never be offered by any source: purged
// (with a tombstone) or in the trash (offered only as a restore).
type Gone struct {
	Purged  []string
	Trashed []string
	// Identities are identity keys only a gone setup used: no proposal
	// may carry one.
	Identities []string
}

// CheckMachine asserts every machine-wide invariant. step names the
// sequence so far for the failure message.
func (m *Machine) CheckMachine(t testing.TB, step string, gone Gone) {
	t.Helper()
	// 1. setup names are unique; nothing gone is listed as live.
	l := m.List(t, "")
	seen := map[string]bool{}
	for _, s := range l.Setups {
		if seen[s.Name] {
			t.Errorf("[%s] setup %q listed twice", step, s.Name)
		}
		seen[s.Name] = true
	}
	for _, n := range append(slices.Clone(gone.Purged), gone.Trashed...) {
		if seen[n] {
			t.Errorf("[%s] setup %q is gone but setup list shows it", step, n)
		}
	}
	trash := m.Trash(t)
	if l.Trashed != len(trash) {
		t.Errorf("[%s] setup list says %d trashed, trash list has %v", step, l.Trashed, trash)
	}
	for _, n := range gone.Purged {
		if slices.Contains(trash, n) {
			t.Errorf("[%s] purged setup %q still in the trash", step, n)
		}
	}
	for _, n := range gone.Trashed {
		if !slices.Contains(trash, n) {
			t.Errorf("[%s] trashed setup %q missing from trash list %v", step, n, trash)
		}
	}
	// 2. import never offers a gone setup and never reads *.migrated.
	r := m.Lore(t, "", "setup", "import-scan", "--json")
	if r.Code != 0 {
		t.Fatalf("[%s] import-scan: %s", step, r)
	}
	if strings.Contains(r.Stdout, MigratedOnlyEmail) {
		t.Errorf("[%s] import-scan read a *.migrated backup: %s", step, r.Stdout)
	}
	sc := decode[scanOut](t, r)
	for _, p := range sc.Proposals {
		for _, n := range append(slices.Clone(gone.Purged), gone.Trashed...) {
			if p.Setup.Name == n || strings.HasPrefix(p.Setup.Name, n+"-") || p.Setup.Identity.Key == "human:"+n+"@example.test" {
				t.Errorf("[%s] import-scan offers gone setup %q as %q (%s)", step, n, p.Setup.Name, p.Setup.Identity.Key)
			}
		}
	}
	for _, p := range sc.Proposals {
		if slices.Contains(gone.Identities, p.Setup.Identity.Key) {
			t.Errorf("[%s] import-scan offers %q with a gone setup's identity %s", step, p.Setup.Name, p.Setup.Identity.Key)
		}
	}
	if strings.Contains(sc.Banner, ".migrated") {
		t.Errorf("[%s] import banner names a backup: %q", step, sc.Banner)
	}
	// 3. config check knows every key lore wrote.
	r = m.Lore(t, "", "config", "check", "--json")
	if r.Code != 0 {
		t.Errorf("[%s] config check: %s", step, r)
	} else {
		for _, w := range decode[checkOut](t, r).Warnings {
			t.Errorf("[%s] config check warns: %s", step, w)
		}
	}
	// 4. doctor --all reports, never errors.
	m.CheckDoctor(t, step)
	// 5. discover answers.
	if r = m.Lore(t, "", "setup", "discover", "--json"); r.Code != 0 {
		t.Errorf("[%s] setup discover: %s", step, r)
	}
	// 6. file modes.
	m.CheckModes(t, step)
}

// doctorExits are doctor --all's documented exits: healthy, degraded, broken.
var doctorExits = []int{0, 1, 2}

// CheckDoctor runs doctor --all --json: a documented exit, a JSON report
// (never an error envelope), no panic.
func (m *Machine) CheckDoctor(t testing.TB, step string) {
	t.Helper()
	r := m.Lore(t, "", "doctor", "--all", "--json")
	if !slices.Contains(doctorExits, r.Code) || strings.Contains(r.Stderr, "panic") || strings.Contains(r.Stderr, "goroutine ") {
		t.Errorf("[%s] doctor --all: %s", step, r)
		return
	}
	var d doctorOut
	if err := json.Unmarshal([]byte(r.Stdout), &d); err != nil || d.Status == "" {
		t.Errorf("[%s] doctor --all printed no report (%v): %s", step, err, r)
	}
}

// Mode rules: a file lore writes in a repo is committed and read by every
// clone (0644, folders 0755); ~/.lore is this machine's and owner-only.
const (
	repoFileBits fs.FileMode = 0o044 // group and others read
	repoDirBits  fs.FileMode = 0o055 // group and others read + enter
	privateBits  fs.FileMode = 0o077 // no group or other access at all
)

// CheckModes checks every file git would commit in the ~/code repos
// (tracked or untracked-not-ignored: what `git add -A` takes) and every
// file under ~/.lore.
func (m *Machine) CheckModes(t testing.TB, step string) {
	t.Helper()
	for _, repo := range m.Repos {
		m.checkRepoModes(t, step, repo)
	}
	m.checkPrivateModes(t, step)
}

func (m *Machine) checkRepoModes(t testing.TB, step, repo string) {
	t.Helper()
	committed := map[string]bool{}
	c := exec.Command("/usr/bin/git", "ls-files", "-co", "--exclude-standard", "-z")
	c.Dir = repo
	c.Env = m.env()
	out, err := c.Output()
	if err != nil { // not a git repo: every file counts
		committed = nil
	}
	for _, f := range strings.Split(string(out), "\x00") {
		if f == "" {
			continue
		}
		committed[filepath.Join(repo, f)] = true
		for d := filepath.Dir(filepath.Join(repo, f)); d != repo && d != "."; d = filepath.Dir(d) {
			committed[d] = true
		}
	}
	_ = filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Errorf("[%s] walk %s: %v", step, p, err)
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		if p == repo || (committed != nil && !committed[p]) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		perm := info.Mode().Perm()
		switch {
		case d.IsDir() && perm&repoDirBits != repoDirBits:
			t.Errorf("[%s] repo folder %s is %#o, want 0755", step, rel(m.Home, p), perm)
		case info.Mode().IsRegular() && perm&repoFileBits != repoFileBits:
			t.Errorf("[%s] repo file %s is %#o, want 0644", step, rel(m.Home, p), perm)
		}
		return nil
	})
}

// checkPrivateModes: nothing under ~/.lore is readable by another user.
// A file counts as private when it or a folder above it (up to ~/.lore)
// shuts out group and others.
func (m *Machine) checkPrivateModes(t testing.TB, step string) {
	t.Helper()
	lore := filepath.Join(m.Home, ".lore")
	// A ~/.lore that is the person's own file (not lore's folder) holds
	// nothing of lore's: its mode is theirs.
	if info, err := os.Lstat(lore); err == nil && !info.IsDir() {
		return
	}
	_ = filepath.WalkDir(lore, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // no ~/.lore yet
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if (d.IsDir() || info.Mode().IsRegular()) && !shutOut(lore, p) {
			t.Errorf("[%s] machine-private %s is %#o, want owner-only", step, rel(m.Home, p), info.Mode().Perm())
		}
		return nil
	})
}

// shutOut: p or a folder between it and root has no group/other bits.
func shutOut(root, p string) bool {
	for q := p; ; q = filepath.Dir(q) {
		if info, err := os.Stat(q); err == nil && info.Mode().Perm()&privateBits == 0 {
			return true
		}
		if q == root || q == filepath.Dir(q) {
			return false
		}
	}
}

func rel(home, p string) string {
	if r, err := filepath.Rel(home, p); err == nil {
		return "~/" + r
	}
	return p
}
