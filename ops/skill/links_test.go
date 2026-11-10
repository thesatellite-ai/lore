package skill

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/skills"
)

// shapeVersion is the binary version of the shape tests.
const shapeVersion = "0.1.18"

// bundleAt writes lore's bundle stamped with version into a fresh folder
// (through a scratch install) and returns it.
func bundleAt(t *testing.T, version string) string {
	t.Helper()
	src := Installer{Home: t.TempDir(), Version: version, Now: fixedNow}
	if _, err := src.Install(context.Background(), InstallIn{Tools: []string{toolprofile.NameClaudeCode}}); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(src.Home, ".claude", "skills", "lore")
}

// symlink makes link -> target, making link's folder.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// snapshot is every file under dir (links followed) as text, to prove a
// refused install wrote nothing.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	tr, err := readTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, k := range sortedKeys(tr.Files) {
		b.WriteString(k + "=" + string(tr.Files[k]) + "\n")
	}
	return b.String()
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// shape is one way a tool's skill folder can sit on disk.
type shape struct {
	name string
	// make builds the shape under home's claude_code skill dir and returns
	// the folder the files really live in ("" when nothing exists).
	make func(t *testing.T, dir string) string
	// status: installed, its version, and the skill_installed /
	// skill_version rows (status and fix variant).
	installed  bool
	version    string
	linked     bool
	installRow onboard.Status
	installFix onboard.FixVariant
	versionRow onboard.Status
	versionFix onboard.FixVariant
	detailHas  string
	// install with no --link: refused (E_SKILL_LINKED) or a no-op success.
	refused bool
	changed bool
}

