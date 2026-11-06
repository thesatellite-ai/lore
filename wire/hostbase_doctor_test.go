package wire

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
)

// fakeLoreDir is a folder holding an executable named lore.
func fakeLoreDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	p := filepath.Join(d, doctorBinaryName)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	execMode := os.FileMode(0o700) // the check under test needs an executable
	if err := os.Chmod(p, execMode); err != nil {
		t.Fatal(err)
	}
	return d
}

// TestLorePathBinaries: every executable named lore in the PATH folders,
// in PATH order and once each; a non-executable file, a folder named lore,
// a missing folder and an empty PATH entry are not binaries.
func TestLorePathBinaries(t *testing.T) {
	t.Parallel()
	mk := func(exec bool, dir bool) string {
		t.Helper()
		d := t.TempDir()
		p := filepath.Join(d, doctorBinaryName)
		switch {
		case dir:
			if err := os.Mkdir(p, 0o750); err != nil {
				t.Fatal(err)
			}
		default:
			mode := os.FileMode(0o600)
			if exec {
				mode = 0o700
			}
			if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	one, two := mk(true, false), mk(true, false)
	noExec, folder := mk(false, false), mk(false, true)
	missing := filepath.Join(t.TempDir(), "gone")
	sep := string(os.PathListSeparator)
	cases := map[string]struct {
		path string
		want []string
	}{
		"empty":                  {"", nil},
		"one":                    {one, []string{filepath.Join(one, doctorBinaryName)}},
		"two in order":           {two + sep + one, []string{filepath.Join(two, doctorBinaryName), filepath.Join(one, doctorBinaryName)}},
		"same folder twice":      {one + sep + one + sep + one + "/", []string{filepath.Join(one, doctorBinaryName)}},
		"not executable skipped": {noExec + sep + one, []string{filepath.Join(one, doctorBinaryName)}},
		"folder named lore":      {folder, nil},
		"missing and empty":      {missing + sep + sep + one, []string{filepath.Join(one, doctorBinaryName)}},
	}
	for name, c := range cases {
		if got := lorePathBinaries(c.path); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}

// doctorBase runs the base with PATH limited to one lore (no PATH finding).
func doctorBase(t *testing.T, a *App, fix bool) opsdoctor.Base {
	t.Helper()
	t.Setenv(pathEnv, filepath.Dir(gitBinary(t)))
	b, err := a.CoreDoctorBase(background(), fix)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// gitBinary is git's path (the tests need it on PATH, and no lore there).
func gitBinary(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// hasLine reports whether any line contains part.
func hasLine(lines []string, part string) bool {
	return slices.ContainsFunc(lines, func(s string) bool { return strings.Contains(s, part) })
}

// TestCoreDoctorBaseIdentityAndPath: the identity section is the write
// identity (stable, no warning); two lore binaries on PATH degrade with the
// list; outside a project the base still answers (identity and PATH only).
func TestCoreDoctorBaseIdentityAndPath(t *testing.T) {
	gitDir := filepath.Dir(gitBinary(t))
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	b := doctorBase(t, a, false)
	if b.Identity.Resolved != testEmail || !b.Identity.Stable || b.Identity.Source == "" {
		t.Errorf("identity = %+v, want %s stable", b.Identity, testEmail)
	}
	if b.Status != opsdoctor.StatusHealthy || len(b.Warnings) != 0 || len(b.Errors) != 0 {
		t.Errorf("fresh project: status %s warnings %v errors %v", b.Status, b.Warnings, b.Errors)
	}
	d1, d2 := fakeLoreDir(t), fakeLoreDir(t)
	t.Setenv(pathEnv, d1+string(os.PathListSeparator)+d2)
	b, err := a.CoreDoctorBase(background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.PathConflicts) != 2 || b.Status != opsdoctor.StatusDegraded || !hasLine(b.Warnings, "multiple lore binaries on PATH") {
		t.Errorf("two binaries: conflicts %v status %s warnings %v", b.PathConflicts, b.Status, b.Warnings)
	}

	t.Setenv(pathEnv, gitDir)
	out := openWith(t, t.TempDir(), Options{})
	b = doctorBase(t, out, false)
	if b.Identity.Resolved != testEmail || b.Status != opsdoctor.StatusHealthy {
		t.Errorf("outside a project: %+v", b)
	}
}

// setMeta merges keys into the checkout's .lore/data/_meta.json.
func setMeta(t *testing.T, repo string, kv map[string]string) {
	t.Helper()
	p := filepath.Join(repo, rowfs.LoreDir, rowfs.DataDir, rowfs.MetaFileName)
	m := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(filepath.Clean(p)); err == nil {
		if err = json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range kv {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		m[k] = b
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorVersionPin: a pin below min_lore_version is an error and a
// broken status; a pin other than this release binary is a note naming
// `lore upgrade`; the pin equal to the binary, a dev build and no pin say
// nothing; LORE_VERSION_PIN wins over the _meta.json pin.
// Break-verified: with VersionBelow's arguments swapped the below-floor
// case reports nothing.
func TestDoctorVersionPin(t *testing.T) {
	t.Setenv(envVersionPin, "")
	cases := []struct {
		name, version, pin, env string
		wantErr, wantNote       bool
	}{
		{"no pin", "0.1.18", "", "", false, false},
		{"pin below floor", "0.1.18", "0.1.10", "", true, false},
		{"pin equals binary", "0.1.19", "0.1.19", "", false, false},
		{"pin equals binary with v", "v0.1.19", "0.1.19", "", false, false},
		{"pin other than binary", "0.1.18", "0.1.19", "", false, true},
		{"dev build", "dev", "0.1.19", "", false, false},
		{"env wins below floor", "0.1.18", "0.1.19", "0.1.11", true, false},
		{"env equals binary", "0.1.20", "0.1.10", "0.1.20", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := gitRepo(t)
			a := openWith(t, repo, Options{Version: c.version})
			withProject(t, a)
			setMeta(t, repo, map[string]string{"min_lore_version": "0.1.18", "lore_version_pin": c.pin})
			t.Setenv(envVersionPin, c.env)
			note, floorErr := a.doctorVersionPin(background())
			if (floorErr != "") != c.wantErr || (note != "") != c.wantNote {
				t.Fatalf("note %q err %q, want note=%v err=%v", note, floorErr, c.wantNote, c.wantErr)
			}
			if c.wantNote && !strings.Contains(note, doctorBinaryName+" upgrade") {
				t.Errorf("note does not name the upgrade: %q", note)
			}
			if c.wantErr {
				b := doctorBase(t, a, false)
				if b.Status != opsdoctor.StatusBroken || !hasLine(b.Errors, "below this project's min_lore_version") {
					t.Errorf("base: status %s errors %v, want broken", b.Status, b.Errors)
				}
			}
		})
	}
}

// TestDoctorCIPins: a workflow (.yml or .yaml) pinning lore below the
// floor warns with file:line; a pin at or above it, a file outside
// .github/workflows and no floor say nothing.
func TestDoctorCIPins(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	wf := filepath.Join(repo, filepath.FromSlash(workflowsDir))
	if err := os.MkdirAll(wf, 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(wf, "low.yml"), "jobs:\n  a:\n    env:\n      LORE_VERSION_PIN: 0.1.12\n")
	write(filepath.Join(wf, "low2.yaml"), "env:\n  LORE_VERSION_PIN: \"0.1.9\"\n")
	write(filepath.Join(wf, "ok.yml"), "env:\n  LORE_VERSION_PIN: 0.1.18\n")
	write(filepath.Join(repo, "other.yml"), "env:\n  LORE_VERSION_PIN: 0.1.1\n")
	var warns []string
	collectWarn := func(s string) { warns = append(warns, s) }
	a.doctorCIPins(background(), collectWarn)
	if len(warns) != 0 {
		t.Errorf("no floor: warns %v, want none", warns)
	}
	setMeta(t, repo, map[string]string{"min_lore_version": "0.1.18"})
	a.doctorCIPins(background(), collectWarn)
	if len(warns) != 2 || !hasLine(warns, ".github/workflows/low.yml:4 pins lore 0.1.12") || !hasLine(warns, ".github/workflows/low2.yaml:2 pins lore 0.1.9") {
		t.Errorf("warns = %v, want low.yml:4 and low2.yaml:2", warns)
	}
}

// TestDoctorClaudeLocal: one import line is quiet; more lines, a git-tracked
// file and a symlink each warn; no file is quiet.
func TestDoctorClaudeLocal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		setup   func(t *testing.T, repo string)
		want    string
		noWarns bool
	}{
		{"no file", func(*testing.T, string) {}, "", true},
		{"one import line", func(t *testing.T, repo string) { writeLocal(t, repo, "@.lore/LORE.md\n") }, "", true},
		{"two lines", func(t *testing.T, repo string) { writeLocal(t, repo, "@.lore/LORE.md\nnotes here\n") }, "more than the one import line", false},
		{"tracked", func(t *testing.T, repo string) {
			writeLocal(t, repo, "@.lore/LORE.md\n")
			gitIn(t, repo, nil, "add", claudeLocalFile)
		}, "is tracked by git", false},
		{"symlink", func(t *testing.T, repo string) {
			target := filepath.Join(t.TempDir(), "real.md")
			if err := os.WriteFile(target, []byte("x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(repo, claudeLocalFile)); err != nil {
				t.Fatal(err)
			}
		}, "is a symlink", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			repo := gitRepo(t)
			a := openWith(t, repo, Options{})
			withProject(t, a)
			c.setup(t, repo)
			var warns []string
			a.doctorClaudeLocal(background(), func(s string) { warns = append(warns, s) })
			if c.noWarns {
				if len(warns) != 0 {
					t.Errorf("warns = %v, want none", warns)
				}
				return
			}
			if !hasLine(warns, c.want) {
				t.Errorf("warns = %v, want %q", warns, c.want)
			}
		})
	}
}

// writeLocal writes the checkout's CLAUDE.local.md.
func writeLocal(t *testing.T, repo, s string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, claudeLocalFile), []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorSecretRulesOff: a rule switched off is a note naming the level,
// the reason and the command that turns it back on; with none off, none.
func TestDoctorSecretRulesOff(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	var notes []string
	a.doctorSecretRulesOff(background(), func(s string) { notes = append(notes, s) })
	if len(notes) != 0 {
		t.Fatalf("no rule off: notes %v", notes)
	}
	if _, err := run(t, a, opssettings.OpSecretRuleOff, `{"id":"generic-api-key","reason":"fixture noise"}`, true); err != nil {
		t.Fatal(err)
	}
	a.doctorSecretRulesOff(background(), func(s string) { notes = append(notes, s) })
	if len(notes) != 1 || !strings.Contains(notes[0], "generic-api-key is off at repo (fixture noise)") ||
		!strings.Contains(notes[0], "lore secrets rule on generic-api-key --to repo") {
		t.Errorf("notes = %v", notes)
	}
}

// TestDoctorUnpushedStores: a configured store whose clone holds a local
// branch commit its tracking ref lacks is one warning, with the url
// redacted (tests run without a TTY: quiet output) and never named; once
// the tracking ref holds it, nothing; a store not cloned here, nothing.
func TestDoctorUnpushedStores(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	url := "https://example.com/agency/notes"
	if err := os.WriteFile(orgcfg.ConfigPath(home), []byte("orgs:\n  - name: agency\n    stores:\n      - url: "+url+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := openWith(t, t.TempDir(), Options{Home: home})
	var warns []string
	collectWarn := func(s string) { warns = append(warns, s) }
	a.doctorUnpushedStores(background(), collectWarn)
	if len(warns) != 0 {
		t.Fatalf("not cloned: warns %v", warns)
	}
	clone := orgcfg.Clones{Home: home}.Dir("example.com/agency/notes")
	if err := os.MkdirAll(filepath.Dir(clone), 0o750); err != nil {
		t.Fatal(err)
	}
	gitIn(t, filepath.Dir(clone), nil, "init", "-q", "--bare", clone)
	writeLiveRef(t, clone, "refs/heads/main", map[string]string{"lore-store.yaml": "version: 1\n"})
	a.doctorUnpushedStores(background(), collectWarn)
	if len(warns) != 1 || !strings.Contains(warns[0], "unpushed store work (refs/heads/main)") {
		t.Fatalf("warns = %v, want one naming refs/heads/main", warns)
	}
	if strings.Contains(warns[0], "example.com") || !strings.HasPrefix(warns[0], redactedText) {
		t.Errorf("quiet warning names the store: %q", warns[0])
	}
	head := gitIn(t, clone, nil, "rev-parse", "refs/heads/main")
	gitIn(t, clone, nil, "update-ref", "refs/remotes/origin/main", head)
	warns = nil
	a.doctorUnpushedStores(background(), collectWarn)
	if len(warns) != 0 {
		t.Errorf("pushed: warns %v, want none", warns)
	}
}
