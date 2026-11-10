package wire

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestDoctorProjects (b-flow3): one project row gives no line; a second
// one (two `lore init`s merged) is a warning naming both ids and `lore
// sync fix-projects`; after fix-projects the warning is gone.
func TestDoctorProjects(t *testing.T) {
	t.Parallel()
	a := openWith(t, gitRepo(t), Options{})
	withProject(t, a)
	ctx := background()
	if got, err := a.DoctorProjects(ctx); err != nil || len(got) != 0 {
		t.Fatalf("one project: %+v %v", got, err)
	}
	name, err := value.TextOf("second")
	if err != nil {
		t.Fatal(err)
	}
	w, err := a.Create(ctx, store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": name})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.DoctorProjects(ctx)
	if err != nil || len(got) != 1 || got[0].Level != DoctorWarning || got[0].Check != DoctorCheckProjects ||
		!strings.Contains(got[0].Message, "2 project rows") || !strings.Contains(got[0].Message, w.Row.ID.String()) ||
		!strings.Contains(got[0].Message, fixProjectsCommand) {
		t.Fatalf("two projects: %+v %v", got, err)
	}
	if _, ferr := a.FixProjects(ctx, "", false); ferr != nil {
		t.Fatal(ferr)
	}
	if got, err = a.DoctorProjects(ctx); err != nil || len(got) != 0 {
		t.Fatalf("after fix-projects: %+v %v", got, err)
	}
}

// TestLeakBlockedOtherProjectNamesFixProjects (b-flow3): a commit refused
// for a "row of another project" names `lore sync fix-projects`, never
// `lore move` / delete of that row; other findings keep their own fix.
func TestLeakBlockedOtherProjectNamesFixProjects(t *testing.T) {
	t.Parallel()
	path := ".lore/data/memories/mem_01a1a6841fc0784d92a62b0acf148b28.json"
	other := leakcheck.Finding{Rule: leakcheck.RuleID, Where: "commit " + path, RowID: "mem_01a1a6841fc0784d92a62b0acf148b28", Path: path, Detail: leakcheck.DetailOtherProject}
	for _, where := range []string{LeakWhereCommit, LeakWherePush} {
		err := leakBlocked(where, []leakcheck.Finding{other}, false)
		var e *errs.Error
		if !errors.As(err, &e) || !strings.Contains(e.Hint, fixProjectsCommand) || strings.Contains(e.Hint, "lore move") || strings.Contains(e.Hint, "delete") {
			t.Fatalf("%s: hint %q", where, e.Hint)
		}
	}
	idHit := other
	idHit.Detail = "id belongs to store agency"
	err := leakBlocked(LeakWhereCommit, []leakcheck.Finding{idHit}, false)
	var e *errs.Error
	if !errors.As(err, &e) || !strings.Contains(e.Hint, "lore move") || strings.Contains(e.Hint, fixProjectsCommand) {
		t.Fatalf("an internal-id finding keeps the move hint: %q", e.Hint)
	}
}
