//go:build userscen

package userscen

// Shared fixtures of the doctor area (doctor_*_test.go): one solo person
// with a lore project in a clone of a fake GitHub repo, a PATH that holds
// only the binary under test (a developer's own lore on PATH would make
// doctor warn about two binaries), and a decoder for `lore doctor --json`.

import (
	"encoding/json"
	"github.com/thesatellite-ai/lore/wire"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// doctorSlug is the fake GitHub repo every doctor scenario clones.
const doctorSlug = "github.com/octo-personal/site"

// doctorReport is the part of `lore doctor --json` the scenarios read
// (ops/doctor.Report: schema_version 1 keys plus exit_status).
type doctorReport struct {
	SchemaVersion int    `json:"schema_version"`
	OK            bool   `json:"ok"`
	Status        string `json:"status"`
	DBOK          bool   `json:"db_ok"`
	WALSizeBytes  int64  `json:"wal_size_bytes"`
	Identity      struct {
		Resolved string `json:"resolved"`
		Source   string `json:"source"`
		Stable   bool   `json:"stable"`
	} `json:"identity"`
	PathConflicts []string `json:"path_conflicts"`
	Warnings      []string `json:"warnings"`
	Notes         []string `json:"notes"`
	Errors        []string `json:"errors"`
	BinaryVersion string   `json:"binary_version"`
	Sync          *struct {
		DataDirPresent bool `json:"data_dir_present"`
		TrackedFiles   int  `json:"tracked_files"`
		OpenConflicts  int  `json:"open_conflicts"`
		ProjectFiles   int  `json:"project_files"`
		Uncommitted    int  `json:"uncommitted_files"`
		DataIgnored    bool `json:"data_dir_gitignored"`
		MergeDriverOK  bool `json:"merge_driver_configured"`
		InGitRepo      bool `json:"in_git_repo"`
		FileErrors     []struct {
			Path   string `json:"path"`
			Kind   string `json:"kind"`
			Detail string `json:"detail"`
		} `json:"file_errors"`
	} `json:"sync"`
	Exit int `json:"exit_status"`
}

// doctorDecode decodes one `lore doctor --json` run and checks the
// invariants every report keeps: schema_version 1, ok iff healthy,
// exit_status equal to the process exit and to the status (0/1/2).
func doctorDecode(t *testing.T, res th.Result) doctorReport {
	t.Helper()
	var rep doctorReport
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		t.Fatalf("doctor --json prints one JSON report: %v\n%s", err, res)
	}
	want := map[string]int{"healthy": 0, "degraded": 1, "broken": 2}
	code, ok := want[rep.Status]
	if !ok || rep.SchemaVersion != 1 || rep.Exit != code || res.Code != code || rep.OK != (rep.Status == "healthy") {
		t.Fatalf("report invariants (schema 1, exit_status = process exit = status code, ok iff healthy) broken: %s", res)
	}
	return rep
}

// doctorPerson is one solo person with a lore project (one rule, all
// committed) in their clone of doctorSlug.
type doctorPerson struct {
	w *th.World
	a *th.Actor
	c *th.Clone
	// path is the PATH every command runs with (binary under test + git).
	path string
}

// newDoctorPerson builds the healthy starting point of every scenario.
func newDoctorPerson(t *testing.T) *doctorPerson {
	t.Helper()
	w := newWorld(t)
	a := w.Human("person", "person@example.com")
	p := &doctorPerson{w: w, a: a, path: narrowPath(t, filepath.Join(w.Root, "shim"))}
	a.SetEnv("PATH", p.path)
	p.c = a.Clone(w.Remote(doctorSlug), "site")
	p.c.MustLore("init", "--non-interactive", "--name=site")
	p.c.MustLore("rule", "add", "--body", "Run the tests before you push")
	p.c.CommitAll("lore init")
	return p
}

// doctor runs `lore doctor --json` (plus extra args) in the clone.
func (p *doctorPerson) doctor(t *testing.T, extra ...string) (doctorReport, th.Result) {
	t.Helper()
	res := p.c.Lore(append([]string{"doctor", "--json"}, extra...)...)
	return doctorDecode(t, res), res
}

// path is a file in the clone.
func (p *doctorPerson) file(rel string) string {
	return filepath.Join(p.c.Dir, filepath.FromSlash(rel))
}

// repoCache is the clone's repo cache, under the person's lore home
// (wire.RepoCachePath; never in the work tree).
func (p *doctorPerson) repoCache() string {
	return wire.RepoCachePath(p.a.LorePath(), p.c.Dir)
}

// ruleID is the first rul_ id in out.
var ruleIDRe = regexp.MustCompile(`rul_[0-9a-f]{32}`)

// hasLine reports whether one of lines contains every part.
func hasLine(lines []string, parts ...string) bool {
	for _, l := range lines {
		all := true
		for _, p := range parts {
			if !strings.Contains(l, p) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}
