package renderhost

// render_stamp_test.go — the stamps of the rendered files
// (render_stamp.go): same inputs give the same stamp and bytes, a changed
// row or setting gives another, and the verdict tells stale, edited and
// missing apart; the pointer files are compared with context.targets.

import (
	"errors"
	"github.com/thesatellite-ai/lore/store"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
)

func stampFixture() ([]pinnedRule, []pinnedHotfix, contextSettings) {
	rules := []pinnedRule{
		{ID: "rul_a", Body: "run the tests", Origin: "repo/rul_a@0001"},
		{ID: "rul_b", Body: "small commits", Origin: "repo/rul_b@0002"},
	}
	hotfixes := []pinnedHotfix{{ID: "hfx_a", Severity: renderHotfixSeverityCritical, Title: "Login", Body: "clear the cache", Origin: "repo/hfx_a@0003"}}
	return rules, hotfixes, contextSettings{Budget: 16000, Profile: "codex", EntryMax: 600, WarnPct: 80, FullFile: true}
}

func TestInputDigestDeterministic(t *testing.T) {
	rules, hotfixes, s := stampFixture()
	want := inputDigest(rules, hotfixes, s)
	// Row order does not matter (the rows are sorted by origin).
	rev := []pinnedRule{rules[1], rules[0]}
	if got := inputDigest(rev, hotfixes, s); got != want {
		t.Errorf("same rows in another order give another digest:\n%s\nvs\n%s", got, want)
	}
	body, _ := buildBudgetedFile("PLACEHOLDER", renderInput{Settings: s, Rules: rules, Hotfixes: hotfixes})
	c1 := renderCanary(want, body)
	c2 := renderCanary(inputDigest(rules, hotfixes, s), body)
	if c1 != c2 || !strings.HasPrefix(c1, "rnd_") || len(c1) != len("rnd_")+stampHexLen {
		t.Errorf("canary not deterministic or not rnd_<16 hex>: %q %q", c1, c2)
	}
	// warn_at decides only --check: not an input.
	s2 := s
	s2.WarnPct = 1
	if inputDigest(rules, hotfixes, s2) != want {
		t.Error("warn_at changed the stamp; it does not change the text")
	}
}

func TestInputDigestChanges(t *testing.T) {
	rules, hotfixes, s := stampFixture()
	base := inputDigest(rules, hotfixes, s)
	edited := append([]pinnedRule(nil), rules...)
	edited[0].Origin = "repo/rul_a@0009" // same id, newer HLC
	added := append(append([]pinnedRule(nil), rules...), pinnedRule{ID: "rul_c", Origin: "repo/rul_c@0004"})
	budget := s
	budget.Budget = 32000
	for name, got := range map[string]string{
		"edited row":   inputDigest(edited, hotfixes, s),
		"added row":    inputDigest(added, hotfixes, s),
		"removed row":  inputDigest(rules[:1], hotfixes, s),
		"other budget": inputDigest(rules, hotfixes, budget),
		"no hotfix":    inputDigest(rules, nil, s),
	} {
		if got == base {
			t.Errorf("%s: the digest did not change", name)
		}
	}
	if renderCanary(base, "a") == renderCanary(base, "b") {
		t.Error("the canary ignores the body")
	}
}

func TestCanaryIn(t *testing.T) {
	if got := canaryIn(canaryLine("rnd_0123456789abcdef") + "body\n"); got != "rnd_0123456789abcdef" {
		t.Errorf("canaryIn = %q", got)
	}
	// the spelling of renders before the self-describing line still reads
	if got := canaryIn("<!-- AICODER:CANARY=rnd_0123456789abcdef SCHEMA=v2 -->\nbody\n"); got != "rnd_0123456789abcdef" {
		t.Errorf("canaryIn (old line) = %q", got)
	}
	if got := canaryLine("rnd_0123456789abcdef"); strings.Contains(got, "AICODER") || !strings.Contains(got, "which render wrote this file") {
		t.Errorf("canaryLine = %q", got)
	}
	if got := canaryIn("no header\n<!-- AICODER:CANARY=rnd_0123456789abcdef SCHEMA=v2 -->\n"); got != "" {
		t.Errorf("canaryIn reads past the first line: %q", got)
	}
}

func TestPrivateStamp(t *testing.T) {
	text := privateHeader + "\n# Private notes\n\nbody\n"
	origins := []string{"me/rul_b@2", "me/rul_a@1"}
	a := withPrivateStamp(text, origins, 8000)
	b := withPrivateStamp(text, []string{"me/rul_a@1", "me/rul_b@2"}, 8000)
	if a != b {
		t.Errorf("same rows in another order give other bytes:\n%s\nvs\n%s", a, b)
	}
	lines := strings.Split(a, "\n")
	if lines[0] != privateHeader || !strings.HasPrefix(lines[1], privateStampPrefix) || strings.Join(lines[2:], "\n") != "# Private notes\n\nbody\n" {
		t.Errorf("stamp not on line 2 after the header:\n%s", a)
	}
	st := privateStampIn(a)
	if len(st) != stampHexLen {
		t.Fatalf("privateStampIn = %q", st)
	}
	for name, other := range map[string]string{
		"newer row":    withPrivateStamp(text, []string{"me/rul_a@1", "me/rul_b@3"}, 8000),
		"other budget": withPrivateStamp(text, origins, 4000),
		"other text":   withPrivateStamp(text+"more\n", origins, 8000),
	} {
		if privateStampIn(other) == st {
			t.Errorf("%s: the stamp did not change", name)
		}
	}
}

