package repoguard

// The docsync gate step, tests/lib/check-docs.sh, runs `ds scan` then
// `ds check`. The scan rewrites .ds/ledger.tsv and refs.tsv and adds a
// block file under .ds/blocks/ for every changed block; the step used to
// put back only the two tsv files, so every run left untracked block files
// that were removed by hand (run:docs:ds-stray-blocks). A gate must leave
// the tree as it found it.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDS is a ds that behaves like the real scan (rewrites the ledger,
// adds a block file) and whose check fails when FAKE_DS_FAIL is set.
const fakeDS = `#!/usr/bin/env bash
case "$1" in
scan) echo "# scanned at a new time" > .ds/ledger.tsv; echo new > .ds/blocks/new-block; echo ref >> .ds/refs.tsv ;;
check) [ -z "${FAKE_DS_FAIL:-}" ] || { echo "1 error"; exit 1; } ;;
esac
`

func TestCheckDocsLeavesDsFolderUnchanged(t *testing.T) {
	t.Parallel()
	script, err := filepath.Abs(filepath.Join(repoRoot, "tests", "lib", "check-docs.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fail := range []bool{false, true} {
		repo := t.TempDir()
		bin := t.TempDir()
		files := map[string]string{
			".ds/ledger.tsv":  "# the committed ledger\n",
			".ds/refs.tsv":    "# the committed refs\n",
			".ds/blocks/old":  "old block\n",
			".ds/config.toml": "x = 1\n",
		}
		for rel, body := range files {
			p := filepath.Join(repo, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(bin, "ds"), []byte(fakeDS), 0o755); err != nil { //nolint:gosec // an executable test double
			t.Fatal(err)
		}
		cmd := exec.Command("bash", script)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "TMPDIR="+t.TempDir())
		if fail {
			cmd.Env = append(cmd.Env, "FAKE_DS_FAIL=1")
		}
		out, err := cmd.CombinedOutput()
		if fail != (err != nil) {
			t.Fatalf("ds check failing=%v, but check-docs.sh returned %v\n%s", fail, err, out)
		}
		if _, serr := os.Stat(filepath.Join(repo, ".ds", "blocks", "new-block")); !os.IsNotExist(serr) {
			t.Errorf("the block file ds scan created was left behind (failing=%v)\n%s", fail, out)
		}
		for rel, want := range files {
			got, rerr := os.ReadFile(filepath.Join(repo, rel))
			if rerr != nil || string(got) != want {
				t.Errorf("%s after the step = %q (%v), want it unchanged %q", rel, got, rerr, want)
			}
		}
	}
	var raw []byte
	raw, err = os.ReadFile(filepath.Join(repoRoot, "Taskfile.lore.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "tests/lib/check-docs.sh") {
		t.Errorf("Taskfile.lore.yml check:docs does not run tests/lib/check-docs.sh")
	}
}
