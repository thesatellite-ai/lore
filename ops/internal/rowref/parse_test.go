package rowref

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// qualProject is a project id a qualified pointer names (any 32 hex digits
// form a valid id; this checkout holds no such project).
const qualProject = "prj_01a1a5aca7f57659be7320c5f4b70099"

// TestParseMatrix (B11 port of saas/pkg/aicoder/xref TestParse): the --ref
// grammar of F15 pointers. A bare id must be a loaded live row; a
// qualified <prj_…>:<id> may point outside this checkout and keeps its
// project; commit:<sha> is a commit pointer; surrounding space is trimmed
// and an empty value is skipped. Every malformed value is refused with an
// input code and never reaches the store (the old xref.ErrBadRef cases:
// "", ":tsk", "acme:", "a:b c", plus the commit and unknown-table forms).
func TestParseMatrix(t *testing.T) {
	ctx := context.Background()
	ok := []struct {
		name, in         string
		project, id, sha string
	}{
		{"bare loaded id", visibleID, "", visibleID, ""},
		{"bare id, space trimmed", "  " + visibleID + " ", "", visibleID, ""},
		{"qualified id outside the checkout", qualProject + ":" + missingID, qualProject, missingID, ""},
		{"qualified, space trimmed", " " + qualProject + ":" + missingID + "  ", qualProject, missingID, ""},
		{"commit pointer", "commit:abc123", "", "", "abc123"},
	}
	for _, c := range ok {
		t.Run(c.name, func(t *testing.T) {
			env := &fakeEnv{}
			refs, err := Parse(ctx, env, []string{c.in})
			if err != nil || len(refs) != 1 {
				t.Fatalf("Parse(%q) = %+v, %v", c.in, refs, err)
			}
			r := refs[0]
			if r.Project != c.project || r.SHA != c.sha || (c.id != "" && r.ID.String() != c.id) {
				t.Fatalf("Parse(%q) = %+v, want project %q id %q sha %q", c.in, r, c.project, c.id, c.sha)
			}
			if env.creates != 0 {
				t.Fatal("Parse wrote a row")
			}
		})
	}
	if refs, err := Parse(ctx, &fakeEnv{}, []string{"", "   "}); err != nil || len(refs) != 0 {
		t.Fatalf("empty values are skipped: %+v %v", refs, err)
	}

	bad := []struct {
		name, in string
		code     errs.Code
	}{
		{"empty project", ":" + visibleID, errs.InvalidInput},
		{"project not an id", "acme/app:" + visibleID, errs.InvalidInput},
		{"empty id after project", qualProject + ":", errs.InvalidID},
		{"space inside", "a:b c", errs.InvalidInput},
		{"not an id", "tsk_01", errs.InvalidID}, // B-ERRS-1: malformed, never not found
		{"empty commit", "commit:", errs.InvalidInput},
		{"commit with space", "commit:ab cd", errs.InvalidInput},
		{"commit with slash", "commit:a/b", errs.InvalidInput},
		{"bare id nowhere", missingID, errs.NotFound},
		{"bare id deleted", deletedID, errs.NotFound},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			env := &fakeEnv{}
			refs, err := Parse(ctx, env, []string{c.in})
			if err == nil || refs != nil {
				t.Fatalf("Parse(%q) = %+v, want %s", c.in, refs, c.code)
			}
			if got := errs.CodeOf(err); got != c.code {
				t.Fatalf("Parse(%q) code %s (%v), want %s", c.in, got, err, c.code)
			}
			if env.creates != 0 {
				t.Fatal("a refused Parse wrote a row")
			}
		})
	}
}

// TestOutWhere (B11 port of xref Unavailable): show prints a loaded
// pointer as <table>/<id> and one outside this checkout in F15's form,
// qualified when the project is known.
func TestOutWhere(t *testing.T) {
	cases := []struct {
		out  Out
		want string
	}{
		{Out{Table: "memories", ID: "mem_2"}, "memories/mem_2"},
		{Out{Table: "tasks", ID: "tsk_01", ProjectID: "prj_x", Unavailable: true}, "→ prj_x:tsk_01 (not available here)"},
		{Out{Table: "tasks", ID: "tsk_01", Unavailable: true}, "→ tsk_01 (not available here)"},
	}
	for _, c := range cases {
		if got := c.out.Where(); got != c.want {
			t.Errorf("Where(%+v) = %q, want %q", c.out, got, c.want)
		}
	}
}
