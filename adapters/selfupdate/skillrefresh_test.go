package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpgradeRefreshesSkillsWithNewBinary (H-185, devco:D-927): after the
// swap `lore upgrade` runs the NEW binary's `skill refresh` and shows its
// lines; it never copies a bundle itself (the old copy wrote through a
// link into a source checkout). When the new binary cannot run it, the
// stale-skill hint is printed instead.
func TestUpgradeRefreshesSkillsWithNewBinary(t *testing.T) {
	home := t.TempDir()
	var ran string
	u := Updater{UserHome: home, SkillRefresh: func(_ context.Context, bin string) (string, error) {
		ran = bin
		return "codex: refreshed the lore skill in /h/.codex/skills/lore from lore 0.1.1 to lore 0.1.40\n", nil
	}}
	var out bytes.Buffer
	if err := u.refreshSkills(context.Background(), &out, "/new/lore", "0.1.40"); err != nil {
		t.Fatal(err)
	}
	if ran != "/new/lore" || out.String() != "  codex: refreshed the lore skill in /h/.codex/skills/lore from lore 0.1.1 to lore 0.1.40\n" {
		t.Fatalf("ran %q, out %q", ran, out.String())
	}

	// The new binary cannot run: an old skill still gets the hint.
	dir := filepath.Join(home, ".codex", "skills", "lore")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, skillEntryFile), []byte("---\nname: lore\nlore_version: 0.1.1\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u.SkillRefresh = func(context.Context, string) (string, error) { return "", errors.New("exec format error") }
	out.Reset()
	if err := u.refreshSkills(context.Background(), &out, "/new/lore", "0.1.40"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "skills are older than lore 0.1.40: lore skill install --all") {
		t.Fatalf("fallback hint missing: %q", out.String())
	}
}
