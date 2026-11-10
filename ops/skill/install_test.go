package skill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/skills"
)

// fixedNow names backups deterministically.
func fixedNow() time.Time { return time.Date(2026, 11, 6, 9, 30, 0, 0, time.UTC) }

// newTestInstaller is an Installer over a temp HOME with the real embedded
// bundle (so the go:embed patterns are exercised too).
func newTestInstaller(t *testing.T, version string) Installer {
	t.Helper()
	return Installer{Home: t.TempDir(), Version: version, Now: fixedNow}
}

func readSkill(t *testing.T, dir string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, skills.EntryFile))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestEmbeddedBundle: the bundle holds SKILL.md with the dev line and the
// examples, like install.sh's copy.
func TestEmbeddedBundle(t *testing.T) {
	files, names, err := skills.Files()
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := toolprofile.ReadSkillVersion(files[skills.EntryFile]); !ok || v != toolprofile.SkillVersionDev {
		t.Fatalf("source SKILL.md lore_version = %q %v, want dev", v, ok)
	}
	hasExample := false
	for _, n := range names {
		hasExample = hasExample || strings.HasPrefix(n, "examples/")
		if strings.HasSuffix(n, ".go") {
			t.Fatalf("Go file in the bundle: %s", n)
		}
	}
	if !hasExample || len(names) < 5 {
		t.Fatalf("bundle looks incomplete: %v", names)
	}
}

