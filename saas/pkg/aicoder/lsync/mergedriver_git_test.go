package lsync

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/row"
)

// gitEnv is a fixed identity and no user/system config.
var gitEnv = []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
	"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x"}

// gitRun runs one git command (built with literal arguments) in dir.
func gitRun(t *testing.T, dir string, cmd *exec.Cmd, extraEnv ...string) {
	t.Helper()
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), gitEnv...), extraEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", cmd.Args, err, out)
	}
}

// coreDoc is a 0.1.18 core row file (`_v: 2`, canonical bytes, one key per
// line as the core writes it) with fields over a fixed base.
func coreDoc(t *testing.T, table, id, hlcText string, fields map[string]any) string {
	t.Helper()
	doc := map[string]any{"_v": 2, "_table": table, "id": id, "_hlc": hlcText}
	for k, v := range fields {
		doc[k] = v
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// TestMergeDriver_GitBranchesEditDifferentFields is the sync.sh scenario
// end to end through git: a core-owned row (a memory and a rule) is edited
// on two branches, each in a different field. Every edit also moves _hlc
// and updated_at, so git's line merge alone conflicts; git runs the merge
// driver (the real MergeFiles via this test binary). The merge must be
// clean, keep both edits, stay a core document (_v 2, decodable by the
// core), and carry an HLC at or above both sides, so the next core write
// (an archive, Expect = that HLC) is not stale.
func TestMergeDriver_GitBranchesEditDifferentFields(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const (
		hBase = "2026-11-02T10:00:00.000Z/0000/act_01a10237c9e87e4c843d8045973a65a1"
		hMain = "2026-11-02T10:00:05.000Z/0000/act_01a10237c9e87e4c843d8045973a65a1"
		hFeat = "2026-11-02T10:00:09.000Z/0000/act_01a10237c9e87e4c843d8045973a65a2"
	)
	cases := []struct {
		table, id      string
		base           map[string]any
		mainEd, featEd map[string]any
	}{
		{"memories", "mem_01a10237c9e87e4c843d8045973a65d1",
			map[string]any{"body": "The client replies on WhatsApp.", "kind": "retrieved", "source_kind": "manual", "updated_at": "2026-11-02T10:00:00Z"},
			map[string]any{"body": "The client replies faster on WhatsApp than by email.", "updated_at": "2026-11-02T10:00:05Z"},
			map[string]any{"kind": "core", "updated_at": "2026-11-02T10:00:09Z"}},
		{"rules", "rul_01a10237c9e87e4c843d8045973a65d2",
			map[string]any{"activation": "always", "body": "Buttons are blue.", "severity": "should", "updated_at": "2026-11-02T10:00:00Z"},
			map[string]any{"body": "Buttons are blue, links are green.", "updated_at": "2026-11-02T10:00:05Z"},
			map[string]any{"activation": "manual", "severity": "must", "updated_at": "2026-11-02T10:00:09Z"}},
	}
	for _, c := range cases {
		t.Run(c.table, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			gitRun(t, dir, exec.Command("git", "init", "-q", "-b", "main"))
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = root.Close() }) // test temp dir
			// the driver is this test binary (TestMain → driverHelper)
			cfg, err := root.ReadFile(".git/config")
			if err != nil {
				t.Fatal(err)
			}
			cfg = append(cfg, []byte("[merge \"lore\"]\n\tdriver = "+self+" %O %A %B %P\n")...)
			if err = root.WriteFile(".git/config", cfg, 0o600); err != nil {
				t.Fatal(err)
			}
			if err = root.WriteFile(".gitattributes", []byte(".lore/data/**/*.json merge=lore\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			rel := ".lore/data/" + c.table + "/" + c.id + ".json"
			if err = root.MkdirAll(filepath.Dir(rel), 0o750); err != nil {
				t.Fatal(err)
			}
			write := func(h string, ed map[string]any) {
				f := map[string]any{}
				for k, v := range c.base {
					f[k] = v
				}
				for k, v := range ed {
					f[k] = v
				}
				if err = root.WriteFile(rel, []byte(coreDoc(t, c.table, c.id, h, f)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write(hBase, nil)
			gitRun(t, dir, exec.Command("git", "add", "-A"))
			gitRun(t, dir, exec.Command("git", "commit", "-qm", "base"))
			gitRun(t, dir, exec.Command("git", "checkout", "-q", "-b", "feature"))
			write(hFeat, c.featEd)
			gitRun(t, dir, exec.Command("git", "commit", "-qam", "feature edit"))
			gitRun(t, dir, exec.Command("git", "checkout", "-q", "main"))
			write(hMain, c.mainEd)
			gitRun(t, dir, exec.Command("git", "commit", "-qam", "main edit"))
			gitRun(t, dir, exec.Command("git", "merge", "--no-edit", "feature"), envDriverHelper+"=1")
			raw, err := root.ReadFile(rel)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "<<<<<<<") {
				t.Fatalf("markers left: %s", raw)
			}
			r, err := row.Decode(raw)
			if err != nil {
				t.Fatalf("the merged file is not a core row: %v\n%s", err, raw)
			}
			for _, ed := range []map[string]any{c.mainEd, c.featEd} {
				for k, v := range ed {
					if k == "updated_at" {
						continue
					}
					if got := r.Fields[k]; got == nil || got.String() != v {
						t.Errorf("%s lost: got %v, want %v\n%s", k, got, v, raw)
					}
				}
			}
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			if doc["_v"] != float64(2) {
				t.Errorf("_v = %v, want 2", doc["_v"])
			}
			for _, side := range []string{hMain, hFeat} {
				h, err := hlc.Parse(side)
				if err != nil {
					t.Fatal(err)
				}
				if r.HLC.Compare(h) < 0 {
					t.Errorf("merged HLC %v is below side %v: the next write would be stale", r.HLC, h)
				}
			}
		})
	}
}
