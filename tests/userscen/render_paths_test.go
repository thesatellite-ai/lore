//go:build userscen

package userscen

// render_paths_test.go — where `lore render` may write:
//   - --target names a file inside the repository (a tool file); a path
//     leading out of it (../x.md, an absolute path elsewhere) is
//     E_BAD_PATH before anything is written, on the CLI and over REST;
//   - --out may name any file on the CLI (the person's own choice), but
//     the LORE.full.md written next to it never replaces a file of that
//     name lore did not write (E_BAD_PATH, nothing written);
//   - over REST (`lore api`) an out outside the repository is E_BAD_PATH.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPaths_TargetStaysInRepo(t *testing.T) {
	c := renderSolo(t)
	c.MustLore("rule", "add", "--body", "money is stored in minor units", "--severity", "must")
	outside := t.TempDir()
	for _, target := range []string{"../x.md", filepath.Join(outside, "x.md"), "docs/../../x.md"} {
		before := campTree(t, c.Dir)
		campRefusal(t, c, 20, "E_BAD_PATH", "inside the repository", "render", "--target", target)
		campSameTree(t, "render --target "+target, before, campTree(t, c.Dir))
	}
	if _, err := os.Stat(filepath.Join(outside, "x.md")); !os.IsNotExist(err) {
		t.Errorf("render --target <outside>: nothing written outside the repository (%v)", err)
	}
	// Inside the repository a path, relative or absolute, is still wired.
	for _, target := range []string{"docs/AI.md", filepath.Join(c.Dir, "notes", "AI.md")} {
		r := c.MustLore("render", "--target", target)
		if !strings.Contains(r.Stdout, "pointer → ") {
			t.Errorf("render --target %s: the in-repo file is wired: %s", target, r)
		}
	}
}

func TestRenderPaths_FullFileNeverClobbered(t *testing.T) {
	c := renderSolo(t)
	c.MustLore("rule", "add", "--body", "money is stored in minor units", "--severity", "must")
	dir := t.TempDir()
	mine := filepath.Join(dir, "LORE.full.md")
	if err := os.WriteFile(mine, []byte("my own notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "K.md")
	campRefusal(t, c, 20, "E_BAD_PATH", "LORE.full.md", "render", "--out", out)
	if b, _ := os.ReadFile(mine); string(b) != "my own notes\n" {
		t.Errorf("the person's LORE.full.md is left as it was, got %q", b)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("a refused render writes nothing, not even --out (%v)", err)
	}
	// Without it, --out outside the repository is allowed; a second render
	// over the LORE.full.md lore wrote itself still works.
	if err := os.Remove(mine); err != nil {
		t.Fatal(err)
	}
	c.MustLore("render", "--out", out)
	c.MustLore("rule", "add", "--body", "dates are stored in UTC", "--severity", "must")
	c.MustLore("render", "--out", out)
	if b, _ := os.ReadFile(mine); !strings.Contains(string(b), "dates are stored in UTC") {
		t.Errorf("lore's own LORE.full.md is refreshed, got %q", b)
	}
}

func TestRenderPaths_RESTStaysInRepo(t *testing.T) {
	c := renderSolo(t)
	c.MustLore("rule", "add", "--body", "money is stored in minor units", "--severity", "must")
	srv := renderStartAPI(t, c)
	outside := t.TempDir()
	for _, body := range []string{
		`{"out":"../K.md"}`,
		`{"out":"` + filepath.Join(outside, "K.md") + `"}`,
		`{"target":["../x.md"]}`,
		`{"target":["` + filepath.Join(outside, "x.md") + `"]}`,
	} {
		r := srv.post(t, "/v1/render/run", []byte(body))
		if r.status != 400 || r.code != "E_BAD_PATH" {
			t.Errorf("REST %s: 400 E_BAD_PATH, got %d %s", body, r.status, r.body)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("REST: nothing written outside the repository, got %d files", len(entries))
	}
	if r := srv.post(t, "/v1/render/run", []byte(`{"out":"docs/K.md"}`)); r.status != 200 {
		t.Errorf("REST out inside the repository is written, got %d %s", r.status, r.body)
	}
}
