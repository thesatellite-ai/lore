package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire"
)

// TestPreCommitTakesMetaMovedOut (devco D-719): `lore store adopt --move`
// of a repo on the lore 0.1.17 format moves every note out and removes the
// repo's _meta.json with the last row (nothing is left to migrate); the
// commit it runs or prints was refused by the pre-commit ("deleted; it
// holds the project's id and format"). A removal adopt recorded commits;
// the same deletion by hand is still refused (H-3).
func TestPreCommitTakesMetaMovedOut(t *testing.T) {
	for _, recorded := range []bool{true, false} {
		name := "by-hand"
		if recorded {
			name = "recorded-by-adopt"
		}
		t.Run(name, func(t *testing.T) {
			f := newLiveFixture(t)
			top := f.s.top
			const rule = ".lore/data/rules/rul_01a1dd52216a707387008bfd35ae45ba.json"
			for rel, body := range map[string]string{
				metaFilePath: `{"_v":1,"project_id":"prj_01a1dd52216a707387008bfd35ae45bb"}`,
				rule:         `{"_v":1,"_table":"rules","id":"rul_01a1dd52216a707387008bfd35ae45ba","severity":"must","body":"Run the linter before every push"}`,
			} {
				full := filepath.Join(top, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(body+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			gitT(t, top, "add", "-A", "-f", ".lore/data")
			gitT(t, top, "commit", "-q", "--no-verify", "-m", "old notes")
			f.s.release()
			for _, rel := range []string{rule, metaFilePath} {
				if err := os.Remove(filepath.Join(top, filepath.FromSlash(rel))); err != nil {
					t.Fatal(err)
				}
			}
			moved := []string{rule}
			if recorded {
				moved = append(moved, metaFilePath)
			}
			if err := wire.RecordMovedOut(f.ctx, top, "acme/ledger", wire.MovedByAdopt, moved); err != nil {
				t.Fatal(err)
			}
			gitT(t, top, "add", "-A", ".lore/data")
			t.Chdir(top)
			err := f.s.h.preCommit(context.Background())
			if recorded && err != nil {
				t.Fatalf("the commit of the moved-out _meta.json was refused: %v", err)
			}
			if !recorded && (err == nil || !strings.Contains(err.Error(), brokenMetaDeleted)) {
				t.Fatalf("a _meta.json deleted by hand must still be refused, got %v", err)
			}
		})
	}
}
