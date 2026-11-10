//go:build machines && !windows

package machines

// macos_test.go — env_macos (shape_macos.go): on a Mac-shaped machine every
// doctor and sync answer is clean. Finder's clutter ignored by the global
// core.excludesFile is not "lore data is gitignored" and not an
// uncommitted lore file, and the repo answers the same from either case
// of its path. The command sweep runs on the shape too (EveryShape).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// macSetup is the setup whose folder is registered in the other case.
const macSetup = "mac"

// macDoctor is the part of doctor --json this test reads.
type macDoctor struct {
	Status   string   `json:"status"`
	Warnings []string `json:"warnings"`
	Errors   []string `json:"errors"`
	Sync     *struct {
		DataIgnored bool `json:"data_dir_gitignored"`
		Uncommitted int  `json:"uncommitted_files"`
		Tracked     int  `json:"tracked_files"`
	} `json:"sync"`
}

func TestEnvMacOS(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeEnvMacOS, loreBin)
	t.Logf("case-insensitive file system: %v (false: the other-case path is a symlink)", m.MacCaseFold)
	m.CheckMachine(t, ShapeEnvMacOS+"/as built", Gone{})
	repo := m.Repos[MacRepo]

	// The shape bites: git ignores the clutter only through the global
	// excludes file, and the work tree is otherwise committed and clean.
	if ign := m.mustGit(t, repo, "status", "--porcelain", "--ignored", "--", ".lore/data"); !strings.Contains(ign, "!! .lore/data/.DS_Store") || !strings.Contains(ign, "!! .lore/data/._foo") {
		t.Fatalf("setup: the global excludes file does not ignore the clutter: %q", ign)
	}
	for _, key := range macJunkRepos {
		dir := m.Repos[key]
		m.mustGit(t, dir, "add", "-A")
		m.mustGit(t, dir, "-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", "init")
		if st := m.mustGit(t, dir, "status", "--porcelain"); st != "" {
			t.Fatalf("setup: %s is not clean after commit: %q", key, st)
		}
	}

	// The setup's folder is registered in the other case of the path.
	csNewSetup(t, m, macSetup, PersonEmail)
	ans, _ := json.Marshal(map[string]any{"folders": []string{envFolderGlob(MacRepoOtherCase)}, "default": false})
	csOK(t, m.Lore(t, "", "setup", "apply", macSetup, "--step", "folders", "--answers", string(ans), "--json"))

	for _, key := range []string{MacRepo, MacRepoOtherCase, RepoLore, RepoWorkApp} {
		dir := m.Repos[key]
		step := ShapeEnvMacOS + "/in " + key
		if here := m.List(t, dir).Active; (key == MacRepo || key == MacRepoOtherCase) && here.Name != macSetup {
			t.Errorf("[%s] the active setup is %q (%s), want %s (its folder is ~/%s)", step, here.Name, here.Why, macSetup, MacRepoOtherCase)
		}
		r := m.Lore(t, dir, "doctor", "--json")
		var d macDoctor
		if err := json.Unmarshal([]byte(r.Stdout), &d); err != nil || d.Sync == nil {
			t.Fatalf("[%s] doctor printed no report with a [sync] section (%v): %s", step, err, r)
		}
		if d.Status != "healthy" || len(d.Warnings) > 0 || len(d.Errors) > 0 || d.Sync.DataIgnored || d.Sync.Uncommitted != 0 || d.Sync.Tracked == 0 {
			t.Errorf("[%s] doctor is not clean: %s", step, r)
		}
		envDoctorOnly(t, m, dir, "in "+key)
		for _, args := range [][]string{{"sync", "status", "--json"}, {"sync", "run", "--json"}} {
			r := m.Lore(t, dir, args...)
			if r.Code != 0 || strings.Contains(r.Stdout+r.Stderr, "E_SYNC_DATA_IGNORED") || strings.Contains(r.Stdout+r.Stderr, "gitignored") {
				t.Errorf("[%s] %s: %s", step, strings.Join(args, " "), r)
			}
		}
	}
	m.CheckDoctorNames(t, ShapeEnvMacOS+"/doctor --all", false)
	envDoctorOnly(t, m, "", "doctor in home")
	m.CheckMachine(t, ShapeEnvMacOS+"/after doctor and sync", Gone{})

	// lore neither commits nor removes the clutter (it is the person's).
	for _, key := range macJunkRepos {
		for _, rel := range macRepoJunk {
			dir := m.Repos[key]
			if out := m.mustGit(t, dir, "ls-files", "--", rel); out != "" {
				t.Errorf("clutter %s/%s is tracked: %q", key, rel, out)
			}
			if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
				t.Errorf("clutter %s/%s is gone: %v", key, rel, err)
			}
		}
	}
}
