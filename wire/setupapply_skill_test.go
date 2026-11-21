package wire

// setupapply_skill_test.go — the tools step installs the lore skill for
// each chosen tool that has none (devco D-809): a member who had just
// joined got Status: DEGRADED from lore doctor because the step only
// recorded the tools. A linked skill folder and a pointer-file tool are
// left alone; a dry run writes nothing; a second save writes nothing.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/ops/skill"
)

func TestSetupToolsStepInstallsSkill(t *testing.T) {
	ctx := context.Background()
	w := newOrgWorld(t)
	home := w.home(t, "mira")
	app, err := New(StandaloneOptions(w.repo, home, w.repo, "test"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	ap, err := app.SetupApplier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := skill.Registry(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	userHome := filepath.Dir(home)
	skillDir := func(tool string) string {
		p, ok := reg.Lookup(tool)
		if !ok || !p.HasSkillDir() {
			t.Fatalf("tool %s has no skill folder in the registry", tool)
		}
		return p.SkillPath(userHome)
	}
	codex := skillDir("codex")
	// claude_code's skill folder is a link to a folder with no SKILL.md:
	// a plain install would refuse, so the step leaves it for the person.
	claude := skillDir("claude_code")
	shared := filepath.Join(userHome, "shared-skills", "lore")
	if err := os.MkdirAll(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(claude), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, claude); err != nil {
		t.Fatal(err)
	}

	setup := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Default: true}
	tools := onboard.Answers{onboard.QIDTools: {List: []string{"codex", "claude_code", "cursor"}}}
	apply := func(dry bool) opssetup.ApplyResult {
		t.Helper()
		res, aerr := ap.Apply(ctx, opssetup.ApplyRequest{Setup: setup, Step: onboard.StepTools, Apply: onboard.ApplyTools, Answers: tools, DryRun: dry})
		if aerr != nil {
			t.Fatalf("tools step (dry %v): %v", dry, aerr)
		}
		return res
	}

	dry := apply(true)
	if !slices.Contains(dry.Wrote, codex) {
		t.Fatalf("dry run wrote %v, want it to name %s", dry.Wrote, codex)
	}
	if _, serr := os.Stat(filepath.Join(codex, "SKILL.md")); !os.IsNotExist(serr) {
		t.Fatalf("dry run wrote the codex skill (stat %v)", serr)
	}

	res := apply(false)
	if _, serr := os.Stat(filepath.Join(codex, "SKILL.md")); serr != nil {
		t.Fatalf("tools step left codex with no skill: %v (wrote %v)", serr, res.Wrote)
	}
	if !res.Changed || !slices.Contains(res.Wrote, codex) || slices.Contains(res.Wrote, claude) {
		t.Fatalf("tools step = changed %v wrote %v, want codex's folder only", res.Changed, res.Wrote)
	}
	if fi, lerr := os.Lstat(claude); lerr != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the linked claude_code folder was replaced (lstat %v, %v)", fi, lerr)
	}
	if ents, _ := os.ReadDir(shared); len(ents) != 0 {
		t.Fatalf("the step wrote into the link's target: %v", ents)
	}
	st, err := skill.MissingTools(ctx, app, []string{"codex"})
	if err != nil || len(st) != 0 {
		t.Fatalf("after the step codex still misses its skill: %v %v", st, err)
	}

	setup = res.Setup
	again := apply(false)
	if slices.Contains(again.Wrote, codex) {
		t.Fatalf("second save rewrote the skill: %v", again.Wrote)
	}
}
