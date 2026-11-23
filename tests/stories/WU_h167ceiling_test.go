//go:build stories && !windows

package stories

// WU_h167ceiling_test.go — bug hunt H-167, devco D-909 (owner urgent): a
// store folder with 175 long must rules and 61 critical/high hotfixes
// rendered a 146 KB private context over its 32000-byte budget (pinned
// rows were "never cut"), and a tool that caps its instruction files
// loaded only part of it, with rules silently missing.
//
//	D-909/render-stays-under-the-cap: 200 long must rules + 60 critical
//	       hotfixes in an internal store: the private files stay within
//	       context.budget.internal, LORE.md and the private files together
//	       under the 40000-byte ceiling, every pinned row listed (in full or
//	       as one index line), and render says how many of each
//	D-909/doctor-names-the-longest: lore doctor warns that the pinned text
//	       in full is over the budget, naming the 10 longest rows and a
//	       command that runs as printed
//	D-909/for-file-is-complete: lore context --for-file prints every
//	       always-on rule the private file lists as one line, in full
//	D-909/setting-above-the-ceiling-is-clamped: context.budget.internal
//	       100000 is clamped, and render says so

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Names and sizes of the ceiling story (made up).
const (
	whcStore    = "kb"
	whcRules    = 200
	whcHotfixes = 60
	whcBudget   = 32000
	whcCeiling  = 40000
)

// whcDir finds the private context folder render printed.
var whcDir = regexp.MustCompile(`private context → (\S+)`)

// whcIDs finds the pinned row ids of a text.
var whcIDs = regexp.MustCompile(`\b(?:rul|hfx)_[0-9a-f]{32}\b`)

// whcBody is a long must rule body (about 400 bytes). Each body carries
// eight words of its own (svc<i>a … svc<i>h): since D-926 (8f1ab7987)
// render pins one row of two whose bodies share 80% of their words, and
// bodies that differed only in their number were near-duplicates (H-196).
func whcBody(i int) string {
	n := strconv.Itoa(i)
	var own strings.Builder
	for _, c := range "abcdefgh" {
		own.WriteString(" svc" + n + string(c))
	}
	return "Rule " + n + ": every service of the company keeps its request ids, logs in UTC and wraps errors with context; it owns" + own.String() + ". " +
		strings.Repeat("Never print a secret value, never log a token, keep the audit trail. ", 4)
}

// whcPrivate reads the private store files render wrote (index.md aside)
// and returns their text and total size.
func whcPrivate(t *testing.T, m *journeys.Machine, renderOut string) (string, int) {
	t.Helper()
	mm := whcDir.FindStringSubmatch(renderOut)
	if mm == nil {
		t.Fatalf("render named no private context folder:\n%s", renderOut)
	}
	dir := mm[1]
	if strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(m.Home, dir[2:])
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no private files in %s: %v", dir, err)
	}
	var b strings.Builder
	for _, f := range files {
		if filepath.Base(f) == "index.md" {
			continue
		}
		data, rerr := os.ReadFile(f)
		if rerr != nil {
			t.Fatal(rerr)
		}
		b.Write(data)
	}
	return b.String(), b.Len()
}

