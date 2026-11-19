package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/schema"
)

// TestPreCommitTakesStagedRowDeletions (devco D-715): row files removed
// and staged by the person (`git add -A .lore/data`), then
// `LORE_ALLOW_ROW_DROP=1 git commit`, failed with E_INTERNAL "pre-commit:
// stage .lore/data" while the same removal unstaged went through. With a
// row of the commit on the live ref the hook stages file by file, and
// naming a staged deletion to `git add` fails ("pathspec did not match
// any files"). A path already staged whole is not staged again: the commit
// goes through staged or unstaged, and the deletions stay in it.
func TestPreCommitTakesStagedRowDeletions(t *testing.T) {
	for _, staged := range []bool{true, false} {
		name := "unstaged"
		if staged {
			name = "staged"
		}
		t.Run(name, func(t *testing.T) {
			f := newLiveFixture(t)
			top := f.s.top
			const rule = ".lore/data/rules/rul_01a1dd52216a707387008bfd35ae45ba.json"
			const memo = ".lore/data/memories/mem_01a1dd5c74427dec80871af648c3aaef.json"
			const task = "tsk_01a1dd5c74427dec80871af648c3aaf0"
			for rel, body := range map[string]string{
				rule:                                 `{"_v":2,"_table":"rules","id":"rul_01a1dd52216a707387008bfd35ae45ba","severity":"must","summary":"tests green before push","body":"Run the full test suite before every push"}`,
				memo:                                 `{"_v":2,"_table":"memories","id":"mem_01a1dd5c74427dec80871af648c3aaef","body":"retry the flaky job once"}`,
				".lore/data/tasks/" + task + ".json": `{"_v":2,"_table":"tasks","id":"` + task + `","title":"open work","status":"todo"}`,
			} {
				full := filepath.Join(top, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(body+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			gitT(t, top, "add", "-A")
			gitT(t, top, "commit", "-q", "--no-verify", "-m", "rows")
			// the task is also on the live ref: the hook stages file by file
			k := live.RowKey{Table: schema.TableTasks, ID: task}
			v := live.NewVersion(nil, canonjson.Object{"id": canonjson.String(task), "title": canonjson.String("open work"), "status": canonjson.String("todo")}, f.s.clk.Now(), liveAnonymousWriter)
			if _, err := f.s.ref.Write(f.ctx, live.WriteRequest{Folder: live.RootFolder, Rows: map[live.RowKey]live.File{k: v}, BinaryVersion: "0.1.18"}); err != nil {
				t.Fatal(err)
			}
			f.s.release()
			for _, rel := range []string{rule, memo, ".lore/data/tasks/" + task + ".json"} {
				if err := os.Remove(filepath.Join(top, filepath.FromSlash(rel))); err != nil {
					t.Fatal(err)
				}
			}
			if staged {
				gitT(t, top, "add", "-A", ".lore/data")
			}
			if err := os.WriteFile(filepath.Join(top, "other.txt"), []byte("x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			gitT(t, top, "add", "other.txt")
			t.Chdir(top)
			t.Setenv(EnvAllowRowDrop, envAllowOn)
			if err := f.s.h.preCommit(context.Background()); err != nil {
				t.Fatalf("the commit of the removed rows was refused: %v", err)
			}
			raw, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: top, Args: []string{"diff", "--cached", "--name-status"}})
			if err != nil {
				t.Fatal(err)
			}
			out := string(raw)
			for _, rel := range []string{rule, memo} {
				if !strings.Contains(out, "D\t"+rel) {
					t.Errorf("%s is not deleted in the commit:\n%s", rel, out)
				}
			}
		})
	}
}
