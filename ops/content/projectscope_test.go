package content

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// projEnv is an Env whose repo project is id; every other method is the
// nil Env's (never called here).
type projEnv struct {
	Env
	id ids.ID[ids.Project]
}

func (e projEnv) Project(context.Context) (ids.ID[ids.Project], error) { return e.id, nil }

// projRow is a row of project p ("" = no project_id).
func projRow(t *testing.T, p string) row.Row {
	t.Helper()
	f := map[string]value.Value{}
	if p != "" {
		id, err := ids.ParseAny(p)
		if err != nil {
			t.Fatal(err)
		}
		if f[FieldProject], err = value.RefOf(id); err != nil {
			t.Fatal(err)
		}
	}
	return row.Row{Fields: f}
}

// TestProjectScopeKeepsFolderRows (devco D-965): a row written with
// `--to <store>/<folder>` carries the folder's own project (D-718), so a
// content kind's list and search with no --project keep every row of a
// store folder, plus the repo's project's rows anywhere; another repo's
// rows in the repo store or a store's root stay out (v0.1.17), and
// --project keeps that project's rows only, in a folder too.
func TestProjectScopeKeepsFolderRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mine, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	folder, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	o := Ops{Env: projEnv{id: mine}, Noun: "architecturenote"}
	def, err := o.ProjectScopeOf(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	named := ProjectScope{id: mine.String(), named: true} // what --project <mine> resolves to
	every, err := Ops{Env: rootEnv{}, Noun: "architecturenote"}.ProjectScopeOf(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		scope ProjectScope
		src   store.Source
		proj  string
		want  bool
	}{
		{"folder row of the folder's project", def, "kb/app", folder.String(), true},
		{"folder row of no project", def, "kb/app", "", true},
		{"repo-store row of the repo's project", def, store.SourceRepo, mine.String(), true},
		{"store-root row of the repo's project", def, "kb", mine.String(), true},
		{"store-root row of another project", def, "kb", folder.String(), false},
		{"repo-store row of another project", def, store.SourceRepo, folder.String(), false},
		{"--project: folder row of another project", named, "kb/app", folder.String(), false},
		{"--project: folder row of that project", named, "kb/app", mine.String(), true},
		{"store root with no project: any row", every, "kb", folder.String(), true},
	} {
		if got := c.scope.Keeps(c.src, projRow(t, c.proj)); got != c.want {
			t.Errorf("%s: Keeps(%s) = %v, want %v", c.name, c.src, got, c.want)
		}
	}
}

// TestNoContentKindFiltersByRepoProjectOnly guards the class of devco
// D-965: a content kind whose list or search compared a row's project_id
// with the repo's project alone hid every row of a store folder. Each
// kind's read keeps rows through ProjectScope.Keeps, which sees the store.
func TestNoContentKindFiltersByRepoProjectOnly(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("..", "*", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	const bad = "content.Field(r, content.FieldProject) == project"
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, bad) && strings.Contains(line, `project == ""`) {
				t.Errorf("%s:%d filters a read by the repo's project only (hides store-folder rows, D-965); use o.c.ProjectScopeOf and project.Keeps(src, r)", f, i+1)
			}
		}
		if strings.Contains(string(b), "o.c.ProjectFilter(ctx, in.Project)") {
			t.Errorf("%s: a list or search resolves --project with ProjectFilter; use ProjectScopeOf so store-folder rows are kept (D-965)", f)
		}
	}
}
