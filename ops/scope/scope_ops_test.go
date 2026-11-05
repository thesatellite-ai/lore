package scope_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/ops/scope"
)

// scopeHarness is a repo with a project, a repo identity and the folders
// dirs on disk.
func scopeHarness(t *testing.T, dirs ...string) *opstest.H {
	t.Helper()
	h := opstest.New(t)
	meta := filepath.Join(h.Repo, rowfs.LoreDir, rowfs.DataDir)
	if err := os.MkdirAll(meta, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(meta, rowfs.MetaFileName), []byte(`{"_v":1,"project_id":"`+h.Project+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.App.EnsureRepoIdentity(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(h.Repo, filepath.FromSlash(d)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// decode reads a result's JSON into v.
func decode(t *testing.T, h *opstest.H, name, in string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(h.JSON(h.Must(name, in))), v); err != nil {
		t.Fatal(err)
	}
}

// TestScopeOps (CONTEXT_BUDGET 6.2.2-6.2.3, OV25, later:473/472) runs
// `lore scope show|list|move` over rows that got their folder through
// --scope on every pinning and note noun (rule, hotfix, decision, memory):
// show prints project, repo and the covering folders outermost first plus
// the matching glob rules, counting only pinned rows; list counts pinned
// rows and notes per folder and flags no file before a render; move
// relabels the folder and the folders under it with ids unchanged, and
// refuses a move into itself, an unknown folder, a taken target and a
// target folder that does not exist; after a move the old path can be
// scoped again and gets a new folder row.
func TestScopeOps(t *testing.T) {
	h := scopeHarness(t, "src/booking/payments", "src/billing", "web")
	add := func(name, in string) string {
		t.Helper()
		return h.ID(h.Must(name, in))
	}
	rOuter := add("rule.add", `{"body":"outer must","severity":"must","scope":"dir:src/booking"}`)
	add("rule.add", `{"body":"outer should","severity":"should","scope":"dir:src/booking"}`) // not pinned: not counted
	add("rule.add", `{"body":"inner must","severity":"must","scope":"dir:src/booking/payments"}`)
	add("hotfix.add", `{"title":"inner trap","body":"b","severity":"critical","scope":"dir:src/booking/payments"}`)
	add("hotfix.add", `{"title":"inner low","body":"b","severity":"low","scope":"dir:src/booking/payments"}`) // not pinned
	add("decision.add", `{"title":"d","body":"b","scope":"dir:src/booking"}`)
	add("memory.add", `{"body":"m","scope":"dir:src/booking"}`)
	add("rule.add", `{"body":"project must","severity":"must","scope":"project"}`)
	add("rule.add", `{"body":"repo must","severity":"must","scope":"repo"}`)
	add("rule.add", `{"body":"web must","severity":"must","scope":"dir:web"}`)
	add("rule.add", `{"body":"go files","severity":"should","activation":"glob","globs":"[\"**/*.go\"]"}`)

	var list scope.ListOut
	decode(t, h, "scope.list", `{}`, &list)
	type cnt struct{ rules, hotfixes, notes int }
	want := map[string]cnt{"src/booking": {1, 0, 2}, "src/booking/payments": {1, 1, 0}, "web": {1, 0, 0}}
	ids := map[string]string{}
	if len(list.Data) != len(want) {
		t.Fatalf("list %+v", list.Data)
	}
	for _, r := range list.Data {
		ids[r.Path] = r.ID
		if w, ok := want[r.Path]; !ok || (cnt{r.Rules, r.Hotfixes, r.Notes}) != w || r.File != "(none)" || r.Missing {
			t.Fatalf("list row %+v, want %+v", r, w)
		}
	}
	if list.Data[0].Path != "src/booking" || list.Data[2].Path != "web" {
		t.Fatalf("list not sorted by path: %+v", list.Data)
	}

	var show scope.ShowOut
	decode(t, h, "scope.show", `{"path":"src/booking/payments/pay.go"}`, &show)
	gotLevels := []string{}
	for _, l := range show.Levels {
		gotLevels = append(gotLevels, l.Scope+":"+l.Name)
	}
	wantLevels := []string{"project:this project", "repo:this repo", "dir:src/booking", "dir:src/booking/payments"}
	if len(gotLevels) != len(wantLevels) {
		t.Fatalf("levels %v, want %v", gotLevels, wantLevels)
	}
	for i := range wantLevels {
		if gotLevels[i] != wantLevels[i] {
			t.Fatalf("levels %v, want %v", gotLevels, wantLevels)
		}
	}
	if show.Levels[0].Rules != 1 || show.Levels[1].Rules != 1 || show.Levels[2].Rules != 1 || show.Levels[3].Rules != 1 || show.Levels[3].Hotfixes != 1 {
		t.Fatalf("level counts %+v", show.Levels)
	}
	if len(show.Globs) != 1 || show.Globs[0].Name != "**/*.go" {
		t.Fatalf("globs %+v", show.Globs)
	}
	show = scope.ShowOut{} // globs is omitempty: decode into a fresh value
	decode(t, h, "scope.show", `{"path":"web/index.html"}`, &show)
	if len(show.Levels) != 3 || show.Levels[2].Name != "web" || len(show.Globs) != 0 {
		t.Fatalf("web chain %+v", show)
	}

	// Refusals first: nothing is written by any of them.
	for _, c := range []struct {
		in   string
		code errs.Code
	}{
		{`{"old":"src/booking","new":"src/booking/x"}`, errs.InvalidInput}, // into itself
		{`{"old":"nowhere","new":"src/billing"}`, errs.NotFound},
		{`{"old":"src/booking","new":"web"}`, errs.InvalidInput},          // taken
		{`{"old":"src/booking","new":"src/gone"}`, errs.InvalidInput},     // no such folder
		{`{"old":"src/booking/payments","new":"web"}`, errs.InvalidInput}, // taken (nested)
	} {
		h.Fails("scope.move", c.in, c.code)
	}

	var moved scope.MoveOut
	decode(t, h, "scope.move", `{"old":"src/booking","new":"src/billing"}`, &moved)
	if len(moved.Data) != 2 {
		t.Fatalf("moved %+v", moved.Data)
	}
	list = scope.ListOut{}
	decode(t, h, "scope.list", `{}`, &list)
	for _, r := range list.Data {
		switch r.Path {
		case "src/billing":
			if r.ID != ids["src/booking"] || r.Rules != 1 || r.Notes != 2 {
				t.Fatalf("moved outer %+v", r)
			}
		case "src/billing/payments":
			if r.ID != ids["src/booking/payments"] || r.Hotfixes != 1 {
				t.Fatalf("moved inner %+v", r)
			}
		case "web":
		default:
			t.Fatalf("stale path after move: %+v", r)
		}
	}
	if got := contenttest.Str(t, h.JSON(h.Must("rule.show", `{"id":"`+rOuter+`"}`)), "data", "scope_id"); got != ids["src/booking"] {
		t.Fatalf("rule scope after move %q, want the same scp_ id", got)
	}

	// The old path again after the move (later:472, engine 0f504de): a new
	// scope row with a fresh id, not the moved one; a second write to the
	// same path re-uses that new row.
	again := add("rule.add", `{"body":"back at booking","severity":"must","scope":"dir:src/booking"}`)
	scp := contenttest.Str(t, h.JSON(h.Must("rule.show", `{"id":"`+again+`"}`)), "data", "scope_id")
	if scp == "" || scp == ids["src/booking"] {
		t.Fatalf("re-created scope %q (moved row %q)", scp, ids["src/booking"])
	}
	twice := add("rule.add", `{"body":"booking again","severity":"must","scope":"dir:src/booking"}`)
	if got := contenttest.Str(t, h.JSON(h.Must("rule.show", `{"id":"`+twice+`"}`)), "data", "scope_id"); got != scp {
		t.Fatalf("second write scope %q, want %q", got, scp)
	}
	list = scope.ListOut{}
	decode(t, h, "scope.list", `{}`, &list)
	if len(list.Data) != 4 {
		t.Fatalf("after re-create: %+v", list.Data)
	}
}
