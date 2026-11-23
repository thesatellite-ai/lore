//go:build journeys && !windows

package journeys

// skillrefresh_test.go — H-185 (devco:D-927): a package-manager upgrade
// swaps the lore binary and never touched the installed lore skill, so
// agents read an older skill and said lore had no `lore search rebuild` or
// `lore ask`. Now any lore command (at most once a day, and at once after
// the binary changed) re-installs a stale skill lore owns and says so on
// one stderr line; a skill linked from a lagging lore source checkout is
// never written: the line names `git -C <checkout> pull`.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// skillRefreshStamp is the release the journey's binary claims to be.
const skillRefreshStamp = "0.1.40"

// skillRefreshRun runs bin with HOME and LORE_HOME under home.
func skillRefreshRun(t *testing.T, bin, home, dir string, args ...string) (stdout, stderr string) {
	t.Helper()
	c := exec.Command(bin, args...)
	c.Dir = dir
	c.Env = []string{"HOME=" + home, "LORE_HOME=" + filepath.Join(home, ".lore"), "PATH=" + os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL=" + filepath.Join(home, ".gitconfig"), "GIT_CONFIG_NOSYSTEM=1", "LORE_NO_UPGRADE_NOTICE=1", "LORE_HUMAN=1",
		"TMPDIR=" + os.TempDir()}
	var so, se strings.Builder
	c.Stdout, c.Stderr = &so, &se
	if err := c.Run(); err != nil {
		t.Fatalf("lore %s: %v\n%s%s", strings.Join(args, " "), err, so.String(), se.String())
	}
	return so.String(), se.String()
}

func TestJourneySkillRefreshAfterUpgrade(t *testing.T) {
	t.Parallel()
	bin := filepath.Join(t.TempDir(), "lore")
	b := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-tags", "loretest", "-ldflags", "-X main.version="+skillRefreshStamp, "-o", bin, "./cmd/lore")
	b.Dir = filepath.Join("..", "..")
	b.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := b.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = Ada Test\n\temail = ada@example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(home, ".codex", "skills", "lore")
	skillRefreshRun(t, bin, home, home, "skill", "install", "--tool", "codex")
	entry := filepath.Join(skill, "SKILL.md")
	cur, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	// What an older lore installed: an older stamp, no upkeep commands.
	old := strings.ReplaceAll(strings.ReplaceAll(string(cur), "lore_version: "+skillRefreshStamp, "lore_version: 0.1.1"), "lore search rebuild", "")
	if err := os.WriteFile(entry, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	so, _ := skillRefreshRun(t, bin, home, home, "skill", "status", "--tool", "codex")
	if !strings.Contains(so, "codex        stale 0.1.1") || !strings.Contains(so, "run lore skill refresh") {
		t.Fatalf("skill status does not call the 0.1.1 skill stale with its fix:\n%s", so)
	}
	// Any command refreshes it (lore skill status itself never writes).
	_, se := skillRefreshRun(t, bin, home, home, "whoami")
	if !strings.Contains(se, "lore: refreshed the lore skill for codex to lore "+skillRefreshStamp) {
		t.Fatalf("whoami stderr does not say the skill was refreshed:\n%s", se)
	}
	now, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(now), "lore_version: "+skillRefreshStamp) || !strings.Contains(string(now), "lore search rebuild") {
		t.Fatal("the skill was not re-installed with this lore's bundle")
	}
	if _, se := skillRefreshRun(t, bin, home, home, "whoami"); strings.Contains(se, "skill") {
		t.Fatalf("the second command checked again within the day:\n%s", se)
	}

	// A skill linked from a lore source checkout that lags: warn with the
	// pull, never write into the checkout.
	co := filepath.Join(t.TempDir(), "lore")
	for _, d := range []string{filepath.Join(co, ".git"), filepath.Join(co, "skills")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(co, "go.mod"), []byte("module github.com/thesatellite-ai/lore\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lag := strings.ReplaceAll(old, "lore_version: 0.1.1", "lore_version: dev")
	if err := os.WriteFile(filepath.Join(co, "skills", "SKILL.md"), []byte(lag), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(skill); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(co, "skills"), skill); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, ".lore", "state", "skill-check")); err != nil { // the next day
		t.Fatal(err)
	}
	_, se = skillRefreshRun(t, bin, home, home, "whoami")
	if want := "run git -C " + co + " pull"; !strings.Contains(se, "lore: the codex skill is stale") || !strings.Contains(se, want) {
		t.Fatalf("a lagging checkout link is not flagged with %q:\n%s", want, se)
	}
	if got, _ := os.ReadFile(filepath.Join(co, "skills", "SKILL.md")); string(got) != lag {
		t.Fatal("lore wrote into the source checkout")
	}
}