func TestStoryWUH167ContextCeiling(t *testing.T) {
	t.Parallel()
	s := &Story{World: journeys.NewWorld(t), ID: "WU"}
	kb := s.Remote("github.com/acme-co/kb-store")
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	c := m.Clone(app, "app")
	m.Lore(c.Dir, "init", "--yes").OK(t)
	m.Lore(c.Dir, "store", "init", "--name", whcStore, "--url", kb.SSHURL(), "--yes").OK(t)
	for i := range whcRules {
		m.Lore(c.Dir, "rule", "add", "--to", whcStore, "--severity", "must", "--body", whcBody(i), "--yes").OK(t)
	}
	for i := range whcHotfixes {
		m.Lore(c.Dir, "hotfix", "add", "--to", whcStore, "--severity", "critical", "--title", "Hotfix "+strconv.Itoa(i)+": the deploy job breaks on a cold cache",
			"--body", whcBody(whcRules+i)+" Fix: warm the cache, then rerun the job.", "--yes").OK(t)
	}
	var render teamharness.Result
	var private string

	s.Step("D-909/render-stays-under-the-cap", func() {
		render = m.Lore(c.Dir, "render").OK(t)
		render.Says(t, "as one-line index", "`lore context --for-file <path>` / `lore show --id <id>`").Never(t, "never cut")
		var size int
		private, size = whcPrivate(t, m, render.Out())
		if size > whcBudget {
			t.Fatalf("devco:D-909: the private context is %d bytes, over context.budget.internal %d:\n%s", size, whcBudget, render)
		}
		lore, err := os.ReadFile(filepath.Join(c.Dir, ".lore", "LORE.md"))
		if err != nil {
			t.Fatal(err)
		}
		if size+len(lore) > whcCeiling {
			t.Fatalf("devco:D-909: LORE.md (%d) and the private context (%d) are over the %d-byte ceiling together", len(lore), size, whcCeiling)
		}
		seen := map[string]bool{}
		for _, id := range whcIDs.FindAllString(private, -1) {
			seen[id] = true
		}
		if len(seen) != whcRules+whcHotfixes {
			t.Fatalf("devco:D-909: the private context lists %d pinned rows, want every one of %d", len(seen), whcRules+whcHotfixes)
		}
	})

	s.Step("D-909/doctor-names-the-longest", func() {
		out := m.Lore(c.Dir, "doctor").Out()
		if !strings.Contains(out, "bytes in full, over context.budget.internal") || !strings.Contains(out, "The longest:") {
			t.Fatalf("devco:D-909: doctor does not warn about the pinned text over the budget:\n%s", out)
		}
		_, after, _ := strings.Cut(out, "The longest:")
		if n := strings.Count(after[:strings.Index(after, "fix:")], "\n  - "); n != 10 {
			t.Fatalf("devco:D-909: doctor names %d longest rows, want 10:\n%s", n, out)
		}
		// its demotion runs exactly as printed
		cmd := regexp.MustCompile("`(lore rule edit --id rul_[0-9a-f]+ --severity should)`").FindStringSubmatch(out)
		if cmd == nil {
			t.Fatalf("doctor prints no demotion command:\n%s", out)
		}
		m.Lore(c.Dir, append(strings.Fields(cmd[1])[1:], "--yes")...).OK(t)
	})

	s.Step("D-909/for-file-is-complete", func() {
		out := m.Lore(c.Dir, "context", "--for-file", "main.go").OK(t).Out()
		// every always-on pinned row the private file lists as one line is
		// in full here (the demoted rule above is no longer pinned)
		private, _ = whcPrivate(t, m, m.Lore(c.Dir, "render").OK(t).Out())
		lines := regexp.MustCompile("(?m)^- `((?:rul|hfx)_[0-9a-f]{32})` \\[").FindAllStringSubmatch(private, -1)
		if len(lines) == 0 {
			t.Fatal("the private context lists no row as one line")
		}
		for _, l := range lines {
			if !strings.Contains(out, `id="`+l[1]+`"`) {
				t.Fatalf("devco:D-909: context --for-file does not print %s in full (the private file lists it as one line)", l[1])
			}
		}
		if strings.Contains(out, "more: ") && strings.Count(out, "<rule ")+strings.Count(out, "<hotfix ") < len(lines) {
			t.Fatalf("context --for-file was cut:\n%s", out[len(out)-400:])
		}
	})

	s.Step("D-909/setting-above-the-ceiling-is-clamped", func() {
		m.Lore(c.Dir, "policy", "set", "--key", "context.budget.internal", "--value", "100000", "--yes").OK(t)
		r := m.Lore(c.Dir, "render").OK(t)
		r.Says(t, "context.budget.internal 100000 clamped to")
		_, size := whcPrivate(t, m, r.Out())
		lore, err := os.ReadFile(filepath.Join(c.Dir, ".lore", "LORE.md"))
		if err != nil {
			t.Fatal(err)
		}
		if size+len(lore) > whcCeiling {
			t.Fatalf("devco:D-909: a budget of 100000 loaded %d + %d bytes, over the %d-byte ceiling", size, len(lore), whcCeiling)
		}
	})
}
