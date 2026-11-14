package wire

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/schema"
)

// TestHistoryNamesARowAGitRmRemoved (H-14): a row file removed by a plain
// `git rm` + commit is no purge: --from-history names the commit's author
// (never "?"), marks it removed and gives the git checkout that brings it
// back; `trash restore` (with or without --from-history) answers with the
// same command instead of the generic not-found, and the command works.
func TestHistoryNamesARowAGitRmRemoved(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	id := histAddMemory(t, a, "", "the client replies on chat")
	file := ".lore/data/" + schema.TableMemories + "/" + id + ".json"
	metaGit(t, repo, "add", "-A")
	metaGit(t, repo, "commit", "-q", "-m", "rows")
	metaGit(t, repo, "rm", "-q", file)
	metaGit(t, repo, "-c", "user.name=Omar Example", "commit", "-q", "-m", "Remove the chat note")

	got := histByID(t, a)
	r, ok := got[id]
	if !ok || !r.Removed || r.RemovedBy != "Omar Example" || !strings.HasPrefix(r.GitRestore, "git checkout ") ||
		!strings.HasSuffix(r.GitRestore, "^ -- :/"+file) {
		t.Fatalf("a git rm'd row: %+v (found %v)", r, ok)
	}
	list, err := run(t, a, opstrash.OpList, `{"from_history":true}`, true)
	if err != nil || !strings.Contains(list, `"removed_by_commit":true`) || !strings.Contains(list, `"deleted_by":"Omar Example"`) {
		t.Fatalf("trash list --from-history: %s %v", list, err)
	}
	for _, in := range []string{`{"target":"` + id + `"}`, `{"target":"` + id + `","from_history":true}`} {
		_, err = run(t, a, opstrash.OpRestore, in, true)
		var le *errs.Error
		if !errs.Has(err, errs.NotFound) || !errors.As(err, &le) || !strings.Contains(le.Hint, r.GitRestore) || !strings.Contains(le.Message, "Omar Example") {
			t.Fatalf("trash restore %s: %v", in, err)
		}
	}
	metaGit(t, repo, strings.Fields(strings.TrimPrefix(r.GitRestore, "git "))...)
	if _, err = run(t, a, opsmemory.OpShow, `{"id":"`+id+`"}`, false); err != nil {
		t.Fatalf("the printed command did not bring the row back: %v", err)
	}
}
