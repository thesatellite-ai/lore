package wire

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"gopkg.in/yaml.v3"
)

// TestMachineConfigWrite: an edit of a config.yaml that is a link (a chain
// of two, as a dotfiles manager makes) lands in the file at the chain's
// end and every link stays a link; a read-only config.yaml (direct or
// behind the link) is E_BAD_PATH naming it with the chmod that allows the
// write, and keeps its bytes and mode.
func TestMachineConfigWrite(t *testing.T) {
	const body = "identity:\n  name: Pat Example\n"
	edit := func(home string) error {
		return editMachineConfig(home, func(root *yaml.Node) error {
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "note"}, &yaml.Node{Kind: yaml.ScalarNode, Value: "edited"})
			return nil
		})
	}
	t.Run("chain of links", func(t *testing.T) {
		home, dots := t.TempDir(), t.TempDir()
		real := filepath.Join(dots, "lore", "config.yaml")
		cwWrite(t, real, body, 0o600)
		mid := filepath.Join(dots, "config-link.yaml")
		cwLink(t, real, mid)
		cwLink(t, mid, configPath(home))
		if err := edit(home); err != nil {
			t.Fatal(err)
		}
		for link, want := range map[string]string{configPath(home): mid, mid: real} {
			if got, err := os.Readlink(link); err != nil || got != want {
				t.Errorf("%s is no longer a link to %s: %q %v", link, want, got, err)
			}
		}
		if b, _ := os.ReadFile(real); !strings.Contains(string(b), "note: edited") {
			t.Errorf("the edit did not land at the end of the chain:\n%s", b)
		}
	})
	for _, linked := range []bool{false, true} {
		t.Run("read-only linked="+map[bool]string{false: "no", true: "yes"}[linked], func(t *testing.T) {
			home := t.TempDir()
			file := configPath(home)
			if linked {
				file = filepath.Join(t.TempDir(), "config.yaml")
				cwLink(t, file, configPath(home))
			}
			cwWrite(t, file, body, 0o444)
			err := edit(home)
			if !errs.Has(err, errs.BadPath) {
				t.Fatalf("want E_BAD_PATH, got %v", err)
			}
			var e *errs.Error
			if !errors.As(err, &e) || !strings.Contains(e.Message, configPath(home)) || !strings.Contains(e.Hint, "chmod u+w "+configPath(home)) {
				t.Errorf("the refusal does not name the file and the chmod: %v", err)
			}
			if b, _ := os.ReadFile(file); string(b) != body {
				t.Errorf("a read-only config.yaml was rewritten:\n%s", b)
			}
			if info, _ := os.Stat(file); info.Mode().Perm() != 0o444 {
				t.Errorf("config.yaml mode is now %v", info.Mode())
			}
		})
	}
}

func cwWrite(t *testing.T, p, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

func cwLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}