// TestInstallIdempotent: first install writes and stamps, the second is
// changed:false and writes nothing; an upgrade re-stamps.
func TestInstallIdempotent(t *testing.T) {
	ctx := context.Background()
	ins := newTestInstaller(t, "v0.1.18")
	out, err := ins.Install(ctx, InstallIn{Tools: []string{toolprofile.NameClaudeCode, toolprofile.NameCodex}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Installed) != 2 || !out.Installed[0].Changed || !out.Installed[1].Changed {
		t.Fatalf("first install: %+v", out)
	}
	dir := filepath.Join(ins.Home, ".claude", "skills", "lore")
	if out.Installed[0].Dir != dir {
		t.Fatalf("dir %q", out.Installed[0].Dir)
	}
	if v, ok := toolprofile.ReadSkillVersion(readSkill(t, dir)); !ok || v != "0.1.18" {
		t.Fatalf("stamp %q %v", v, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, "examples", "01-bootstrap.md")); err != nil {
		t.Fatalf("examples missing: %v", err)
	}
	st, _ := os.Stat(filepath.Join(dir, skills.EntryFile))
	again, err := ins.Install(ctx, InstallIn{Tools: []string{toolprofile.NameClaudeCode}})
	if err != nil {
		t.Fatal(err)
	}
	if again.Installed[0].Changed {
		t.Fatalf("second install changed: %+v", again)
	}
	st2, _ := os.Stat(filepath.Join(dir, skills.EntryFile))
	if !st.ModTime().Equal(st2.ModTime()) {
		t.Fatal("second install rewrote SKILL.md")
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), ".lore-skill-*"))
	if len(leftovers) != 0 {
		t.Fatalf("swap left folders behind: %v", leftovers)
	}
	// A newer binary re-stamps, with no backup (old version, not a hand edit).
	up := ins
	up.Version = "0.1.19"
	res, err := up.Install(ctx, InstallIn{Tools: []string{toolprofile.NameClaudeCode}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Installed[0].Changed || len(res.Installed[0].Backup) != 0 {
		t.Fatalf("upgrade install: %+v", res)
	}
	if v, _ := toolprofile.ReadSkillVersion(readSkill(t, dir)); v != "0.1.19" {
		t.Fatalf("re-stamp: %q", v)
	}
}

// TestInstallBacksUpHandEdit: a hand-edited SKILL.md at the same version is
// kept as SKILL.md.bak-<date> before the overwrite; the user's own extra
// file survives; status reports modified before and not after.
func TestInstallBacksUpHandEdit(t *testing.T) {
	ctx := context.Background()
	ins := newTestInstaller(t, "0.1.18")
	if _, err := ins.Install(ctx, InstallIn{Tools: []string{toolprofile.NameClaudeCode}}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(ins.Home, ".claude", "skills", "lore")
	edited := append(readSkill(t, dir), []byte("\nmy own note\n")...)
	if err := os.WriteFile(filepath.Join(dir, skills.EntryFile), edited, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := ins.Status(ctx, StatusIn{Tools: []string{toolprofile.NameClaudeCode}})
	if err != nil || !st.Tools[0].Modified || st.Tools[0].Status != toolprofile.SkillOK {
		t.Fatalf("status before: %+v %v", st, err)
	}
	dry, err := ins.Install(ctx, InstallIn{Tools: []string{toolprofile.NameClaudeCode}, DryRun: true})
	if err != nil || !dry.Installed[0].Changed || len(dry.Installed[0].Backup) != 1 {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	if got := readSkill(t, dir); string(got) != string(edited) {
		t.Fatal("dry run wrote")
	}
	out, err := ins.Install(ctx, InstallIn{Tools: []string{toolprofile.NameClaudeCode}})
	if err != nil {
		t.Fatal(err)
	}
	bak := filepath.Join(dir, "SKILL.md.bak-2026-11-06")
	if b := out.Installed[0].Backup; len(b) != 1 || b[0] != bak {
		t.Fatalf("backup %v", b)
	}
	if got, _ := os.ReadFile(bak); string(got) != string(edited) {
		t.Fatal("backup does not hold the edited copy")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "mine.txt")); string(got) != "keep me" {
		t.Fatal("user file lost")
	}
	if strings.Contains(string(readSkill(t, dir)), "my own note") {
		t.Fatal("edit not overwritten")
	}
	st, _ = ins.Status(ctx, StatusIn{Tools: []string{toolprofile.NameClaudeCode}})
	if st.Tools[0].Modified {
		t.Fatalf("still modified: %+v", st)
	}
}

// TestInstallRefusesCursor: an explicit tool with no skill folder is
// E_UNSUPPORTED with the 4.7.1 text; --all skips it silently.
func TestInstallRefusesCursor(t *testing.T) {
	ctx := context.Background()
	ins := newTestInstaller(t, "0.1.18")
	_, err := ins.Install(ctx, InstallIn{Tools: []string{toolprofile.NameCursor}})
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.Unsupported || e.Message != "cursor has no skill folder; it reads lore through .cursor/rules/lore.mdc" {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(ins.Home, ".cursor")); !os.IsNotExist(err) {
		t.Fatal("wrote something for cursor")
	}
	all, err := ins.Install(ctx, InstallIn{All: true, DryRun: true})
	if err != nil || len(all.Installed) != 2 {
		t.Fatalf("--all: %+v %v", all, err)
	}
	if _, err := ins.Install(ctx, InstallIn{Tools: []string{"nosuch"}}); err == nil {
		t.Fatal("unknown tool accepted")
	}
}

// fakeChooser is a ToolChooser with fixed answers.
type fakeChooser struct {
	active   []string
	detected []string
}

func (f fakeChooser) ActiveSetupTools(context.Context) ([]string, bool, error) {
	return f.active, len(f.active) > 0, nil
}
func (f fakeChooser) DetectedTools(context.Context) ([]string, error) { return f.detected, nil }

// TestInstallDefaultTools: no --tool uses the active setup, else the
// detected tools; a detected tool without a skill folder is skipped.
func TestInstallDefaultTools(t *testing.T) {
	ctx := context.Background()
	ins := newTestInstaller(t, "0.1.18")
	ins.Chooser = fakeChooser{active: []string{toolprofile.NameCodex}, detected: []string{toolprofile.NameClaudeCode}}
	out, err := ins.Install(ctx, InstallIn{DryRun: true})
	if err != nil || len(out.Installed) != 1 || out.Installed[0].Tool != toolprofile.NameCodex {
		t.Fatalf("active setup: %+v %v", out, err)
	}
	ins.Chooser = fakeChooser{detected: []string{toolprofile.NameClaudeCode, toolprofile.NameCursor}}
	out, err = ins.Install(ctx, InstallIn{DryRun: true})
	if err != nil || len(out.Installed) != 1 || len(out.Skipped) != 1 || out.Skipped[0].Tool != toolprofile.NameCursor {
		t.Fatalf("detected: %+v %v", out, err)
	}
	// The fallback detector: ~/.codex exists, ~/.claude does not.
	ins.Chooser = nil
	if err := os.MkdirAll(filepath.Join(ins.Home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err = ins.Install(ctx, InstallIn{DryRun: true})
	if err != nil || len(out.Installed) != 1 || out.Installed[0].Tool != toolprofile.NameCodex {
		t.Fatalf("home detector: %+v %v", out, err)
	}
	empty := newTestInstaller(t, "0.1.18")
	if _, err := empty.Install(ctx, InstallIn{}); err == nil {
		t.Fatal("no tool found: want an error")
	}
}

// TestStatusVersions: status runs the 4.7.1 check against the binary.
func TestStatusVersions(t *testing.T) {
	ctx := context.Background()
	old := newTestInstaller(t, "0.1.17")
	if _, err := old.Install(ctx, InstallIn{Tools: []string{toolprofile.NameClaudeCode}}); err != nil {
		t.Fatal(err)
	}
	cur := old
	cur.Version = "0.1.18"
	st, err := cur.Status(ctx, StatusIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Tools) != 2 || st.BinaryVersion != "0.1.18" {
		t.Fatalf("%+v", st)
	}
	c, x := st.Tools[0], st.Tools[1]
	if !c.Installed || c.Version != "0.1.17" || c.Status != toolprofile.SkillWarn || c.Detail != "skill from lore 0.1.17, binary 0.1.18" || c.Modified {
		t.Fatalf("claude_code: %+v", c)
	}
	if x.Installed || x.Status != toolprofile.SkillWarn {
		t.Fatalf("codex: %+v", x)
	}
	cs, err := cur.Status(ctx, StatusIn{Tools: []string{toolprofile.NameCursor}})
	if err != nil || cs.Tools[0].Status != toolprofile.SkillSkip {
		t.Fatalf("cursor: %+v %v", cs, err)
	}
}

// fakeEnv is an InstallEnv over a temp HOME.
type fakeEnv struct{ home string }

func (fakeEnv) BinaryVersion() string { return "0.1.18" }
func (fakeEnv) ToolProfiles(context.Context) ([]toolprofile.Profile, error) {
	return []toolprofile.Profile{{Name: "mytool", PointerFile: "MYTOOL.md", Import: toolprofile.ImportRead, SkillDir: "/etc/bad"}}, nil
}
func (f fakeEnv) SkillHome(context.Context) (string, error)     { return f.home, nil }
func (fakeEnv) SkillTools(context.Context) (ToolChooser, error) { return nil, nil }

// TestInstallOpsRegister: the two specs pass the registry's checks (names,
// error codes, Local rules) beside skill.compile, and a bad skill_dir is
// E_CONFIG_INVALID naming context.profiles.
func TestInstallOpsRegister(t *testing.T) {
	env := fakeEnv{home: t.TempDir()}
	if _, err := op.NewRegistry(append(Ops(nil), InstallOps(env)...)...); err != nil {
		t.Fatal(err)
	}
	_, err := Registry(context.Background(), env)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.ConfigInvalid || !strings.Contains(e.Error(), "context.profiles") {
		t.Fatalf("err = %v", err)
	}
}

// clockEnv is an InstallEnv that is also an opclock.Clocker (the engine's
// clock as wire.App offers it, LORE_NOW in a loretest build); with a zero
// at it offers no clock (plainEnv below).
type clockEnv struct {
	home string
	at   time.Time
}

func (clockEnv) BinaryVersion() string                                       { return "0.1.18" }
func (clockEnv) ToolProfiles(context.Context) ([]toolprofile.Profile, error) { return nil, nil }
func (c clockEnv) SkillHome(context.Context) (string, error)                 { return c.home, nil }
func (clockEnv) SkillTools(context.Context) (ToolChooser, error)             { return nil, nil }
func (c clockEnv) Now() time.Time                                            { return c.at }

// plainEnv is the same env without a clock.
type plainEnv struct{ InstallEnv }

// TestNewInstallerUsesEngineClock: the ops build the Installer on the
// env's clock, so a hand-edited skill's backup is named after the engine's
// date (SKILL.md.bak-<date>), not the wall clock's; an env with no clock
// still gets a working one.
func TestNewInstallerUsesEngineClock(t *testing.T) {
	at := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	env := clockEnv{home: t.TempDir(), at: at}
	ins, err := newInstaller(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if ins.Now == nil || !ins.Now().Equal(at) {
		t.Fatal("installer clock is not the env's")
	}
	plain, err := newInstaller(context.Background(), plainEnv{env})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Now == nil || time.Since(plain.Now()) > time.Hour {
		t.Fatal("an env without a clock must fall back to the system clock")
	}
}
