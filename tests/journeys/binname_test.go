//go:build journeys && !windows

package journeys

// binname_test.go — a lore build behaves the same whatever its file is
// called (devco D-333). The docs examples run with a build named `bin-lt`
// set its store clone up differently from the same build named
// `lore-docs-verify`: no merge driver, `*.json -merge` in the clone's
// attributes and no pre-commit block, so a hand commit of a secret passed.
//
//	the same build copied under two odd names ─▶ each: store init + a task
//	  ─▶ merge driver, .git/info/attributes and every hook file of the
//	     store clone are byte for byte the same (paths aside)
//	  ─▶ a hand commit of a key is blocked in both clones

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Placeholders of this journey.
const (
	jbnName  = "Nia Field"
	jbnEmail = "nia@field.example"
	jbnRepo  = "github.com/field-co/site"
	jbnStore = "github.com/field-co/notes"
	jbnTitle = "Renew the TLS certificate"
	// jbnKey is a made-up AWS access key (split so this file holds no key).
	jbnKey = "AKIA" + "IOSFODNN7EXAMPLE"
)

// jbnNames are the odd names the build is copied under: neither is `lore`
// nor starts with `lore-`.
var jbnNames = []string{"bin-lt", "docs-verify"}

// jbnCopy copies the suite's binary to dir/name.
func jbnCopy(t *testing.T, dir, name string) string {
	t.Helper()
	src, err := os.Open(loreBin)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst := filepath.Join(dir, name)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(out, src); err != nil {
		t.Fatal(err)
	}
	if err = out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}

// jbnSetup runs store init and a task add with bin and returns the store
// clone's git set-up (driver, attributes, hook files; the World's root and
// bin's path replaced by placeholders) and the store clone's folder.
func jbnSetup(t *testing.T, bin string) (string, *teamharness.World, string) {
	t.Helper()
	tw := teamharness.New(t, bin)
	tw.RequireTestClock()
	w := &World{World: tw, T: t, now: JourneyStart}
	m := w.Persona(jbnName, jbnEmail).Laptop()
	app := m.Clone(w.Remote(jbnRepo), "site")
	m.LoreIn(app, "init").OK(t)
	m.LoreIn(app, "store", "init", "notes", w.Remote(jbnStore).SSHURL(), "--yes").OK(t)
	m.LoreIn(app, "task", "add", "--title", jbnTitle, "--to", "notes", "--no-live").OK(t)

	var clone string
	_ = filepath.WalkDir(filepath.Join(m.Home, ".lore", "stores"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "lore-store.yaml" && clone == "" {
			clone = filepath.Dir(p)
		}
		return nil
	})
	if clone == "" {
		t.Fatalf("no store clone under %s", filepath.Join(m.Home, ".lore", "stores"))
	}
	var b strings.Builder
	drv := tw.TryGit(clone, "config", "--get-regexp", `^merge\.lore`)
	b.WriteString("merge config:\n" + drv.Stdout + "\n")
	attrs, _ := os.ReadFile(filepath.Join(clone, ".git", "info", "attributes"))
	b.WriteString("info/attributes:\n" + string(attrs) + "\n")
	hooks, _ := filepath.Glob(filepath.Join(clone, ".git", "hooks", "*"))
	sort.Strings(hooks)
	for _, h := range hooks {
		if strings.HasSuffix(h, ".sample") {
			continue
		}
		body, _ := os.ReadFile(h)
		b.WriteString("hook " + filepath.Base(h) + ":\n" + string(body) + "\n")
	}
	got := strings.ReplaceAll(b.String(), bin, "<bin>")
	return strings.ReplaceAll(got, tw.Root, "<root>"), tw, clone
}

func TestJourneyBinaryNameDoesNotChangeStoreSetup(t *testing.T) {
	t.Parallel()
	if loreBin == "" {
		t.Skip("no lore binary")
	}
	dir := t.TempDir()
	setups := map[string]string{}
	for _, name := range jbnNames {
		setup, tw, clone := jbnSetup(t, jbnCopy(t, dir, name))
		if !strings.Contains(setup, "merge-driver") || !strings.Contains(setup, "merge=lore") || !strings.Contains(setup, "check leaks") {
			t.Fatalf("a build named %s did not set its store clone up for plain git (merge driver, attributes, pre-commit block):\n%s", name, setup)
		}
		setups[name] = setup

		// a hand commit of a key is blocked by the pre-commit block
		tasks, _ := filepath.Glob(filepath.Join(clone, "_store", "tasks", "*.json"))
		if len(tasks) != 1 {
			t.Fatalf("%s: want one task file, got %v", name, tasks)
		}
		raw, err := os.ReadFile(tasks[0])
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(tasks[0], []byte(strings.Replace(string(raw), jbnTitle, jbnTitle+" (key "+jbnKey+")", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(clone, tasks[0])
		tw.Git(clone, "add", rel)
		if r := tw.TryGit(clone, "commit", "-qm", "note the key"); r.Code == 0 || !strings.Contains(r.Stderr+r.Stdout, "E_LEAK_BLOCKED") {
			t.Fatalf("a build named %s: a hand commit of a key in its store clone must be blocked: %s", name, r)
		}
	}
	if a, b := setups[jbnNames[0]], setups[jbnNames[1]]; a != b {
		t.Fatalf("the same build set its store clone up differently under two names:\n--- %s\n%s\n--- %s\n%s", jbnNames[0], a, jbnNames[1], b)
	}
}
