package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/skills"
)

// refreshVersion is the release binary of the refresh tests.
const refreshVersion = "0.1.40"

// codexSkillDir is the codex profile's skill folder under home.
func codexSkillDir(home string) string { return filepath.Join(home, ".codex", "skills", "lore") }

// TestRefreshStaleSkill (H-185, devco:D-927): a skill an older lore
// installed is "stale" in `lore skill status` with the command that fixes
// it, and skill.refresh re-installs it with this binary's bundle and stamp.
// Before the fix nothing ever refreshed it and status only said "warn".
func TestRefreshStaleSkill(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	old := Installer{Home: home, Version: "0.1.1", Now: fixedNow}
	if _, err := old.Install(ctx, InstallIn{Tools: []string{toolprofile.NameCodex}}); err != nil {
		t.Fatal(err)
	}
	dir := codexSkillDir(home)
	// The older bundle lacked the maintenance commands.
	entry := readSkill(t, dir)
	if err := os.WriteFile(filepath.Join(dir, skills.EntryFile), []byte(strings.ReplaceAll(string(entry), "lore search rebuild", "")), 0o600); err != nil {
		t.Fatal(err)
	}
	ins := Installer{Home: home, Version: refreshVersion, Now: fixedNow}
	st, err := ins.Status(ctx, StatusIn{Tools: []string{toolprofile.NameCodex}})
	if err != nil {
		t.Fatal(err)
	}
	ts := st.Tools[0]
	if !ts.Stale || ts.Fix != FixRefresh {
		t.Fatalf("status of a 0.1.1 skill under lore %s: stale=%v fix=%q, want stale with %q", refreshVersion, ts.Stale, ts.Fix, FixRefresh)
	}
	if txt := st.Text(); !strings.Contains(txt, "codex        stale") || !strings.Contains(txt, "run "+FixRefresh) {
		t.Fatalf("status text does not say stale with the fix:\n%s", txt)
	}
	out, err := ins.Refresh(ctx, RefreshIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Refreshed) != 1 || out.Refreshed[0].Tool != toolprofile.NameCodex || out.Refreshed[0].From != "0.1.1" || len(out.Left) != 0 {
		t.Fatalf("refresh: %+v", out)
	}
	got := readSkill(t, dir)
	if v, _ := toolprofile.ReadSkillVersion(got); v != refreshVersion || !strings.Contains(string(got), "lore search rebuild") {
		t.Fatalf("after refresh the skill is %q and names lore search rebuild=%v", v, strings.Contains(string(got), "lore search rebuild"))
	}
	again, err := ins.Refresh(ctx, RefreshIn{})
	if err != nil || len(again.Refreshed)+len(again.Left) != 0 || !strings.Contains(again.Text(), "is current") {
		t.Fatalf("second refresh: %+v %v", again, err)
	}
}

// TestRefreshRestampsSameFiles (H-185): an upgrade that did not change
// the skill's text leaves the same files under an older line; refresh
// restamps it, so doctor's version warning goes away.
func TestRefreshRestampsSameFiles(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	if _, err := (Installer{Home: home, Version: "0.1.39", Now: fixedNow}).Install(ctx, InstallIn{Tools: []string{toolprofile.NameCodex}}); err != nil {
		t.Fatal(err)
	}
	ins := Installer{Home: home, Version: refreshVersion, Now: fixedNow}
	out, err := ins.Refresh(ctx, RefreshIn{})
	if err != nil || len(out.Refreshed) != 1 || out.Refreshed[0].From != "0.1.39" {
		t.Fatalf("refresh of a 0.1.39 stamp: %+v %v", out, err)
	}
	if v, _ := toolprofile.ReadSkillVersion(readSkill(t, codexSkillDir(home))); v != refreshVersion {
		t.Fatalf("stamp %q, want %s", v, refreshVersion)
	}
}

// TestRefreshLeavesSourceCheckout (H-185): a skill folder linked into a
// lore source checkout that lags this release is stale with the exact
// `git -C <checkout> pull`, and refresh never writes into the checkout.
// Before the fix status said "skip: skill is linked from a source
// checkout" and nothing named the pull.
func TestRefreshLeavesSourceCheckout(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	co := filepath.Join(t.TempDir(), "lore")
	for _, d := range []string{filepath.Join(co, ".git"), filepath.Join(co, "skills")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(co, "go.mod"), []byte(loreModulePrefix+"\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, _, err := skills.Files()
	if err != nil {
		t.Fatal(err)
	}
	lagging := strings.ReplaceAll(string(b[skills.EntryFile]), "lore ask", "")
	if err := os.WriteFile(filepath.Join(co, "skills", skills.EntryFile), []byte(lagging), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink(t, filepath.Join(co, "skills"), codexSkillDir(home))
	before := snapshot(t, filepath.Join(co, "skills"))
	ins := Installer{Home: home, Version: refreshVersion, Now: fixedNow}
	st, err := ins.Status(ctx, StatusIn{Tools: []string{toolprofile.NameCodex}})
	if err != nil {
		t.Fatal(err)
	}
	ts := st.Tools[0]
	wantFix := "git -C " + co + " pull"
	if !ts.Stale || ts.Fix != wantFix || ts.Checkout != co {
		t.Fatalf("linked checkout: stale=%v fix=%q checkout=%q, want fix %q", ts.Stale, ts.Fix, ts.Checkout, wantFix)
	}
	out, err := ins.Refresh(ctx, RefreshIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Refreshed) != 0 || len(out.Left) != 1 || out.Left[0].Fix != wantFix {
		t.Fatalf("refresh of a checkout link: %+v", out)
	}
	if after := snapshot(t, filepath.Join(co, "skills")); after != before {
		t.Fatal("refresh wrote into the source checkout")
	}
}

// TestRefreshNotStale: a development binary compares nothing, a hand edit
// at this binary's version and a newer skill are not stale, and a
// checkout that equals the bundle is current.
func TestRefreshNotStale(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		installed string
		binary    string
		edit      bool
	}{
		{"dev binary over an old skill", "0.1.1", "dev", false},
		{"hand edit at the same release", refreshVersion, refreshVersion, true},
		{"skill newer than the binary", "0.2.0", refreshVersion, false},
		{"current skill", refreshVersion, refreshVersion, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			if _, err := (Installer{Home: home, Version: c.installed, Now: fixedNow}).Install(ctx, InstallIn{Tools: []string{toolprofile.NameCodex}}); err != nil {
				t.Fatal(err)
			}
			if c.edit {
				p := filepath.Join(codexSkillDir(home), skills.EntryFile)
				if err := os.WriteFile(p, append(readSkill(t, codexSkillDir(home)), "\nmy own note\n"...), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ins := Installer{Home: home, Version: c.binary, Now: fixedNow}
			out, err := ins.Refresh(ctx, RefreshIn{})
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Refreshed)+len(out.Left) != 0 {
				t.Fatalf("refreshed what is not stale: %+v", out)
			}
		})
	}
}
