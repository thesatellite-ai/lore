//go:build machines && !windows

package machines

// sot_render_test.go — lifecycle proofs for rows 10 to 13 of
// the source-of-truth inventory through the built binary and `lore api`: each
// rendered copy is stamped with its rows, a changed row makes it stale in
// render --check and the doctor (CLI and REST) with the fix, the fix makes
// it clean, a second clone that syncs a teammate's rule gets a fresh
// LORE.md, a pointer file whose tool left context.targets is named (never
// removed), and a hand-edited skill copy is named with its install command.
//
//	row 10  LORE.md / LORE.full.md   stale after rule add; check and doctor say so; lore render clears it
//	row 10  two clones               clone B syncs A's live rule; its LORE.md is re-rendered
//	row 11  private render files     a personal-store rule makes the private file stale; sync refreshes it
//	row 12  tool pointer files       an orphan AGENTS.md is named with the keep fix and left as it is
//	row 13  installed skill copies   a hand edit is named with lore skill install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSotRender(t *testing.T) {
	for _, c := range []struct {
		name string
		run  func(t *testing.T)
	}{
		{"row10_lore_md", sotrLoreMD},
		{"row10_two_clones", sotrTwoClones},
		{"row11_private", sotrPrivate},
		{"row12_pointers", sotrPointers},
		{"row13_skill_copies", sotrSkillCopies},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t)
		})
	}
}

// Words the proofs look for.
const (
	sotrStale     = ".lore/LORE.md is stale"
	sotrDoctorRow = "rendered_copy: .lore/LORE.md is stale"
	sotrFix       = "fix: lore render"
)

