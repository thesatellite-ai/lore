package toolprofile

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// mapProbe is a Probe over a fixed map of file states.
type mapProbe map[string]FileState

func (m mapProbe) State(rel string) FileState { return m[rel] }

func names(ps []Profile) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return strings.Join(out, ",")
}

// TOOL NEUTRAL: wired files win, else detected files, else AGENTS.md —
// never CLAUDE.md by default; a repo already wired into CLAUDE.md keeps it
// and a hand-written AGENTS.md beside it is not wired behind its back.
func TestResolveMatrix(t *testing.T) {
	reg, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		probe  mapProbe
		want   string
		reason Reason
	}{
		{"empty repo", mapProbe{}, NameCodex, ReasonFallback},
		{"claude file present", mapProbe{"CLAUDE.md": FilePresent}, NameClaudeCode, ReasonDetected},
		{"claude + agents present", mapProbe{"CLAUDE.md": FilePresent, "AGENTS.md": FilePresent}, NameClaudeCode + "," + NameCodex, ReasonDetected},
		{"v0.1.17 claude wired, agents hand", mapProbe{"CLAUDE.md": FileWired, "AGENTS.md": FilePresent}, NameClaudeCode, ReasonWired},
		{"agents wired only", mapProbe{"AGENTS.md": FileWired, "GEMINI.md": FilePresent}, NameCodex, ReasonWired},
		{"every tool present", mapProbe{"CLAUDE.md": FilePresent, "AGENTS.md": FilePresent, ".cursor/rules/lore.mdc": FilePresent, "GEMINI.md": FilePresent, ".github/copilot-instructions.md": FilePresent}, "claude_code,codex,cursor,gemini_cli,copilot", ReasonDetected},
	}
	if sel := reg.Resolve(mapProbe{"CLAUDE.md": FileWired, "AGENTS.md": FilePresent}); names(sel.Unwired) != NameCodex {
		t.Fatalf("Unwired = %s, want codex", names(sel.Unwired))
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sel := reg.Resolve(c.probe)
			if got := names(sel.Profiles); got != c.want || sel.Reason != c.reason {
				t.Fatalf("Resolve = %s (%s), want %s (%s)", got, sel.Reason, c.want, c.reason)
			}
			if sel.Reason != ReasonWired && len(sel.Unwired) != 0 {
				t.Fatalf("Unwired is only for ReasonWired: %v", sel.Unwired)
			}
		})
	}
}

func TestBuiltinsTable(t *testing.T) {
	want := map[string]struct {
		file   string
		budget int
	}{
		NameClaudeCode: {"CLAUDE.md", 32000},
		NameCodex:      {"AGENTS.md", 16000},
		NameCursor:     {".cursor/rules/lore.mdc", 16000},
		NameGeminiCLI:  {"GEMINI.md", 32000},
		NameCopilot:    {".github/copilot-instructions.md", 16000},
		NameGeneric:    {"", 16000},
	}
	got := Builtins()
	if len(got) != len(want) {
		t.Fatalf("%d built-ins, want %d", len(got), len(want))
	}
	for _, p := range got {
		w, ok := want[p.Name]
		if !ok || p.PointerFile != w.file || p.EffectiveBudget() != w.budget {
			t.Errorf("%+v, want %+v", p, w)
		}
	}
	reg, _ := New(nil)
	if reg.Fallback().PointerFile != "AGENTS.md" {
		t.Fatal("fallback must be AGENTS.md")
	}
}