// TestCopyVerdict is the stale decision render --check and the doctor make.
func TestCopyVerdict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LORE.md")
	want := canaryLine("rnd_aaaaaaaaaaaaaaaa") + "rules\n"
	if k, err := copyVerdict(path, want, canaryIn); err != nil || k != opsrender.CopyMissing {
		t.Errorf("no file: %q %v, want missing", k, err)
	}
	for _, c := range []struct {
		name, disk string
		want       opsrender.CopyKind
	}{
		{"same bytes", want, ""},
		{"rows changed (other stamp)", canaryLine("rnd_bbbbbbbbbbbbbbbb") + "rules\n", opsrender.CopyStale},
		{"edited by hand (same stamp)", want + "my note\n", opsrender.CopyEdited},
		{"no stamp at all (an old render)", "rules\n", opsrender.CopyStale},
	} {
		if err := os.WriteFile(path, []byte(c.disk), 0o644); err != nil {
			t.Fatal(err)
		}
		if k, err := copyVerdict(path, want, canaryIn); err != nil || k != c.want {
			t.Errorf("%s: verdict %q (%v), want %q", c.name, k, err, c.want)
		}
	}
	// Without a stamp reader any difference is stale.
	if err := os.WriteFile(path, []byte(want+"x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if k, _ := copyVerdict(path, want, nil); k != opsrender.CopyStale {
		t.Errorf("unstamped file that differs: %q, want stale", k)
	}
}

func TestStaleError(t *testing.T) {
	if staleError(nil) != nil {
		t.Error("no findings, still an error")
	}
	err := staleError([]opsrender.CopyFinding{{Kind: opsrender.CopyStale, File: LoreMDRel, Message: copyMessage(opsrender.CopyStale, LoreMDRel, "the summary agents load"), Fix: renderFixCommand}})
	if err == nil {
		t.Fatal("a stale file passes")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.Refused || !strings.Contains(e.Message, LoreMDRel+" is stale") || !strings.Contains(e.Hint, "lore render") {
		t.Errorf("want E_REFUSED naming %s and the hint lore render, got %v (%+v)", LoreMDRel, err, e)
	}
}

func TestPointerCopies(t *testing.T) {
	root := t.TempDir()
	reg, err := toolprofile.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	claude, _ := reg.Lookup(toolprofile.NameClaudeCode)
	codex, ok := reg.Lookup("codex")
	if !ok {
		t.Fatal("no codex profile")
	}
	// codex's AGENTS.md carries the pointer, but only claude_code is listed
	// and CLAUDE.md does not exist yet.
	agents := codex.StitchPointer("# mine\n", toolprofile.Link{FromPointer: LoreMDRel, FromRoot: LoreMDRel})
	if err := os.WriteFile(filepath.Join(root, codex.PointerFile), []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	sel, err := reg.Select([]string{toolprofile.NameClaudeCode}, rootProbe{root: root})
	if err != nil {
		t.Fatal(err)
	}
	pick := targetPick{sel: sel, targets: []contextTarget{{profile: claude, file: claude.PointerFile, known: true}}}
	got := pointerCopies(root, pick)
	kinds := map[opsrender.CopyKind]opsrender.CopyFinding{}
	for _, f := range got {
		kinds[f.Kind] = f
	}
	if f, ok := kinds[opsrender.PointerMissing]; !ok || f.File != claude.PointerFile || f.Fix != renderFixCommand {
		t.Errorf("missing pointer for claude_code not reported: %+v", got)
	}
	if f, ok := kinds[opsrender.PointerOrphan]; !ok || f.File != codex.PointerFile ||
		f.Fix != "lore policy set context.targets claude_code,codex --yes" || !strings.Contains(f.Message, "by hand") {
		t.Errorf("orphan AGENTS.md not reported with the keep fix: %+v", got)
	}
	if data, _ := os.ReadFile(filepath.Join(root, codex.PointerFile)); string(data) != agents {
		t.Error("pointerCopies changed a file")
	}
	// Without a context.targets row nothing is compared.
	if got := pointerCopies(root, targetPick{sel: toolprofile.Selection{Reason: toolprofile.ReasonWired}}); len(got) != 0 {
		t.Errorf("no row, findings anyway: %+v", got)
	}
}

// TestLocalCopyMessage: a machine-local copy that is missing reads "not
// rendered yet on this machine" (B3-25); stale keeps the shared sentence.
func TestLocalCopyMessage(t *testing.T) {
	t.Parallel()
	if got := localCopyMessage(opsrender.CopyMissing, "CLAUDE.local.md", "the import lines"); got != "CLAUDE.local.md is not rendered yet on this machine (the import lines)" {
		t.Errorf("missing: %q", got)
	}
	if got := localCopyMessage(opsrender.CopyStale, "x.md", "the index"); got != copyMessage(opsrender.CopyStale, "x.md", "the index") {
		t.Errorf("stale: %q", got)
	}
}

// B4-17: a finding about a store without its sto_ id yet names the store,
// not a name- file render never writes once `lore fix` gave it the id.
func TestPrivateSourcesOf(t *testing.T) {
	p := &privatePlan{stores: []privateStore{{stem: privateNoIDPrefix + "acme", sources: []store.Source{"acme", "acme/org"}}}}
	if got := p.sourcesOf(privateNoIDPrefix + "acme"); got != "acme, acme/org" {
		t.Fatalf("sourcesOf = %q", got)
	}
	if got := p.sourcesOf(privateNoIDPrefix + "me"); got != "me" {
		t.Fatalf("unknown stem = %q", got)
	}
}
