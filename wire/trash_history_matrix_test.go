package wire

// trash_history_matrix_test.go — later 57 (DELETION 3.6, 3.11, DQ4): the
// `lore trash list|show|restore --from-history` matrix through the real
// ops over a git checkout.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// histList runs trash list --from-history in a and indexes it by id.
func histList(t *testing.T, a *App) map[string]opstrash.Item {
	t.Helper()
	out, err := run(t, a, opstrash.OpList, `{"from_history":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var l opstrash.ListOut
	if err = json.Unmarshal([]byte(out), &l); err != nil {
		t.Fatal(err)
	}
	got := map[string]opstrash.Item{}
	for _, it := range l.Data {
		got[it.ID] = it
	}
	return got
}

// histPurgeAny purges the row id (any table) in the repo store.
func histPurgeAny(t *testing.T, a *App, id string) {
	t.Helper()
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eng.Purge(background(), engine.Ref{Store: store.SourceRepo, ID: aid}); err != nil {
		t.Fatalf("purge %s: %v", id, err)
	}
}

// commitAll commits every change of repo.
func commitAll(t *testing.T, repo, msg string) {
	t.Helper()
	metaGit(t, repo, "add", "-A")
	metaGit(t, repo, "commit", "-q", "--allow-empty", "-m", msg)
}

// TestFromHistoryMatrix (DELETION 3.6): a row purged to a tombstone is
// listed with its purger; a row an older lore hard-deleted (file removed)
// is listed with deleter "?"; a file deleted and later re-added is not
// listed; a live row is not listed. show --from-history prints the last
// content with a resource kind's sensitive field hidden (3.11) unless
// --show-sensitive; restore --from-history is always E_PURGED (DQ4), for a
// listed id and an unknown one; show of an id that is not in history is
// E_NOT_FOUND.
func TestFromHistoryMatrix(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	for _, in := range []struct{ op, body string }{
		{settings.OpKindAdd, `{"kind":"host"}`},
		{settings.OpFieldAdd, `{"kind":"host","field":"ip","type":"text","sensitive":true}`},
		{settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`},
	} {
		if _, err := run(t, a, in.op, in.body, true); err != nil {
			t.Fatalf("%s: %v", in.op, err)
		}
	}
	out, err := run(t, a, settings.OpResAdd, `{"kind":"host","name":"hub","set":["ip=203.0.113.10","os=debian-12"]}`, true)
	if err != nil {
		t.Fatal(err)
	}
	resID := idOf(t, out)
	tomb := histAddMemory(t, a, "", "purged to a tombstone")
	hard := histAddMemory(t, a, "", "hard deleted by an older lore")
	back := histAddMemory(t, a, "", "deleted then re-added")
	alive := histAddMemory(t, a, "", "still alive")
	commitAll(t, repo, "rows")

	histPurgeAny(t, a, tomb)
	histPurgeAny(t, a, resID)
	data := filepath.Join(repo, ".lore", "data", schema.TableMemories)
	backDoc := mustRead(t, filepath.Join(data, back+".json"))
	for _, id := range []string{hard, back} {
		metaGit(t, repo, "rm", "-q", filepath.Join(".lore", "data", schema.TableMemories, id+".json"))
	}
	commitAll(t, repo, "purge and hard delete")
	if err = os.WriteFile(filepath.Join(data, back+".json"), backDoc, 0o600); err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "re-add")

	got := histList(t, a)
	if it, ok := got[tomb]; !ok || it.DeletedBy == "?" || it.DeletedBy == "" {
		t.Errorf("tombstone purge: %+v (listed %v)", it, ok)
	}
	// H-14: a row file a commit removed (an older lore's hard delete, a
	// git rm) is listed as removed by that commit's author, not purged
	if it, ok := got[hard]; !ok || !it.Removed || it.DeletedBy == "?" || it.DeletedBy == "" || it.GitRestore == "" {
		t.Errorf("old hard delete: %+v (listed %v), want removed by the commit's author with its git command", it, ok)
	}
	if _, ok := got[resID]; !ok {
		t.Errorf("purged resource not listed: %v", got)
	}
	for _, id := range []string{back, alive} {
		if _, ok := got[id]; ok {
			t.Errorf("%s listed, but its file is there", id)
		}
	}

	for _, c := range []struct {
		yes, show bool
		want      string
		never     string
	}{
		{want: "debian-12", never: "203.0.113.10"},
		{yes: true, show: true, want: "203.0.113.10"},
	} {
		in := `{"id":"` + resID + `","from_history":true}`
		if c.show {
			in = `{"id":"` + resID + `","from_history":true,"show_sensitive":true}`
		}
		shown, serr := run(t, a, opstrash.OpShow, in, c.yes)
		if serr != nil {
			t.Fatal(serr)
		}
		if !strings.Contains(shown, c.want) || (c.never != "" && strings.Contains(shown, c.never)) {
			t.Fatalf("show --from-history (show_sensitive %v): %s", c.show, shown)
		}
	}
	if shown, serr := run(t, a, opstrash.OpShow, `{"id":"`+tomb+`","from_history":true}`, false); serr != nil || !strings.Contains(shown, "purged to a tombstone") {
		t.Fatalf("show --from-history of a memory: %s %v", shown, serr)
	}
	if _, serr := run(t, a, opstrash.OpShow, `{"id":"`+alive+`","from_history":true}`, false); !errs.Has(serr, errs.NotFound) {
		t.Fatalf("show --from-history of a live row: %v, want E_NOT_FOUND", serr)
	}
	for _, id := range []string{tomb, "mem_0e45506e64357c94a87e70ef1c6bb0ff"} {
		if _, rerr := run(t, a, opstrash.OpRestore, `{"target":"`+id+`","from_history":true}`, true); !errs.Has(rerr, errs.Purged) {
			t.Fatalf("restore --from-history %s: %v, want E_PURGED", id, rerr)
		}
	}
	// a removed row is not purged: the refusal names the commit that removed it (H-14)
	if _, rerr := run(t, a, opstrash.OpRestore, `{"target":"`+hard+`","from_history":true}`, true); rerr == nil || errs.Has(rerr, errs.Purged) || !strings.Contains(rerr.Error(), "removed by commit") {
		t.Fatalf("restore --from-history %s: %v, want the commit that removed it", hard, rerr)
	}
}

// TestFromHistoryShallowAndEmpty: a checkout with no commit lists nothing
// (no error); a shallow clone is E_UNSUPPORTED naming git fetch
// --unshallow, so an incomplete history is never shown as complete.
func TestFromHistoryShallowAndEmpty(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	if got := histList(t, a); len(got) != 0 {
		t.Fatalf("no commits: %v", got)
	}
	histAddMemory(t, a, "", "one")
	commitAll(t, repo, "one")
	histAddMemory(t, a, "", "two")
	commitAll(t, repo, "two")
	clone := filepath.Join(t.TempDir(), "shallow")
	gitIn(t, repo, nil, "clone", "-q", "--depth", "1", fileuri.URL(repo, ""), clone)
	b := openWith(t, clone, Options{})
	_, err := run(t, b, opstrash.OpList, `{"from_history":true}`, false)
	var e *errs.Error
	if !errs.Has(err, errs.Unsupported) || !errors.As(err, &e) || !strings.Contains(e.Hint, "--unshallow") {
		t.Fatalf("shallow clone: %v, want E_UNSUPPORTED with an unshallow hint", err)
	}
}
