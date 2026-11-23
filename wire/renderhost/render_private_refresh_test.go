package renderhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestRefreshPrivateStaleFollowsPinnedRows pins devco:D-921: after a must
// rule is edited or archived and a critical hotfix is lowered, the private
// store file a tool imports holds the new text, without `lore render`.
// A file never rendered is not created, and one edited by hand (stamp
// still current) is left for lore doctor.
func TestRefreshPrivateStaleFollowsPinnedRows(t *testing.T) {
	ctx := context.Background()
	a, repo := privApp(t)
	projects, err := a.List(ctx, "inner", schema.TableProjects)
	if err != nil || len(projects) == 0 {
		t.Fatalf("inner project: %v", err)
	}
	add := func(table row.Table, fields map[string]string) engine.Ref {
		vals := map[string]value.Value{"source_kind": text(t, "manual"), "project_id": text(t, projects[0].ID.String())}
		for k, v := range fields {
			vals[k] = text(t, v)
		}
		w, cerr := a.Create(ctx, "inner", table, vals)
		if cerr != nil {
			t.Fatal(cerr)
		}
		return engine.Ref{Table: table, ID: w.Row.ID}
	}
	edit := add(schema.TableRules, map[string]string{"body": "Old rule text: tabs everywhere.", "severity": "must", "activation": schema.RuleActivationAlways})
	gone := add(schema.TableRules, map[string]string{"body": "Archived rule: call billing from tests.", "severity": "must", "activation": schema.RuleActivationAlways})
	lower := add(schema.TableHotfixes, map[string]string{"title": "queue drain", "body": "Critical hotfix: drain the queue first.", "severity": "critical"})

	home := t.TempDir()
	h := New(Options{App: a, Home: home})
	// never rendered: a write leaves the home without a private file
	if wrote, rerr := h.refreshPrivateStale(ctx, repo); rerr != nil || len(wrote) != 0 {
		t.Fatalf("refresh before any render wrote %v (%v), want nothing", wrote, rerr)
	}
	if _, serr := os.Stat(filepath.Join(home, renderDirName)); !os.IsNotExist(serr) {
		t.Fatalf("refresh before any render made %s", filepath.Join(home, renderDirName))
	}
	var out strings.Builder
	if err = h.renderPrivate(ctx, &out, repo, false, true); err != nil {
		t.Fatal(err)
	}
	file := storeFileOf(t, h, repo)

	update := func(ref engine.Ref, field string, v value.Value) {
		if _, uerr := a.Update(ctx, ref, map[string]value.Value{field: v}); uerr != nil {
			t.Fatal(uerr)
		}
	}
	archived, err := value.DateTimeOf("2026-01-02T03:04:05Z")
	if err != nil {
		t.Fatal(err)
	}
	update(edit, "body", text(t, "New rule text: gofmt everywhere."))
	update(gone, "archived_at", archived)
	update(lower, "severity", text(t, "low"))

	wrote, err := h.refreshPrivateStale(ctx, repo)
	if err != nil || len(wrote) != 1 || wrote[0] != file {
		t.Fatalf("refresh wrote %v (%v), want only %s", wrote, err, file)
	}
	body := readFile(t, file)
	for _, want := range []string{"New rule text: gofmt everywhere."} {
		if !strings.Contains(body, want) {
			t.Fatalf("refreshed private file lacks %q:\n%s", want, body)
		}
	}
	for _, stale := range []string{"Old rule text", "Archived rule"} {
		if strings.Contains(body, stale) {
			t.Fatalf("refreshed private file still holds %q:\n%s", stale, body)
		}
	}
	if pinned, _, _ := strings.Cut(body, "Other notes"); strings.Contains(pinned, "Critical hotfix: drain") {
		t.Fatalf("a hotfix lowered to low is still pinned in the private file:\n%s", body)
	}

	// edited by hand, rows unchanged: left as it is (doctor names it)
	hand := body + "\nmy own line\n"
	if err = os.WriteFile(file, []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	if wrote, err = h.refreshPrivateStale(ctx, repo); err != nil || len(wrote) != 0 || readFile(t, file) != hand {
		t.Fatalf("refresh rewrote a hand-edited, current file: wrote %v (%v)", wrote, err)
	}
}

// storeFileOf is the private file of the inner store of the repo's plan.
func storeFileOf(t *testing.T, h *Host, repo string) string {
	t.Helper()
	p, err := h.planPrivate(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range p.files {
		if filepath.Base(f.path) != privateIndexFile {
			return f.path
		}
	}
	t.Fatal("no private store file planned")
	return ""
}

// readFile is path's text.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSessionStartRefreshesStalePrivateRender pins the other half of
// devco:D-921: a rule edited where no write refreshed this checkout's
// private file (another checkout, an older lore) is brought up to date by
// the SessionStart hook before the session reads it.
func TestSessionStartRefreshesStalePrivateRender(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	ctx := context.Background()
	a, repo := privApp(t)
	projects, err := a.List(ctx, "inner", schema.TableProjects)
	if err != nil || len(projects) == 0 {
		t.Fatalf("inner project: %v", err)
	}
	w, err := a.Create(ctx, "inner", schema.TableRules, map[string]value.Value{"source_kind": text(t, "manual"),
		"project_id": text(t, projects[0].ID.String()), "body": text(t, "Old start text."), "severity": text(t, "must"),
		"activation": text(t, schema.RuleActivationAlways)})
	if err != nil {
		t.Fatal(err)
	}
	h := New(Options{App: a, Home: t.TempDir()})
	var out strings.Builder
	if err = h.renderPrivate(ctx, &out, repo, false, true); err != nil {
		t.Fatal(err)
	}
	file := storeFileOf(t, h, repo)
	if _, err = a.Update(ctx, engine.Ref{Table: schema.TableRules, ID: w.Row.ID}, map[string]value.Value{"body": text(t, "New start text.")}); err != nil {
		t.Fatal(err)
	}
	if body := readFile(t, file); !strings.Contains(body, "Old start text.") {
		t.Fatalf("the file changed before the session started:\n%s", body)
	}
	t.Chdir(repo)
	if _, err = h.contextSessionStart(ctx); err != nil {
		t.Fatal(err)
	}
	if body := readFile(t, file); !strings.Contains(body, "New start text.") || strings.Contains(body, "Old start text.") {
		t.Fatalf("SessionStart left the private render stale:\n%s", body)
	}
}