func TestUserProfiles(t *testing.T) {
	reg, err := New([]Profile{{Name: "windsurf", PointerFile: "./.windsurfrules", Import: ImportRead}})
	if err != nil {
		t.Fatal(err)
	}
	p, ok := reg.Lookup("windsurf")
	if !ok || p.PointerFile != ".windsurfrules" || p.EffectiveBudget() != DefaultBudget {
		t.Fatalf("user profile = %+v %v", p, ok)
	}
	if q, ok := reg.ByPointerFile(".windsurfrules"); !ok || q.Name != "windsurf" {
		t.Fatal("ByPointerFile misses the user profile")
	}
	// A user profile takes part in detection like a built-in.
	sel := reg.Resolve(mapProbe{".windsurfrules": FilePresent})
	if names(sel.Profiles) != "windsurf" {
		t.Fatalf("detect = %s", names(sel.Profiles))
	}

	bad := []struct {
		p    Profile
		want string
	}{
		{Profile{Name: "Bad-Name", PointerFile: "X.md", Import: ImportRead}, "name must be"},
		{Profile{Name: "", PointerFile: "X.md", Import: ImportRead}, "name must be"},
		{Profile{Name: NameClaudeCode, PointerFile: "X.md", Import: ImportRead}, "already taken"},
		{Profile{Name: "x", PointerFile: "CLAUDE.md", Import: ImportRead}, "already belongs to profile \"claude_code\""},
		{Profile{Name: "x", PointerFile: "X.md", Import: "include"}, "is not one of"},
		{Profile{Name: "x", Import: ImportRead}, "pointer_file is required"},
		{Profile{Name: "x", PointerFile: "../X.md", Import: ImportRead}, "inside the repo"},
		{Profile{Name: "x", PointerFile: "/etc/X.md", Import: ImportRead}, "inside the repo"},
		{Profile{Name: "x", PointerFile: `a\X.md`, Import: ImportRead}, "inside the repo"},
		{Profile{Name: "x", PointerFile: ".lore/X.md", Import: ImportRead}, "inside .lore/"},
		{Profile{Name: "x", PointerFile: "X.md", Import: ImportRead, Budget: -1}, "negative"},
	}
	for _, b := range bad {
		if _, berr := New([]Profile{b.p}); berr == nil || !strings.Contains(berr.Error(), b.want) {
			t.Errorf("New(%+v) = %v, want %q", b.p, berr, b.want)
		}
	}
	// Two user profiles may not share a name or a file either.
	if _, dupErr := New([]Profile{{Name: "a", PointerFile: "A.md", Import: ImportRead}, {Name: "a", PointerFile: "B.md", Import: ImportRead}}); dupErr == nil {
		t.Error("duplicate user name accepted")
	}
	if _, fileErr := New([]Profile{{Name: "a", PointerFile: "A.md", Import: ImportRead}, {Name: "b", PointerFile: "A.md", Import: ImportRead}}); fileErr == nil {
		t.Error("duplicate user pointer file accepted")
	}
	// import none needs no file and stitches nothing.
	reg, err = New([]Profile{{Name: "mine", Import: ImportNone, PointerFile: "ignored.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := reg.Lookup("mine"); p.HasPointerFile() {
		t.Fatal("import none must have no pointer file")
	}
}

func TestPointerBlocks(t *testing.T) {
	link := Link{FromPointer: "../.lore/LORE.md", FromRoot: ".lore/LORE.md"}
	claude, _ := mustReg(t).Lookup(NameClaudeCode)
	// ImportAt keeps v0.1.17's exact bytes, so existing repos see no diff.
	v017 := "<!-- lore:pointer:start -->\n@../.lore/LORE.md\n<!-- if your agent does not support @import, read ../.lore/LORE.md now -->\n<!-- lore:pointer:end -->\n"
	if got := claude.PointerBlock(link); got != v017 {
		t.Fatalf("at block = %q", got)
	}
	codex, _ := mustReg(t).Lookup(NameCodex)
	if got := codex.PointerBlock(link); !strings.Contains(got, "Read `.lore/LORE.md`") || strings.Contains(got, "@") {
		t.Fatalf("read block = %q", got)
	}
	generic, _ := mustReg(t).Lookup(NameGeneric)
	if generic.PointerBlock(link) != "" || generic.HasPointerFile() {
		t.Fatal("generic has no pointer file")
	}

	// Stitch: new file, prepend to hand content, replace in place, idempotent.
	blk := codex.PointerBlock(link)
	if got := codex.StitchPointer("", link); got != blk {
		t.Fatalf("new = %q", got)
	}
	hand := "# notes\n"
	once := codex.StitchPointer(hand, link)
	if once != blk+"\n"+hand {
		t.Fatalf("prepend = %q", once)
	}
	if codex.StitchPointer(once, link) != once {
		t.Fatal("not idempotent")
	}
	stale := strings.Replace(once, "Read", "Old", 1)
	if codex.StitchPointer(stale, link) != once {
		t.Fatal("stale block not replaced in place")
	}
	if !HasPointer(once) || HasPointer(hand) {
		t.Fatal("HasPointer")
	}
}

// A Cursor rule's frontmatter must stay first or Cursor ignores the rule.
func TestMDCPlacement(t *testing.T) {
	link := Link{FromPointer: "../../.lore/LORE.md", FromRoot: ".lore/LORE.md"}
	cursor, _ := mustReg(t).Lookup(NameCursor)
	blk := cursor.PointerBlock(link)
	fresh := cursor.StitchPointer("", link)
	if !strings.HasPrefix(fresh, "---\n") || !strings.Contains(fresh, "alwaysApply: true\n---\n"+blk) {
		t.Fatalf("new mdc = %q", fresh)
	}
	own := "---\ndescription: mine\nglobs: '*.go'\n---\nbody\n"
	got := cursor.StitchPointer(own, link)
	if got != "---\ndescription: mine\nglobs: '*.go'\n---\n"+blk+"\nbody\n" {
		t.Fatalf("existing mdc = %q", got)
	}
	if cursor.StitchPointer(got, link) != got {
		t.Fatal("mdc not idempotent")
	}
	// No frontmatter: plain prepend.
	if got := cursor.StitchPointer("body\n", link); got != blk+"\nbody\n" {
		t.Fatalf("mdc without frontmatter = %q", got)
	}
	// An unterminated frontmatter is not one.
	if got := cursor.StitchPointer("---\nx\n", link); got != blk+"\n---\nx\n" {
		t.Fatalf("unterminated = %q", got)
	}
	// A "$" in a block is literal, never a capture reference.
	re := MarkerRegexp("<!-- a -->", "<!-- b -->")
	if got := cursor.Place("<!-- a -->old<!-- b -->\n", "<!-- a -->$RID<!-- b -->\n", re); got != "<!-- a -->$RID<!-- b -->\n" {
		t.Fatalf("literal = %q", got)
	}
}

func mustReg(t *testing.T) *Registry {
	t.Helper()
	r, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestSelect (Q-R2-3): a context.targets row picks its profiles in registry
// order whatever order it was typed in, includes user profiles, lists the
// tool files it leaves alone as Unwired, and refuses an unknown or empty
// name list with E_INVALID_INPUT naming the bad profile.
func TestSelect(t *testing.T) {
	reg, err := New([]Profile{{Name: "windsurf", PointerFile: ".windsurfrules", Import: ImportRead}})
	if err != nil {
		t.Fatal(err)
	}
	probe := mapProbe{"CLAUDE.md": FileWired, "GEMINI.md": FilePresent}
	sel, err := reg.Select([]string{"cursor", "codex", "windsurf"}, probe)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(sel.Profiles); got != "codex,cursor,windsurf" || sel.Reason != ReasonSetting {
		t.Errorf("profiles %s reason %s", got, sel.Reason)
	}
	if got := names(sel.Unwired); got != NameGeminiCLI {
		t.Errorf("unwired %s, want gemini_cli (CLAUDE.md is wired, not merely present)", got)
	}
	for _, bad := range [][]string{{"codex", "claud"}, nil} {
		_, serr := reg.Select(bad, probe)
		if serr == nil || !errs.Has(serr, errs.InvalidInput) {
			t.Errorf("%v: err %v, want E_INVALID_INPUT", bad, serr)
			continue
		}
		if len(bad) > 0 && !strings.Contains(serr.Error(), `"claud"`) {
			t.Errorf("error does not name the profile: %v", serr)
		}
	}
	if u := reg.Unknown([]string{"x", "codex", "x"}); len(u) != 1 || u[0] != "x" {
		t.Errorf("Unknown = %v", u)
	}
}