// TestSkillDirShapes runs every folder shape through status, the shared
// check rows (doctor and setup page), and install without --link (owner
// bug 2026-11-08: a linked folder read as empty and said "not installed").
func TestSkillDirShapes(t *testing.T) {
	shapes := []shape{
		{name: "real folder, current", make: func(t *testing.T, dir string) string {
			b := bundleAt(t, shapeVersion)
			if err := os.Rename(b, dir); err != nil {
				if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(b, dir); err != nil {
					t.Fatal(err)
				}
			}
			return dir
		}, installed: true, version: shapeVersion, installRow: onboard.StatusOK, versionRow: onboard.StatusOK},
		{name: "linked folder, current", make: func(t *testing.T, dir string) string {
			b := bundleAt(t, shapeVersion)
			symlink(t, b, dir)
			return b
		}, installed: true, version: shapeVersion, linked: true, installRow: onboard.StatusOK, versionRow: onboard.StatusOK, detailHas: " -> "},
		{name: "chain of two links (shared-skills layout), current", make: func(t *testing.T, dir string) string {
			b := bundleAt(t, shapeVersion)
			mid := filepath.Join(t.TempDir(), ".agents", "skills", "lore")
			symlink(t, b, mid)
			symlink(t, mid, dir)
			return b
		}, installed: true, version: shapeVersion, linked: true, installRow: onboard.StatusOK, versionRow: onboard.StatusOK, detailHas: " -> "},
		{name: "relative chain of two links, current", make: func(t *testing.T, dir string) string {
			home := filepath.Dir(filepath.Dir(filepath.Dir(dir))) // dir = home/.claude/skills/lore
			b := bundleAt(t, shapeVersion)
			mid := filepath.Join(home, ".agents", "skills", "lore")
			symlink(t, b, mid)
			symlink(t, "../../.agents/skills/lore", dir) // the owner's exact link text
			return b
		}, installed: true, version: shapeVersion, linked: true, installRow: onboard.StatusOK, versionRow: onboard.StatusOK, detailHas: " -> "},
		{name: "dangling link", make: func(t *testing.T, dir string) string {
			symlink(t, filepath.Join(t.TempDir(), "gone"), dir)
			return ""
		}, linked: true, installRow: onboard.StatusWarn, installFix: onboard.FixLinked, versionRow: onboard.StatusSkip, detailHas: "does not exist", refused: true},
		{name: "linked SKILL.md in a real folder, current", make: func(t *testing.T, dir string) string {
			b := bundleAt(t, shapeVersion)
			if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(b, dir); err != nil {
				t.Fatal(err)
			}
			elsewhere := filepath.Join(t.TempDir(), "SKILL.md")
			if err := os.Rename(filepath.Join(dir, skills.EntryFile), elsewhere); err != nil {
				t.Fatal(err)
			}
			symlink(t, elsewhere, filepath.Join(dir, skills.EntryFile))
			return dir
		}, installed: true, version: shapeVersion, linked: true, installRow: onboard.StatusOK, versionRow: onboard.StatusOK},
		{name: "link to an older lore skill", make: func(t *testing.T, dir string) string {
			b := bundleAt(t, "0.1.17")
			symlink(t, b, dir)
			return b
		}, installed: true, version: "0.1.17", linked: true, installRow: onboard.StatusOK, versionRow: onboard.StatusWarn, versionFix: onboard.FixLinked, refused: true},
		{name: "link to a folder with no SKILL.md", make: func(t *testing.T, dir string) string {
			b := t.TempDir()
			if err := os.WriteFile(filepath.Join(b, "notes.md"), []byte("placeholder\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			symlink(t, b, dir)
			return b
		}, linked: true, installRow: onboard.StatusWarn, installFix: onboard.FixLinked, versionRow: onboard.StatusSkip, detailHas: "holds no SKILL.md", refused: true},
		{name: "link to a foreign skill", make: func(t *testing.T, dir string) string {
			b := t.TempDir()
			if err := os.WriteFile(filepath.Join(b, skills.EntryFile), []byte("---\nname: someone-else\n---\nplaceholder\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			symlink(t, b, dir)
			return b
		}, linked: true, installRow: onboard.StatusWarn, installFix: onboard.FixLinked, versionRow: onboard.StatusSkip, detailHas: "not lore's", refused: true},
		{name: "missing folder", make: func(*testing.T, string) string { return "" },
			installRow: onboard.StatusWarn, versionRow: onboard.StatusSkip, changed: true},
	}
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			ctx := context.Background()
			ins := Installer{Home: t.TempDir(), Version: shapeVersion, Now: fixedNow}
			dir := filepath.Join(ins.Home, ".claude", "skills", "lore")
			real := sh.make(t, dir)

			st, err := ins.Status(ctx, StatusIn{Tools: []string{toolprofile.NameClaudeCode}})
			if err != nil {
				t.Fatal(err)
			}
			ts := st.Tools[0]
			if ts.Installed != sh.installed || ts.Version != sh.version || ts.Linked != sh.linked {
				t.Fatalf("status = %+v, want installed %v version %q linked %v", ts, sh.installed, sh.version, sh.linked)
			}
			// The shared rows: what doctor, Home and the Tools step all show.
			ir := CheckRow(onboard.CheckToolsSkillInstalled, ts, shapeVersion, nil)
			vr := CheckRow(onboard.CheckToolsSkillVersion, ts, shapeVersion, nil)
			if ir.Status != sh.installRow || onboard.FixVariant(ir.Vars[onboard.VarFixVariant]) != sh.installFix {
				t.Fatalf("skill_installed row = %+v, want %s fix %q", ir, sh.installRow, sh.installFix)
			}
			if vr.Status != sh.versionRow || onboard.FixVariant(vr.Vars[onboard.VarFixVariant]) != sh.versionFix {
				t.Fatalf("skill_version row = %+v, want %s fix %q", vr, sh.versionRow, sh.versionFix)
			}
			if sh.detailHas != "" && !strings.Contains(ir.Detail+" "+ts.Detail, sh.detailHas) {
				t.Fatalf("detail %q / %q lacks %q", ir.Detail, ts.Detail, sh.detailHas)
			}
			if ts.Link != "" && real != "" && !sameReal(t, ts.Link, real) {
				t.Fatalf("link end %q, want %q", ts.Link, real)
			}

			before := ""
			if real != "" {
				before = snapshot(t, real)
			}
			out, err := ins.Install(ctx, InstallIn{Tools: []string{toolprofile.NameClaudeCode}})
			if sh.refused {
				if !errs.Has(err, errs.SkillLinked) {
					t.Fatalf("install = %+v, %v; want E_SKILL_LINKED", out, err)
				}
				var e *errs.Error
				if !errors.As(err, &e) || !strings.Contains(e.Hint, "--link replace") || !strings.Contains(e.Hint, "--link update") || !strings.Contains(e.Message, dir) {
					t.Fatalf("refusal must name the link and both choices: %v", err)
				}
				if real != "" && snapshot(t, real) != before {
					t.Fatal("a refused install changed the link's target")
				}
				// --all: the same refusal is a skip, never an "installed".
				all, aerr := ins.Install(ctx, InstallIn{All: true})
				if aerr != nil || len(all.Skipped) == 0 || all.Skipped[0].Tool != toolprofile.NameClaudeCode || !strings.Contains(all.Skipped[0].Why, "E_SKILL_LINKED") {
					t.Fatalf("--all = %+v, %v; want claude_code skipped with the refusal", all, aerr)
				}
				for _, i := range all.Installed {
					if i.Tool == toolprofile.NameClaudeCode {
						t.Fatalf("--all reported claude_code installed: %+v", all)
					}
				}
				return
			}
			if err != nil || len(out.Installed) != 1 || out.Installed[0].Changed != sh.changed {
				t.Fatalf("install = %+v, %v; want changed %v", out, err, sh.changed)
			}
			if real != "" && snapshot(t, real) != before {
				t.Fatal("an up-to-date install rewrote files")
			}
			if fi, lerr := os.Lstat(dir); sh.linked && real != dir && (lerr != nil || fi.Mode()&os.ModeSymlink == 0) {
				t.Fatal("an up-to-date install replaced the person's link")
			}
		})
	}
}

// TestInstallLinkChoices: --link replace swaps only the link for lore's
// copy (its target untouched); --link update writes into the target,
// keeping the person's files and a .bak of hand edits; update onto a
// dangling link refuses.
func TestInstallLinkChoices(t *testing.T) {
	ctx := context.Background()
	tool := []string{toolprofile.NameClaudeCode}
	setup := func(t *testing.T) (Installer, string, string) {
		ins := Installer{Home: t.TempDir(), Version: shapeVersion, Now: fixedNow}
		dir := filepath.Join(ins.Home, ".claude", "skills", "lore")
		old := bundleAt(t, "0.1.17")
		if err := os.WriteFile(filepath.Join(old, "mine.md"), []byte("placeholder\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		mid := filepath.Join(ins.Home, ".agents", "skills", "lore")
		symlink(t, old, mid)
		symlink(t, mid, dir)
		return ins, dir, old
	}
	t.Run("replace", func(t *testing.T) {
		ins, dir, old := setup(t)
		before := snapshot(t, old)
		dry, err := ins.Install(ctx, InstallIn{Tools: tool, Link: LinkReplace, DryRun: true})
		if err != nil || len(dry.Installed) != 1 || dry.Installed[0].Via != viaReplacedLink {
			t.Fatalf("dry run %+v %v", dry, err)
		}
		if fi, _ := os.Lstat(dir); fi.Mode()&os.ModeSymlink == 0 {
			t.Fatal("dry run replaced the link")
		}
		out, err := ins.Install(ctx, InstallIn{Tools: tool, Link: LinkReplace})
		if err != nil || len(out.Installed) != 1 || !out.Installed[0].Changed || out.Installed[0].Via != viaReplacedLink {
			t.Fatalf("%+v %v", out, err)
		}
		fi, err := os.Lstat(dir)
		if err != nil || !fi.IsDir() {
			t.Fatalf("dir is not a real folder now: %v %v", fi, err)
		}
		if v, _ := toolprofile.ReadSkillVersion(readSkill(t, dir)); v != shapeVersion {
			t.Fatalf("stamp %q", v)
		}
		if snapshot(t, old) != before {
			t.Fatal("replace touched the link's target")
		}
		if _, err := os.Stat(filepath.Join(ins.Home, ".agents", "skills", "lore", skills.EntryFile)); err != nil {
			t.Fatalf("the middle link is gone: %v", err)
		}
		st, _ := ins.Status(ctx, StatusIn{Tools: tool})
		if !st.Tools[0].Installed || st.Tools[0].Linked || st.Tools[0].Status != toolprofile.SkillOK {
			t.Fatalf("status after replace %+v", st.Tools[0])
		}
	})
	t.Run("update", func(t *testing.T) {
		ins, dir, old := setup(t)
		out, err := ins.Install(ctx, InstallIn{Tools: tool, Link: LinkUpdate})
		if err != nil || len(out.Installed) != 1 || out.Installed[0].Via != viaUpdatedTarget {
			t.Fatalf("%+v %v", out, err)
		}
		if fi, _ := os.Lstat(dir); fi.Mode()&os.ModeSymlink == 0 {
			t.Fatal("update replaced the link")
		}
		if v, _ := toolprofile.ReadSkillVersion(readSkill(t, old)); v != shapeVersion {
			t.Fatalf("target stamp %q", v)
		}
		if b, err := os.ReadFile(filepath.Join(old, "mine.md")); err != nil || !bytes.Equal(b, []byte("placeholder\n")) {
			t.Fatal("update dropped the person's own file")
		}
		again, err := ins.Install(ctx, InstallIn{Tools: tool})
		if err != nil || again.Installed[0].Changed {
			t.Fatalf("after update a plain install must be a no-op: %+v %v", again, err)
		}
	})
	t.Run("update keeps a hand edit", func(t *testing.T) {
		ins, _, old := setup(t)
		if _, err := ins.Install(ctx, InstallIn{Tools: tool, Link: LinkUpdate}); err != nil {
			t.Fatal(err)
		}
		edited := append(readSkill(t, old), []byte("\nplaceholder edit\n")...)
		if err := os.WriteFile(filepath.Join(old, skills.EntryFile), edited, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ins.Install(ctx, InstallIn{Tools: tool}); !errs.Has(err, errs.SkillLinked) {
			t.Fatalf("an edited target must refuse a plain install: %v", err)
		}
		out, err := ins.Install(ctx, InstallIn{Tools: tool, Link: LinkUpdate})
		if err != nil || len(out.Installed[0].Backup) != 1 {
			t.Fatalf("%+v %v", out, err)
		}
		if b, err := os.ReadFile(out.Installed[0].Backup[0]); err != nil || !bytes.Equal(b, edited) {
			t.Fatal("backup missing or wrong")
		}
	})
	t.Run("update onto a dangling link", func(t *testing.T) {
		ins := Installer{Home: t.TempDir(), Version: shapeVersion, Now: fixedNow}
		dir := filepath.Join(ins.Home, ".claude", "skills", "lore")
		symlink(t, filepath.Join(t.TempDir(), "gone"), dir)
		if _, err := ins.Install(ctx, InstallIn{Tools: tool, Link: LinkUpdate}); !errs.Has(err, errs.SkillLinked) {
			t.Fatalf("want E_SKILL_LINKED, got %v", err)
		}
		out, err := ins.Install(ctx, InstallIn{Tools: tool, Link: LinkReplace})
		if err != nil || !out.Installed[0].Changed {
			t.Fatalf("replace of a dangling link: %+v %v", out, err)
		}
		if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
			t.Fatal("dangling link not replaced by a folder")
		}
	})
	t.Run("bad mode", func(t *testing.T) {
		ins, _, _ := setup(t)
		if _, err := ins.Install(ctx, InstallIn{Tools: tool, Link: "sideways"}); !errs.Has(err, errs.InvalidInput) {
			t.Fatalf("want E_INVALID_INPUT, got %v", err)
		}
	})
}

// TestInstallKeepsPersonsLinks: a plain install into lore's own folder puts
// the person's own linked file back as a link, never as a copy.
func TestInstallKeepsPersonsLinks(t *testing.T) {
	ctx := context.Background()
	ins := Installer{Home: t.TempDir(), Version: "0.1.17", Now: fixedNow}
	tool := []string{toolprofile.NameClaudeCode}
	if _, err := ins.Install(ctx, InstallIn{Tools: tool}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(ins.Home, ".claude", "skills", "lore")
	mine := filepath.Join(t.TempDir(), "mine.md")
	if err := os.WriteFile(mine, []byte("placeholder\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink(t, mine, filepath.Join(dir, "mine.md"))
	ins.Version = shapeVersion
	if _, err := ins.Install(ctx, InstallIn{Tools: tool}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(filepath.Join(dir, "mine.md"))
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the person's link became a copy: %v %v", fi, err)
	}
}

// TestLinkLoop: a loop of links is E_SYMLINK_LOOP, not a hang or "not installed".
func TestLinkLoop(t *testing.T) {
	ins := Installer{Home: t.TempDir(), Version: shapeVersion, Now: fixedNow}
	dir := filepath.Join(ins.Home, ".claude", "skills", "lore")
	other := filepath.Join(ins.Home, "loop")
	symlink(t, other, dir)
	symlink(t, dir, other)
	if _, err := ins.Status(context.Background(), StatusIn{Tools: []string{toolprofile.NameClaudeCode}}); !errs.Has(err, errs.SymlinkLoop) {
		t.Fatalf("status: want E_SYMLINK_LOOP, got %v", err)
	}
	if _, err := ins.Install(context.Background(), InstallIn{Tools: []string{toolprofile.NameClaudeCode}}); !errs.Has(err, errs.SymlinkLoop) {
		t.Fatalf("install: want E_SYMLINK_LOOP, got %v", err)
	}
}

// TestCheckRowNoteOnlyForPlainInstall: the "upgrade lore first" info row
// replaces a runnable install fix only; a linked folder keeps its
// two-choice fix (an upgrade does not change what the link holds).
func TestCheckRowNoteOnlyForPlainInstall(t *testing.T) {
	older := func() (PathLore, bool) { return PathLore{Path: "/placeholder/lore", Version: "0.1.17"}, true }
	plain := ToolStatus{Tool: "claude_code", Status: toolprofile.SkillWarn, Detail: "not installed"}
	r := CheckRow(onboard.CheckToolsSkillInstalled, plain, shapeVersion, older)
	if r.Vars[onboard.VarLevel] != string(onboard.LevelInfo) || r.Vars[onboard.VarFixVariant] != string(onboard.FixAfterUpgrade) {
		t.Fatalf("plain = %+v", r)
	}
	linked := plain
	linked.Linked = true
	r = CheckRow(onboard.CheckToolsSkillInstalled, linked, shapeVersion, older)
	if r.Vars[onboard.VarLevel] != "" || r.Vars[onboard.VarFixVariant] != string(onboard.FixLinked) {
		t.Fatalf("linked = %+v", r)
	}
}

func sameReal(t *testing.T, a, b string) bool {
	t.Helper()
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	return err == nil && ra == rb
}
