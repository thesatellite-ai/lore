//go:build journeys && !windows

package journeys

// pinned_render_refresh_test.go — devco:D-921 (bug hunt H-179, owner
// urgent): after a few hundred writes to an internal store an agent read an
// edited must rule's old text and an archived rule from the private render
// (~/.lore/render/<repo>/<store>.md, the file CLAUDE.local.md imports): only
// `lore render` and `lore sync` rewrote it. Now every write refreshes the
// stale private files of the checkout it runs in, from the repo root and
// from a subfolder, with the store named by --to; `lore doctor` then has
// no stale private render to name.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// prRowID matches the rule or hotfix id an add prints.
var prRowID = regexp.MustCompile(`\b((?:rul|hfx)_[0-9a-f]{32})\b`)

func TestJourneyPinnedEditsRefreshPrivateRender(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	nia := w.Persona("Nia Okoro", "nia@acme-co.example")
	lap := nia.Laptop()
	kb := w.Remote("github.com/acme-co/orbit-notes")
	code := lap.Clone(w.Remote("github.com/acme-co/orbit"), "orbit")
	src := filepath.Join(code.Dir, "src")
	if err := os.MkdirAll(src, 0o750); err != nil {
		t.Fatal(err)
	}
	id := func(out string) string {
		m := prRowID.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no row id in:\n%s", out)
		}
		return m[1]
	}
	privateFile := func() string {
		files, _ := filepath.Glob(filepath.Join(lap.Home, ".lore", "render", "*", "*.md"))
		for _, f := range files {
			if filepath.Base(f) != "index.md" {
				return f
			}
		}
		t.Fatalf("no private render file under %s", filepath.Join(lap.Home, ".lore", "render"))
		return ""
	}
	read := func() string {
		b, err := os.ReadFile(filepath.Clean(privateFile()))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	var edit, gone, lower string

	w.Step("day1/pinned-rows-in-an-internal-store-rendered-once", func() {
		lap.LoreIn(code, "init", "--yes").OK(t)
		lap.LoreIn(code, "store", "init", "--name", "kb", "--url", kb.SSHURL(), "--yes").OK(t)
		edit = id(lap.LoreIn(code, "rule", "add", "--to", "kb", "--severity", "must", "--body", "Old rule text: tabs in every Go file", "--yes").OK(t).Out())
		gone = id(lap.LoreIn(code, "rule", "add", "--to", "kb", "--severity", "must", "--body", "Archived rule: call billing from tests", "--yes").OK(t).Out())
		lower = id(lap.LoreIn(code, "hotfix", "add", "--to", "kb", "--severity", "critical", "--title", "queue drain", "--body", "Critical hotfix: drain the queue first", "--yes").OK(t).Out())
		lap.LoreIn(code, "render").OK(t)
		if body := read(); !strings.Contains(body, "Old rule text") || !strings.Contains(body, "Archived rule") {
			t.Fatalf("render left the pinned rows out:\n%s", body)
		}
	})

	w.Step("day1/edit-archive-lower-reach-the-private-render-without-render", func() {
		lap.LoreIn(code, "rule", "edit", "--id", edit, "--body", "New rule text: gofmt every Go file", "--yes").OK(t)
		lap.LoreIn(code, "rule", "archive", "--id", gone, "--yes").OK(t)
		lap.Lore(src, "hotfix", "edit", "--id", lower, "--severity", "low", "--yes").OK(t)
		lap.Lore(src, "rule", "add", "--to", "kb", "--severity", "must", "--body", "Added rule: wrap every error", "--yes").OK(t)
		body := read()
		for _, want := range []string{"New rule text: gofmt every Go file", "Added rule: wrap every error"} {
			if !strings.Contains(body, want) {
				t.Fatalf("private render lacks %q after the write:\n%s", want, body)
			}
		}
		for _, stale := range []string{"Old rule text", "Archived rule"} {
			if strings.Contains(body, stale) {
				t.Fatalf("private render still holds %q after the write:\n%s", stale, body)
			}
		}
		if pinned, _, _ := strings.Cut(body, "Other notes"); strings.Contains(pinned, "Critical hotfix: drain") {
			t.Fatalf("a hotfix lowered to low is still pinned:\n%s", body)
		}
		lap.LoreIn(code, "doctor").Never(t, "private render of an internal store is out of date")
	})
}
