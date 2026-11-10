//go:build teame2e

package teame2e

// later.md 607 (b11-sync), the half of v0.1.17 TestE2E_DoctorAndStatus
// that TestTeE_DoctorInTeammateClone left: the doctor [sync] section on
// cmd/lore, which P-DOCTOR-1 fills from the row files and git only
// (wire/hostbase_doctor_sync.go), seen in a teammate's own clone, plus the
// `lore sync status --json` envelope.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// finSyncDoctor is the part of `lore doctor --json` this test reads. The
// sync block is decoded as raw keys too, so a lore.db-only key (baselined,
// pending_exports, trash_rows) the binary has no source for is caught.
type finSyncDoctor struct {
	Status string `json:"status"`
	Exit   int    `json:"exit_status"`
	Sync   *struct {
		DataDirPresent bool `json:"data_dir_present"`
		TrackedFiles   int  `json:"tracked_files"`
		OpenConflicts  int  `json:"open_conflicts"`
		Uncommitted    int  `json:"uncommitted_files"`
		DataIgnored    bool `json:"data_dir_gitignored"`
		MergeDriverOK  bool `json:"merge_driver_configured"`
		InGitRepo      bool `json:"in_git_repo"`
		FileErrors     []struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
		} `json:"file_errors"`
	} `json:"sync"`
	RawSync map[string]json.RawMessage `json:"-"`
}

// finSyncDecode reads one doctor --json report.
func finSyncDecode(t *testing.T, res th.Result) finSyncDoctor {
	t.Helper()
	var rep finSyncDoctor
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil || rep.Sync == nil {
		t.Fatalf("doctor --json prints one report with a [sync] section: %v %s", err, res)
	}
	var raw struct {
		Sync map[string]json.RawMessage `json:"sync"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &raw); err != nil {
		t.Fatal(err)
	}
	rep.RawSync = raw.Sync
	return rep
}

func TestFinSync_DoctorSyncSectionInTeammateClone(t *testing.T) {
	w := newTeamWorld(t)
	r := w.Remote(slugLoose)
	bob := w.Human("bob", "bob@solo.example")
	bob.WriteConfig(teESoloConfig("human:bob@solo.example", "Bob Roe", "bob"))
	carol := w.Human("carol", "carol@solo.example")
	carol.WriteConfig(teESoloConfig("human:carol@solo.example", "Carol Poe", "carol"))
	bc := bob.Clone(r, "tool")
	id := teEInitPushed(t, bc, "x")
	// Only the binary under test is on PATH (as in
	// TestTeE_DoctorInTeammateClone): a developer's own lore there would
	// make doctor warn about two binaries and the pre-commit hook refuse.
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	cc := carol.Clone(bc.Remote, "tool").With("PATH=" + strings.Join([]string{filepath.Join(w.Root, "shim"), filepath.Dir(git), "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	cc.MustLore("rule", "list")

	// A fresh teammate clone: wired by her first command, everything
	// committed, healthy.
	res := cc.Lore("doctor", "--json")
	rep := finSyncDecode(t, res)
	s := rep.Sync
	if res.Code != 0 || rep.Status != "healthy" || rep.Exit != 0 {
		t.Fatalf("a fresh teammate clone is healthy: %s", res)
	}
	if !s.DataDirPresent || !s.InGitRepo || !s.MergeDriverOK || s.DataIgnored || s.TrackedFiles == 0 || s.Uncommitted != 0 || s.OpenConflicts != 0 || len(s.FileErrors) != 0 {
		t.Fatalf("fresh clone [sync] = %+v", *s)
	}
	for _, k := range []string{"baselined", "pending_exports", "trash_rows", "errors", "dangling_references", "duplicate_groups"} {
		if _, ok := rep.RawSync[k]; ok {
			t.Fatalf("[sync] carries lore.db key %q this binary has no source for: %s", k, res.Stdout)
		}
	}

	// Uncommitted work is informational: still healthy, counted.
	cc.MustLore("memory", "add", "--body", "carol's note")
	res = cc.Lore("doctor", "--json")
	rep = finSyncDecode(t, res)
	if res.Code != 0 || rep.Status != "healthy" || rep.Sync.Uncommitted == 0 {
		t.Fatalf("uncommitted row files are counted, not an error: %s", res)
	}

	// A broken row file fails doctor (exit 2) and is the one file error,
	// named by its repo-relative path (the backend's Problem place; v0.1.17
	// named it relative to .lore/data) with kind invalid-file.
	file := dataDir + "/" + tblRules + "/" + id + ".json"
	cc.WriteFile(file, "{broken")
	res = cc.Lore("doctor", "--json")
	rep = finSyncDecode(t, res)
	if res.Code != 2 || rep.Status != "broken" || len(rep.Sync.FileErrors) != 1 || rep.Sync.FileErrors[0].Path != file || rep.Sync.FileErrors[0].Kind != "invalid-file" {
		t.Fatalf("a broken row file is the one [sync] file error %s (exit 2): %s", file, res)
	}

	var st struct {
		Kind string `json:"kind"`
	}
	out := cc.MustLore("sync", "status", "--json")
	if err := json.Unmarshal([]byte(out.Stdout), &st); err != nil || st.Kind != "sync.status" {
		t.Fatalf("sync status --json envelope: %v %s", err, out)
	}
}