// sotrLoreMD: render, add a rule, render --check (CLI and REST) and the
// doctor (CLI and REST) report LORE.md stale with lore render; the fix
// (lore render through REST) makes every one of them clean again.
func sotrLoreMD(t *testing.T) {
	m := Build(t, ShapeFresh, loreBin)
	const url = "git@github.com:sotorg/sot-render.git"
	bare := filepath.Join(m.Dir, "sot-render.git")
	sotBare(t, bare)
	sotRewrite(t, m, bare, url)
	a := sotrRepo(t, m, url, "sotr-a", true)

	if code, out := sotrCheck(t, m, a); code != 0 {
		t.Fatalf("render --check right after render: exit %d\n%s", code, out)
	}
	sotrLacks(t, "doctor right after render", sotrDoctor(t, m, a), "rendered_copy")

	sotOK(t, m, a, "rule", "add", "--body", "sotr rule two", "--severity", "must")
	code, out := sotrCheck(t, m, a)
	if code == 0 {
		t.Fatalf("render --check after rule add passed:\n%s", out)
	}
	sotrHas(t, "render --check after rule add", out, "E_REFUSED", sotrStale, "lore render")
	sotrHas(t, "doctor after rule add", sotrDoctor(t, m, a), sotrDoctorRow, sotrFix)
	if md, _ := os.ReadFile(filepath.Join(a, ".lore", "LORE.md")); strings.Contains(string(md), "sotr rule two") {
		t.Fatal("fixture: LORE.md already holds the new rule")
	}

	api := csStartAPI(t, m, a)
	if st, body := api.call(t, "POST", "/v1/render/run", `{"check":true}`); st == sotStatusOK || !strings.Contains(body, sotrStale) {
		t.Errorf("REST render --check after rule add: HTTP %d %s", st, body)
	}
	sotrHas(t, "REST doctor after rule add", sotAPICall(t, api, "POST", "/v1/doctor/run", `{}`, sotStatusOK), sotrDoctorRow)

	// The fix, as the doctor names it (REST here; the CLI runs the same op).
	sotAPICall(t, api, "POST", "/v1/render/run", `{}`, sotStatusOK)
	if md, _ := os.ReadFile(filepath.Join(a, ".lore", "LORE.md")); !strings.Contains(string(md), "sotr rule two") {
		t.Errorf("after the fix LORE.md lacks the new rule:\n%s", md)
	}
	if code, out := sotrCheck(t, m, a); code != 0 {
		t.Errorf("render --check after the fix: exit %d\n%s", code, out)
	}
	sotrLacks(t, "doctor after the fix", sotrDoctor(t, m, a), "rendered_copy")
	sotrLacks(t, "REST doctor after the fix", sotAPICall(t, api, "POST", "/v1/doctor/run", `{}`, sotStatusOK), "rendered_copy")

	// Same rows, same bytes: a second render changes nothing.
	before, _ := os.ReadFile(filepath.Join(a, ".lore", "LORE.md"))
	sotOK(t, m, a, "render")
	if after, _ := os.ReadFile(filepath.Join(a, ".lore", "LORE.md")); string(after) != string(before) {
		t.Errorf("a second render of the same rows changed LORE.md:\n%s\nvs\n%s", before, after)
	}

	// A hand edit with a current stamp is "edited by hand", not stale.
	md := filepath.Join(a, ".lore", "LORE.md")
	if err := os.WriteFile(md, append(before, []byte("my own line\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	sotrHas(t, "doctor after a hand edit", sotrDoctor(t, m, a), "rendered_copy: .lore/LORE.md was edited by hand", sotrFix)
}

// sotrTwoClones: clone B renders the same bytes as A; A adds a live rule;
// B's sync brings the row in and re-renders its LORE.md, so B's check is
// clean and its LORE.md holds the rule.
func sotrTwoClones(t *testing.T) {
	m := Build(t, ShapeFresh, loreBin)
	const url = "git@github.com:sotorg/sot-render-two.git"
	bare := filepath.Join(m.Dir, "sot-render-two.git")
	sotBare(t, bare)
	sotRewrite(t, m, bare, url)
	a := sotrRepo(t, m, url, "sotr2-a", true)
	b := sotrRepo(t, m, url, "sotr2-b", false)
	sotOK(t, m, b, "sync")
	if code, out := sotrCheck(t, m, b); code != 0 {
		t.Fatalf("clone B before A's rule: exit %d\n%s", code, out)
	}
	amd, _ := os.ReadFile(filepath.Join(a, ".lore", "LORE.md"))
	bmd, _ := os.ReadFile(filepath.Join(b, ".lore", "LORE.md"))
	if string(amd) != string(bmd) {
		t.Errorf("two clones of the same rows hold different LORE.md:\n%s\nvs\n%s", amd, bmd)
	}

	sotOK(t, m, a, "rule", "add", "--body", "sotr rule from a", "--severity", "must", "--live")
	sotOK(t, m, a, "sync")
	sotOK(t, m, b, "sync")
	if md, _ := os.ReadFile(filepath.Join(b, ".lore", "LORE.md")); !strings.Contains(string(md), "sotr rule from a") {
		t.Errorf("clone B after syncing A's rule: LORE.md was not re-rendered:\n%s", md)
	}
	if code, out := sotrCheck(t, m, b); code != 0 {
		t.Errorf("clone B after sync: render --check exit %d\n%s", code, out)
	}
	sotrLacks(t, "clone B doctor after sync", sotrDoctor(t, m, b), "rendered_copy")
}

// sotrPrivate: a personal-store rule makes the private store file stale
// (check and doctor name it); lore sync re-renders it.
func sotrPrivate(t *testing.T) {
	m := Build(t, ShapeFresh, loreBin)
	const url = "git@github.com:sotorg/sot-render-me.git"
	bare := filepath.Join(m.Dir, "sot-render-me.git")
	sotBare(t, bare)
	sotRewrite(t, m, bare, url)
	a := sotrRepo(t, m, url, "sotr3-a", true)
	sotOK(t, m, a, "store", "init", "me", "--yes")
	sotOK(t, m, a, "memory", "add", "--to", "me", "--body", "sotr private note")
	sotOK(t, m, a, "render")
	files, _ := filepath.Glob(filepath.Join(m.Home, ".lore", "render", "*", "sto_*.md"))
	if len(files) != 1 {
		t.Fatalf("private store files: %v", files)
	}
	first, _ := os.ReadFile(files[0])
	if lines := strings.SplitN(string(first), "\n", 3); len(lines) < 2 || !strings.HasPrefix(lines[1], "<!-- lore:inputs ") {
		t.Errorf("private file without its stamp on line 2:\n%s", first)
	}
	if code, out := sotrCheck(t, m, a); code != 0 {
		t.Fatalf("render --check after render: exit %d\n%s", code, out)
	}

	sotOK(t, m, a, "rule", "add", "--to", "me", "--body", "sotr private rule", "--severity", "must")
	code, out := sotrCheck(t, m, a)
	if code == 0 {
		t.Fatalf("render --check after a private rule passed:\n%s", out)
	}
	sotrHas(t, "render --check after a private rule", out, filepath.Base(files[0])+" is stale", "lore render")
	sotrHas(t, "doctor after a private rule", sotrDoctor(t, m, a), "rendered_copy: ", filepath.Base(files[0])+" is stale", sotrFix)

	sotOK(t, m, a, "sync")
	if got, _ := os.ReadFile(files[0]); !strings.Contains(string(got), "sotr private rule") {
		t.Errorf("sync did not re-render the private file:\n%s", got)
	}
	if code, out := sotrCheck(t, m, a); code != 0 {
		t.Errorf("render --check after sync: exit %d\n%s", code, out)
	}
}

// sotrPointers: context.targets moves from codex to claude_code; the
// doctor names AGENTS.md as an orphan with the keep fix and leaves the
// file as it is; running the fix clears the finding.
func sotrPointers(t *testing.T) {
	m := Build(t, ShapeFresh, loreBin)
	const url = "git@github.com:sotorg/sot-render-ptr.git"
	bare := filepath.Join(m.Dir, "sot-render-ptr.git")
	sotBare(t, bare)
	sotRewrite(t, m, bare, url)
	a := sotrRepo(t, m, url, "sotr4-a", true)
	agents := filepath.Join(a, "AGENTS.md")
	before, err := os.ReadFile(agents)
	if err != nil {
		t.Fatalf("fixture: render wired no AGENTS.md: %v", err)
	}
	sotrLacks(t, "doctor before the switch", sotrDoctor(t, m, a), "tool_pointer")

	sotOK(t, m, a, "policy", "set", "context.targets", "claude_code", "--yes")
	sotOK(t, m, a, "render")
	got := sotrDoctor(t, m, a)
	sotrHas(t, "doctor after the switch", got, "tool_pointer: AGENTS.md carries lore's pointer", "fix: lore policy set --key context.targets --value claude_code,codex --yes", "by hand")
	if after, _ := os.ReadFile(agents); string(after) != string(before) {
		t.Errorf("AGENTS.md changed:\n%s\nvs\n%s", before, after)
	}

	// CLAUDE.md lost its pointer: the listed tool is named with lore render.
	claude := filepath.Join(a, "CLAUDE.md")
	if err := os.WriteFile(claude, []byte("# my notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sotrHas(t, "doctor with CLAUDE.md unwired", sotrDoctor(t, m, a), "tool_pointer: CLAUDE.md has no lore pointer", sotrFix)

	sotOK(t, m, a, "policy", "set", "context.targets", "claude_code,codex", "--yes")
	sotOK(t, m, a, "render")
	sotrLacks(t, "doctor after both fixes", sotrDoctor(t, m, a), "tool_pointer")
	if c, _ := os.ReadFile(claude); !strings.Contains(string(c), "# my notes") {
		t.Errorf("render dropped the person's own line in CLAUDE.md:\n%s", c)
	}
}

// sotrSkillCopies: a hand edit of an installed skill copy is named by the
// doctor (CLI and REST) with lore skill install; reinstalling clears it.
func sotrSkillCopies(t *testing.T) {
	m := Build(t, ShapeFresh, loreBin)
	repo := m.Repos[RepoLore]
	sotOK(t, m, repo, "skill", "install", "--tool", "claude_code")
	sotrLacks(t, "doctor after install", sotrDoctor(t, m, repo), "skill_copy")
	skillMD := filepath.Join(m.Home, ".claude", "skills", "lore", "SKILL.md")
	f, err := os.OpenFile(skillMD, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n# my own note\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	sotrHas(t, "doctor after a hand edit", sotrDoctor(t, m, repo), "skill_copy: the claude_code skill", "edited by hand", "fix: lore skill install --tool claude_code")
	api := csStartAPI(t, m, repo)
	sotrHas(t, "REST doctor after a hand edit", sotAPICall(t, api, "POST", "/v1/doctor/run", `{}`, sotStatusOK), "skill_copy")
	sotOK(t, m, repo, "skill", "install", "--tool", "claude_code")
	sotrLacks(t, "doctor after reinstall", sotrDoctor(t, m, repo), "skill_copy")
}
