package wire

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// secondStore is the id of the second folder store of the lens tests.
const secondStore = "sto_01a1a5aca7f57659be7320c5f4b70003"

// lensRepo is a repo with two internal folder stores (agency, golang), a
// project row in each of the three stores named after its store, and the
// client-repo hook answering with hook.
func lensRepo(t *testing.T, hook func(context.Context) (bool, error)) *App {
	t.Helper()
	repo := gitRepo(t)
	writeStoresYAML(t, repo, "stores:\n"+
		"  - name: agency\n    url: "+folderStoreAt(t, folderStore, rowfs.VisibilityInternal)+"\n"+
		"  - name: golang\n    url: "+folderStoreAt(t, secondStore, rowfs.VisibilityInternal)+"\n")
	a := openWith(t, repo, Options{RepoIsClient: hook})
	for _, s := range []store.Source{store.SourceRepo, "agency", "golang"} {
		name, err := value.TextOf("prj-" + string(s))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Create(background(), s, schema.TableProjects, map[string]value.Value{"name": name}); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

// cells renders a result as "a|b" lines.
func cells(r opssql.Result) []string {
	out := make([]string, 0, len(r.Rows))
	for _, row := range r.Rows {
		parts := make([]string, len(row))
		for i, c := range row {
			parts[i] = c.String()
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return out
}

func sqlRows(t *testing.T, a *App, q string) []string {
	t.Helper()
	res, err := a.SQL(background(), q, false)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return cells(res)
}

// TestLensOverStores: in an internal repo the lens spans the repo and both
// stores, `source` is each store's name, and every table of the registry
// has its <table>_all view.
func TestLensOverStores(t *testing.T) {
	a := lensRepo(t, func(context.Context) (bool, error) { return false, nil })
	got := sqlRows(t, a, `SELECT source, name FROM projects_all ORDER BY source`)
	if want := []string{"agency|prj-agency", "golang|prj-golang", "repo|prj-repo"}; !slices.Equal(got, want) {
		t.Fatalf("projects_all = %v, want %v", got, want)
	}
	// The plain name is the nearest store's copy (the repo, attach order).
	if got := sqlRows(t, a, `SELECT name FROM projects`); !slices.Equal(got, []string{"prj-repo"}) {
		t.Fatalf("projects = %v", got)
	}
	l, err := a.Lens(background(), sqlite.LensOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	views := l.Views()
	for _, d := range eng.Registry().Tables() {
		if !slices.Contains(views, d.Name.String()+"_all") {
			t.Errorf("no view %s_all in %v", d.Name, views)
		}
	}
}

// TestLensClientRepoSeesOnlyRepo (MQ6, CFG6): a client repo's lens holds
// only the repo cache, never a store's rows, for every way the client
// answer can be client: the hook says so, the hook fails, no hook (fail
// closed). Only a positive "not a client" answer opens the stores.
func TestLensClientRepoSeesOnlyRepo(t *testing.T) {
	cases := []struct {
		name   string
		hook   func(context.Context) (bool, error)
		stores bool
	}{
		{"client", func(context.Context) (bool, error) { return true, nil }, false},
		{"hook error", func(context.Context) (bool, error) { return false, errors.New("org unreachable") }, false},
		{"no hook", nil, false},
		{"internal", func(context.Context) (bool, error) { return false, nil }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := lensRepo(t, c.hook)
			got := sqlRows(t, a, `SELECT source, name FROM projects_all ORDER BY source`)
			want := []string{"repo|prj-repo"}
			if c.stores {
				want = []string{"agency|prj-agency", "golang|prj-golang", "repo|prj-repo"}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("projects_all = %v, want %v", got, want)
			}
			if !c.stores {
				// No attached file but the repo's: s_1 does not exist even
				// on an unmasked lens.
				if _, err := a.SQL(background(), `SELECT name FROM s_1.projects`, true); err == nil {
					t.Fatal("a client lens attached a second file")
				}
			}
		})
	}
}

// kindWorld runs spec 15 step 5 in a: `lore resource kind add server`,
// two field adds (ip, and the sensitive admin_ip), two resources.
func kindWorld(t *testing.T, a *App) {
	t.Helper()
	steps := []struct{ op, in string }{
		{opssettings.OpKindAdd, `{"kind":"server"}`},
		{opssettings.OpFieldAdd, `{"kind":"server","field":"ip","type":"ip"}`},
		{opssettings.OpFieldAdd, `{"kind":"server","field":"admin_ip","type":"ip","sensitive":true}`},
		{opssettings.OpResAdd, `{"kind":"server","name":"hub_prod","set":["ip=203.0.113.10","admin_ip=198.51.100.7"]}`},
		{opssettings.OpResAdd, `{"kind":"server","name":"hub_stag","set":["ip=203.0.113.11"]}`},
	}
	for _, s := range steps {
		if _, err := run(t, a, s.op, s.in, true); err != nil {
			t.Fatalf("%s %s: %v", s.op, s.in, err)
		}
	}
}

// TestSQLSpec15Check: spec 15 step 5's check query answers through the
// registered sql.run op, the sensitive field is masked, and
// --show-sensitive asks first (E_NEEDS_CONFIRM) and shows it with yes.
func TestSQLSpec15Check(t *testing.T) {
	a := lensRepo(t, func(context.Context) (bool, error) { return false, nil })
	kindWorld(t, a)
	out, err := run(t, a, opssql.OpRun, `{"query":"SELECT name, ip FROM res_server ORDER BY name"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := runRows(t, out); !slices.Equal(got, []string{"hub_prod|203.0.113.10", "hub_stag|203.0.113.11"}) {
		t.Fatalf("spec 15 check = %v (%s)", got, out)
	}
	masked := `{"query":"SELECT name, admin_ip FROM res_server_all ORDER BY name"}`
	out, err = run(t, a, opssql.OpRun, masked, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := runRows(t, out); strings.Contains(out, "198.51.100.7") || !slices.Equal(got, []string{"hub_prod|" + sqlite.MaskedValue, "hub_stag|<null>"}) {
		t.Fatalf("masked = %v (%s)", got, out)
	}
	show := `{"query":"SELECT name, admin_ip FROM res_server_all ORDER BY name","show_sensitive":true}`
	if _, err = run(t, a, opssql.OpRun, show, false); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("--show-sensitive without yes: %v, want E_NEEDS_CONFIRM", err)
	}
	out, err = run(t, a, opssql.OpRun, show, true)
	if err != nil || !slices.Equal(runRows(t, out), []string{"hub_prod|198.51.100.7", "hub_stag|<null>"}) {
		t.Fatalf("--show-sensitive --yes = %s, %v", out, err)
	}
	// resources.fields is masked too, and a direct read of a file refused.
	out, err = run(t, a, opssql.OpRun, `{"query":"SELECT fields FROM resources_all"}`, false)
	if err != nil || strings.Contains(out, "198.51.100.7") {
		t.Fatalf("resources_all.fields = %s, %v", out, err)
	}
	if _, err = run(t, a, opssql.OpRun, `{"query":"SELECT admin_ip FROM s_0.res_server"}`, false); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("direct file read on a masked lens: %v", err)
	}
}

// runRows decodes a sql.run JSON output into "a|b" lines (<null> for a
// NULL cell), checking count matches.
func runRows(t *testing.T, out string) []string {
	t.Helper()
	var got opssql.RunOut
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, len(got.Rows))
	for _, r := range got.Rows {
		parts := make([]string, len(r.Cells))
		for i, c := range r.Cells {
			parts[i] = c.Value
			if c.Kind == string(value.KindNull) {
				parts[i] = "<null>"
			}
		}
		lines = append(lines, strings.Join(parts, "|"))
	}
	if got.Count != len(lines) {
		t.Fatalf("count %d, rows %d", got.Count, len(lines))
	}
	return lines
}

// TestSQLRefusesWrites: every write shape is refused through the op and
// nothing changes.
func TestSQLRefusesWrites(t *testing.T) {
	a := lensRepo(t, func(context.Context) (bool, error) { return false, nil })
	for _, q := range []string{
		`INSERT INTO projects (id) VALUES ('x')`,
		`UPDATE projects SET name = 'x'`,
		`DELETE FROM projects`,
		`ATTACH DATABASE ':memory:' AS evil`,
		`PRAGMA query_only = OFF`,
		`SELECT 1; DELETE FROM projects`,
		`DROP VIEW projects_all`,
		`WITH x AS (SELECT 1) DELETE FROM projects`,
		``,
		`   `,
	} {
		for _, show := range []bool{false, true} {
			in, err := json.Marshal(opssql.RunIn{Query: q, ShowSensitive: show})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := run(t, a, opssql.OpRun, string(in), true); !errs.Has(err, errs.ReadOnly) && !errs.Has(err, errs.InvalidInput) {
				t.Errorf("%q (show=%v): %v, want a refusal", q, show, err)
			}
		}
	}
	if got := sqlRows(t, a, `SELECT COUNT(*) FROM projects_all`); !slices.Equal(got, []string{"3"}) {
		t.Fatalf("rows changed: %v", got)
	}
}

// TestLensCombinedCache: past MaxStoreCaches the lens is the repo file
// plus the one combined file, and `source` is still each store's name.
func TestLensCombinedCache(t *testing.T) {
	repo := gitRepo(t)
	var yaml strings.Builder
	yaml.WriteString("stores:\n")
	n := MaxStoreCaches + 1
	names := make([]string, 0, n)
	for i := range n {
		id := "sto_01a1a5aca7f57659be7320c5f4b7" + strconv.FormatInt(int64(0x1000+i), 16)
		name := "s" + strconv.Itoa(i)
		names = append(names, name)
		yaml.WriteString("  - name: " + name + "\n    url: " + folderStoreAt(t, id, rowfs.VisibilityInternal) + "\n")
	}
	writeStoresYAML(t, repo, yaml.String())
	a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if _, err := a.Engine(background()); err != nil {
		t.Fatal(err)
	}
	if !a.Combined() {
		t.Fatal("not a combined cache")
	}
	for _, s := range append([]string{string(store.SourceRepo)}, names...) {
		name, err := value.TextOf("prj-" + s)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Create(background(), store.Source(s), schema.TableProjects, map[string]value.Value{"name": name}); err != nil {
			t.Fatal(err)
		}
	}
	got := sqlRows(t, a, `SELECT source || '=' || name FROM projects_all ORDER BY source`)
	var want []string
	for _, s := range append([]string{string(store.SourceRepo)}, names...) {
		want = append(want, s+"=prj-"+s)
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("projects_all = %v, want %v", got, want)
	}
}
