package host

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// dataIgnoreRepo is a git checkout with a lore project at its top.
func dataIgnoreRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(root, ".lore", "data", "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeIgnore(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestEnsureDataNotIgnored: OWNERQ-9 / G-DECIDE-9 over the ignore rules a
// repo really has: which ones the re-include block fixes (and only then
// .gitignore changes), which stay ignored with their rule named, and that
// an edit that helped nothing is undone byte for byte.
func TestEnsureDataNotIgnored(t *testing.T) {
	cases := []struct {
		name      string
		files     map[string]string // repo-relative path -> content
		changed   bool
		stillHas  string // substring of one still-ignored line; "" = none
		gitignore string // "" = .gitignore must equal its original (or stay absent)
	}{
		{name: "nothing ignored", files: map[string]string{".gitignore": "node_modules/\n"}},
		{name: "*.json at the top", files: map[string]string{".gitignore": "*.json\n"}, changed: true, gitignore: "!.lore/data/**"},
		{name: "_* hides _meta.json", files: map[string]string{".gitignore": "_*\n"}, changed: true, gitignore: "!.lore/data/**"},
		{name: "snapshots/ folder rule", files: map[string]string{".gitignore": "snapshots/\n"}, changed: true, gitignore: "!.lore/data/**"},
		{name: "rule only in info/exclude: .gitignore created", files: map[string]string{".git/info/exclude": "*.json\n"}, changed: true, gitignore: "!.lore/data/**"},
		{name: "whole .lore/ excluded: edit undone", files: map[string]string{".gitignore": "# mine\n.lore/\n"}, stillHas: ".gitignore:2:.lore/"},
		{name: "whole .lore/ in exclude: no .gitignore made", files: map[string]string{".git/info/exclude": ".lore/\n"}, stillHas: "exclude:1:.lore/"},
		{name: "nested rule wins over the block: edit undone", files: map[string]string{".lore/data/tasks/.gitignore": "*.json\n"}, stillHas: "tasks/sample.json (ignored by .lore/data/tasks/.gitignore:1:*.json)"},
		{name: "negation already there", files: map[string]string{".gitignore": "*.json\n!.lore/data/**\n"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := dataIgnoreRepo(t)
			for p, body := range c.files {
				writeIgnore(t, root, p, body)
			}
			gi := filepath.Join(root, ".gitignore")
			before, berr := os.ReadFile(gi)
			changed, still, err := EnsureDataNotIgnored(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if changed != c.changed {
				t.Fatalf("changed = %v, want %v (still %v)", changed, c.changed, still)
			}
			joined := strings.Join(still, "\n")
			if (c.stillHas == "") != (len(still) == 0) || !strings.Contains(joined, c.stillHas) {
				t.Fatalf("still ignored:\n%s\nwant one containing %q", joined, c.stillHas)
			}
			after, aerr := os.ReadFile(gi)
			if c.gitignore == "" {
				if (berr == nil) != (aerr == nil) || string(before) != string(after) {
					t.Fatalf(".gitignore changed:\n%q\n->\n%q", before, after)
				}
				return
			}
			if !strings.Contains(string(after), c.gitignore) {
				t.Fatalf(".gitignore lacks %q:\n%s", c.gitignore, after)
			}
			// Run twice: the block is never added a second time.
			if again, _, _ := EnsureDataNotIgnored(context.Background(), root); again {
				t.Fatal("a second run changed .gitignore again")
			}
		})
	}
}

// TestSyncRefusesIgnoredData: `lore sync` fails closed with
// E_SYNC_DATA_IGNORED (exit 23) while a row file stays ignored, also with
// LORE_SYNC_GIT=0 (checked, .gitignore not edited), and passes once fixed.
func TestSyncRefusesIgnoredData(t *testing.T) {
	ctx := context.Background()
	for _, off := range []bool{false, true} {
		root := dataIgnoreRepo(t)
		if off {
			t.Setenv(EnvSyncGit, envOff)
		}
		l := liveSync{h: &liveHost{cfg: LiveConfig{WorkDir: root}}}
		writeIgnore(t, root, ".lore/data/tasks/.gitignore", "*.json\n")
		err := l.refuseIgnoredData(ctx)
		if errs.CodeOf(err) != errs.SyncDataIgnored || errs.DefOf(errs.CodeOf(err)).Exit != 23 || !strings.Contains(err.Error(), "nothing was pushed") {
			t.Fatalf("off=%v: got %v", off, err)
		}
		if _, gerr := os.Stat(filepath.Join(root, ".gitignore")); off && gerr == nil {
			t.Fatal("LORE_SYNC_GIT=0 edited .gitignore")
		}
		if err := os.Remove(filepath.Join(root, ".lore/data/tasks/.gitignore")); err != nil {
			t.Fatal(err)
		}
		if err := l.refuseIgnoredData(ctx); err != nil {
			t.Fatalf("off=%v, fixed: %v", off, err)
		}
	}
	// Not a lore project: nothing to check.
	if err := (liveSync{h: &liveHost{cfg: LiveConfig{WorkDir: t.TempDir()}}}).refuseIgnoredData(ctx); err != nil {
		t.Fatal(err)
	}
}
