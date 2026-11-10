package architecturenote_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// spec is v0.1.17's architecturenote facts (read from its real output, 2026-11-04).
var spec = contenttest.Spec{
	Noun: "architecturenote", Table: schema.TableArchitectureNotes, Prefix: "anr",
	KindList: "architectureNote.list", KindShow: "architectureNote.show", KindSearch: "architecturenote.search",
	Empty: "(no architecturenotes)\n", Badge: "AR", TitleKey: "title",
	ListLine: contenttest.TitleLine("AR", "title"),
	Add: func(title, body string) string {
		return `{"title":` + contenttest.Quote(title) + `,"body":` + contenttest.Quote(body) + `}`
	},
	RecordKeys: []string{"id", "created_at", "updated_at", "project_id", "title", "body", "created_by_actor_id"},
	HitKeys:    []string{"body", "title"},
	EditWord:   "architecture-note",
	EditTitle: func(id, v string) string {
		return `{"id":"` + id + `","title":` + contenttest.Quote(v) + `}`
	},
}

func TestArchitecturenoteConformance(t *testing.T) {
	contenttest.Check(t, spec, func(t *testing.T) contenttest.Runner { return opstest.New(t) })
}

// TestRebind: edit --rebind-repo moves a note to a repo of the project
// (list --repo then shows it, list --master-only does not);
// --rebind-master moves it back; both at once and an unknown repo are
// refused. The repo row is seeded as `lore repo add` writes it.
func TestRebind(t *testing.T) {
	h := opstest.New(t)
	repo := seedRepo(t, h, "web")
	id := h.ID(h.Must("architecturenote.add", `{"title":"T","body":"b"}`))
	h.Fails("architecturenote.edit", `{"id":"`+id+`","rebind_repo":"web","rebind_master":true}`, errs.InvalidInput)
	h.Fails("architecturenote.edit", `{"id":"`+id+`","rebind_repo":"nope"}`, errs.NotFound)
	if got := opstest.Text(h.Must("architecturenote.edit", `{"id":"`+id+`","rebind_repo":"web"}`)); got != "✓ architecture-note "+id+" updated\n" {
		t.Errorf("rebind text %q", got)
	}
	if got := contenttest.Str(t, h.JSON(h.Must("architecturenote.show", `{"id":"`+id+`"}`)), "data", "repo_id"); got != repo {
		t.Errorf("repo_id %q, want %s", got, repo)
	}
	listed := func(in string) bool { return strings.Contains(h.JSON(h.Must("architecturenote.list", in)), id) }
	if listed(`{"master_only":true}`) || !listed(`{"repo":"web"}`) || !listed(`{"repo":"`+repo+`","no_inherit":true}`) || !listed(`{"all_repos":true}`) || listed("") {
		t.Error("scope filters disagree with repo_id after --rebind-repo")
	}
	h.Must("architecturenote.edit", `{"id":"`+id+`","rebind_master":true}`)
	if got := contenttest.Str(t, h.JSON(h.Must("architecturenote.show", `{"id":"`+id+`"}`)), "data", "repo_id"); got != "" {
		t.Errorf("repo_id after --rebind-master %q", got)
	}
	if !listed("") || listed(`{"repo":"web","no_inherit":true}`) {
		t.Error("scope filters disagree with repo_id after --rebind-master")
	}
}

// seedRepo writes a repos row (mount name) of the harness project and
// returns its id.
func seedRepo(t *testing.T, h *opstest.H, mount string) string {
	t.Helper()
	ctx := context.Background()
	m, err := value.TextOf(mount)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := ids.ParseAny(h.Project)
	if err != nil {
		t.Fatal(err)
	}
	p, err := value.RefOf(pid)
	if err != nil {
		t.Fatal(err)
	}
	w, err := h.App.Create(ctx, store.SourceRepo, schema.TableRepos, map[string]value.Value{"mount_name": m, "project_id": p})
	if err != nil {
		t.Fatal(err)
	}
	return w.Row.ID.String()
}
