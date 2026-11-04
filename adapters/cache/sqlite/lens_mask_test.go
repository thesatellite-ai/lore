package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// maskProbeIP is the sensitive value (a documentation-range address) the masking tests must never see.
const maskProbeIP = "198.51.100.7"

// sensitiveWorld writes repo.db (a memory, plus a "vault" kind whose
// "host" field is sensitive and one resource holding maskProbeIP) and
// golang.db (kindWorld: a non-sensitive server kind) and returns both
// sources, the vault kind id and its host field id.
func sensitiveWorld(t *testing.T) (f *fixture, srcs []LensSource, kind ids.ID[ids.Kind], host ids.ID[ids.KindField]) {
	t.Helper()
	f = newFixture(t)
	repo := f.open("repo.db")
	kind, host = must(ids.New[ids.Kind]()), must(ids.New[ids.KindField]())
	label := must(ids.New[ids.KindField]())
	hostRow := f.live(schema.TableKindFields, host.Any(), map[string]value.Value{
		fieldKindID: text(t, kind.String()), fieldName: text(t, "host"), "type": text(t, "ip"), fieldSensitive: value.BoolOf(true),
	})
	f.apply(repo, store.SourceRepo, "1",
		f.memory(newMem(), newPrj(), "repo memory"),
		f.kindRow(kind, "vault"), hostRow,
		f.fieldRow(label, kind, "label", "text", ids.ID[ids.Kind]{}),
		f.resource(must(ids.New[ids.Resource]()), kind, "vault_prod", map[ids.ID[ids.KindField]][2]string{
			host: {"host", `"` + maskProbeIP + `"`}, label: {"label", `"primary"`},
		}),
		f.resource(must(ids.New[ids.Resource]()), kind, "vault_empty", map[ids.ID[ids.KindField]][2]string{
			label: {"label", `"no host"`},
		}),
	)
	golang := f.open("golang.db")
	f.kindWorld(golang)
	for _, c := range []*Cache{repo, golang} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	srcs = []LensSource{
		{Name: string(store.SourceRepo), Path: filepath.Join(f.dir, "repo.db")},
		{Name: "golang", Path: filepath.Join(f.dir, "golang.db")},
	}
	return f, srcs, kind, host
}

func openMasked(t *testing.T, srcs []LensSource, masked bool) *Lens {
	t.Helper()
	l, err := OpenLensWith(context.Background(), srcs, LensOptions{MaskSensitive: masked})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// dump renders a whole result as text, to search it for a leaked value.
func dump(r Result) string {
	var b strings.Builder
	b.WriteString(strings.Join(r.Columns, "|"))
	for _, row := range r.Rows {
		for _, c := range row {
			b.WriteString("|" + c.String())
		}
	}
	return b.String()
}

// TestLensMaskSensitive: on a masked lens every way to the sensitive
// value, through the views, shows ••• (NULL stays NULL), the non-sensitive
// columns and kinds are untouched, and an unmasked lens shows the value.
func TestLensMaskSensitive(t *testing.T) {
	_, srcs, kind, _ := sensitiveWorld(t)
	ctx := context.Background()
	masked := openMasked(t, srcs, true)
	queries := map[string]string{
		"plain name":       `SELECT name, host, label FROM res_vault ORDER BY name`,
		"all view":         `SELECT source, name, host, label FROM res_vault_all ORDER BY name`,
		"stable view":      `SELECT name, host FROM "` + schema.KindTablePrefix + kind.String() + `_all" ORDER BY name`,
		"alias":            `SELECT host AS x, host || '' AS y, upper(host) AS z FROM res_vault_all`,
		"resources json":   `SELECT fields FROM resources_all`,
		"resources plain":  `SELECT json_extract(fields, '$') FROM resources`,
		"star":             `SELECT * FROM res_vault`,
		"subquery":         `SELECT (SELECT group_concat(host) FROM res_vault) AS h`,
		"temp qualified":   `SELECT host FROM temp.res_vault`,
		"case insensitive": `SELECT HOST FROM RES_VAULT`,
	}
	for name, q := range queries {
		res, err := masked.Query(ctx, q)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(dump(res), maskProbeIP) {
			t.Errorf("%s leaks the sensitive value: %s", name, dump(res))
		}
	}
	res := must(masked.Query(ctx, `SELECT name, host, label FROM res_vault_all ORDER BY name`))
	want := [][]string{{"vault_empty", "null", "no host"}, {"vault_prod", MaskedValue, "primary"}}
	for i, w := range want {
		got := []string{res.Rows[i][0].String(), string(res.Rows[i][1].Kind()), res.Rows[i][2].String()}
		if res.Rows[i][1].Kind() != value.KindNull {
			got[1] = res.Rows[i][1].String()
		}
		if !slices.Equal(got, w) {
			t.Fatalf("row %d = %v, want %v", i, got, w)
		}
	}
	// The non-sensitive kind of another store is unmasked.
	res = must(masked.Query(ctx, `SELECT source, ip FROM res_server_all`))
	if len(res.Rows) != 1 || res.Rows[0][0].String() != "golang" || res.Rows[0][1].String() != "203.0.113.10" {
		t.Fatalf("res_server_all = %v", res.Rows)
	}
	unmasked := openMasked(t, srcs, false)
	res = must(unmasked.Query(ctx, `SELECT host FROM res_vault_all WHERE name = 'vault_prod'`))
	if len(res.Rows) != 1 || res.Rows[0][0].String() != maskProbeIP {
		t.Fatalf("unmasked lens: %v", res.Rows)
	}
}

// TestLensMaskResourcesJSON: the sensitive entry of resources.fields has
// its value masked and keeps its name; the other entries are unchanged.
func TestLensMaskResourcesJSON(t *testing.T) {
	_, srcs, _, host := sensitiveWorld(t)
	l := openMasked(t, srcs, true)
	res := must(l.Query(context.Background(),
		`SELECT json_extract(fields, '$."`+host.String()+`".v'), json_extract(fields, '$."`+host.String()+`".name') FROM resources_all WHERE name = 'vault_prod'`))
	if len(res.Rows) != 1 || res.Rows[0][0].String() != MaskedValue || res.Rows[0][1].String() != "host" {
		t.Fatalf("masked fields = %v", res.Rows)
	}
	res = must(l.Query(context.Background(), `SELECT fields FROM resources_all WHERE name = 'vault_prod'`))
	if !strings.Contains(res.Rows[0][0].String(), `"primary"`) {
		t.Fatalf("non-sensitive entry lost: %s", res.Rows[0][0])
	}
}

// TestLensMaskRefusesBypass: a masked lens refuses every name that reads
// past the views (an attached file's schema, quoted any way, and the raw
// page reader); an unmasked lens allows them.
func TestLensMaskRefusesBypass(t *testing.T) {
	_, srcs, _, _ := sensitiveWorld(t)
	ctx := context.Background()
	masked := openMasked(t, srcs, true)
	bypass := []string{
		`SELECT host FROM s_0.res_vault`,
		`SELECT host FROM S_0.res_vault`,
		`SELECT host FROM "s_0".res_vault`,
		`SELECT host FROM [s_0].res_vault`,
		"SELECT host FROM `s_1`.res_server",
		`SELECT data FROM s_0._rows`,
		`SELECT * FROM sqlite_dbpage('s_0')`,
		`SELECT data FROM sqlite_dbpage(char(115,95,48))`,
		`SELECT * FROM SQLITE_DBPAGE`,
		`SELECT name FROM pragma_table_info('res_vault', 's_0')`,
		`WITH x AS (SELECT * FROM s_0.res_vault) SELECT host FROM x`,
		`SELECT 1 /* c */ , (SELECT host FROM s_0 . res_vault)`,
	}
	for _, q := range bypass {
		_, err := masked.Query(ctx, q)
		if !errs.Has(err, errs.SQLRefused) || err == nil || !strings.Contains(err.Error(), "masked views") {
			t.Errorf("bypass not refused: %s → %v", q, err)
		}
	}
	// A name only inside a comment, or as part of a longer word, is fine.
	for _, q := range []string{
		`SELECT 1 -- s_0.res_vault`,
		`SELECT 1 /* s_0 */`,
		`SELECT 's_00' AS s_01`,
	} {
		if _, err := masked.Query(ctx, q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
	unmasked := openMasked(t, srcs, false)
	res := must(unmasked.Query(ctx, `SELECT host FROM s_0.res_vault WHERE name = 'vault_prod'`))
	if res.Rows[0][0].String() != maskProbeIP {
		t.Fatalf("unmasked direct read: %v", res.Rows)
	}
}

// TestLensMaskedStillReadOnly: masking does not loosen the write refusal.
func TestLensMaskedStillReadOnly(t *testing.T) {
	_, srcs, _, _ := sensitiveWorld(t)
	l := openMasked(t, srcs, true)
	for _, q := range []string{
		`INSERT INTO res_vault (id) VALUES ('x')`,
		`UPDATE res_vault SET host = 'x'`,
		`DELETE FROM resources`,
		`DROP VIEW res_vault`,
		`ATTACH DATABASE ':memory:' AS evil`,
		`PRAGMA query_only = OFF`,
		`SELECT 1; SELECT 2`,
		`WITH x AS (SELECT 1) DELETE FROM res_vault`,
	} {
		// A WITH-hidden write passes the keyword check; SQLite then refuses
		// it (a view cannot be modified, or query_only): either code is a
		// refusal, and the row count below proves nothing changed.
		if _, err := l.Query(context.Background(), q); !errs.Has(err, errs.SQLRefused) && !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: %v (want a refusal)", q, err)
		}
	}
	if res := must(l.Query(context.Background(), `SELECT COUNT(*) FROM resources_all`)); res.Rows[0][0].String() != "3" {
		t.Fatalf("rows changed: %v", res.Rows)
	}
}

// TestLensMixedCombined: a lens over the repo file plus a combined file
// takes `source` from the plain file's name and from each combined row's
// own _source; ResolveID finds an id in either, and an id held by two
// sources is E_AMBIGUOUS_ID.
func TestLensMixedCombined(t *testing.T) {
	f := newFixture(t)
	repo := f.open("repo.db")
	shared := newMem()
	f.apply(repo, store.SourceRepo, "1", f.memory(newMem(), newPrj(), "repo row"), f.memory(shared, newPrj(), "repo copy"))
	comb := f.open("combined.db")
	only := newMem()
	f.apply(comb, "agency", "1", f.memory(only, newPrj(), "agency row"))
	f.apply(comb, "golang", "1", f.memory(shared, newPrj(), "golang copy"))
	for _, c := range []*Cache{repo, comb} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	srcs := []LensSource{
		{Name: string(store.SourceRepo), Path: filepath.Join(f.dir, "repo.db")},
		{Name: "combined", Path: filepath.Join(f.dir, "combined.db"), Combined: true},
	}
	for _, masked := range []bool{false, true} {
		l := openMasked(t, srcs, masked)
		res := must(l.Query(context.Background(), `SELECT source, COUNT(*) FROM memories_all GROUP BY source ORDER BY source`))
		var got []string
		for _, r := range res.Rows {
			got = append(got, r[0].String()+"="+r[1].String())
		}
		if want := []string{"agency=1", "golang=1", "repo=2"}; !slices.Equal(got, want) {
			t.Fatalf("masked=%v sources = %v, want %v", masked, got, want)
		}
		hit := must(l.ResolveID(context.Background(), only))
		if hit.Source != "agency" {
			t.Fatalf("ResolveID = %+v", hit)
		}
		if _, err := l.ResolveID(context.Background(), shared); !errs.Has(err, errs.AmbiguousID) {
			t.Fatalf("shared id: %v", err)
		}
	}
}

// TestNameTokens is the lexer matrix the bypass guard stands on.
func TestNameTokens(t *testing.T) {
	cases := map[string][]string{
		`SELECT a FROM b`:               {"SELECT", "a", "FROM", "b"},
		`SELECT "q""x" FROM [br]`:       {"SELECT", `q"x`, "FROM", "br"},
		"SELECT `bt` , 'it''s'":         {"SELECT", "bt", "it's"},
		"SELECT 1 -- s_0\n, 2":          {"SELECT", "1", "2"},
		`SELECT /* s_0 */ x`:            {"SELECT", "x"},
		`SELECT héllo_1$ FROM t`:        {"SELECT", "héllo_1$", "FROM", "t"},
		`SELECT 'unterminated`:          {"SELECT", "unterminated"},
		``:                              nil,
		`SELECT x'00' FROM s_0.t`:       {"SELECT", "x", "00", "FROM", "s_0", "t"},
		`SELECT a.b,c(d)`:               {"SELECT", "a", "b", "c", "d"},
		`SELECT [unterminated`:          {"SELECT", "unterminated"},
		"SELECT \"emoji 😀\" FROM \"t\"": {"SELECT", "emoji 😀", "FROM", "t"},
	}
	for q, want := range cases {
		if got := nameTokens(q); !slices.Equal(got, want) {
			t.Errorf("nameTokens(%q) = %q, want %q", q, got, want)
		}
	}
}

// TestLensMaskFieldsShapes: the resources.fields mask over every shape a
// writer could leave — the spec's {name, v}, a flat sensitive value, a
// column that is not an object — never shows the sensitive value, and
// passes every other entry through with its JSON type.
func TestLensMaskFieldsShapes(t *testing.T) {
	f, srcs, kind, host := sensitiveWorld(t)
	other := must(ids.New[ids.KindField]()).String()
	cases := map[string]struct {
		fields string
		want   string // the masked column, or "" to only check for the leak
	}{
		"flat sensitive": {`{"` + host.String() + `":"` + maskProbeIP + `","` + other + `":7}`, `{"` + host.String() + `":"` + MaskedValue + `","` + other + `":7}`},
		"typed others": {`{"` + host.String() + `":{"name":"host","v":"` + maskProbeIP + `"},"a":true,"b":false,"c":null,"d":[1,"x"],"e":{"name":"e","v":2.5}}`,
			// the cache stores canonical JSON (keys sorted), so "a"… come first
			`{"a":true,"b":false,"c":null,"d":[1,"x"],"e":{"name":"e","v":2.5},"` + host.String() + `":{"name":"host","v":"` + MaskedValue + `"}}`},
		"array column":  {`["` + maskProbeIP + `"]`, MaskedValue},
		"string column": {`"` + maskProbeIP + `"`, MaskedValue},
		"empty object":  {`{}`, `{}`},
	}
	c := f.open("shapes.db")
	i := 0
	for name, tc := range cases {
		i++
		raw := must(value.JSONOf([]byte(tc.fields)))
		f.apply(c, store.SourceRepo, "s"+string(rune('a'+i)), f.live(schema.TableResources, must(ids.New[ids.Resource]()).Any(), map[string]value.Value{
			"kind": text(t, kind.String()), "name": text(t, name), "fields": raw,
		}))
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	l := openMasked(t, []LensSource{srcs[0], srcs[1], {Name: "shapes", Path: filepath.Join(f.dir, "shapes.db")}}, true)
	for name, tc := range cases {
		res := must(l.Query(context.Background(), `SELECT fields FROM resources_all WHERE source = 'shapes' AND name = '`+name+`'`))
		if len(res.Rows) != 1 {
			t.Fatalf("%s: %v", name, res.Rows)
		}
		got := res.Rows[0][0].String()
		if strings.Contains(got, maskProbeIP) {
			t.Errorf("%s leaks: %s", name, got)
		}
		if tc.want != "" && got != tc.want {
			t.Errorf("%s = %s, want %s", name, got, tc.want)
		}
	}
}

// TestLensQueryErrorNamesTheCause: a bad query is E_INVALID_INPUT whose
// message (all a surface prints) carries SQLite's reason.
func TestLensQueryErrorNamesTheCause(t *testing.T) {
	_, srcs, _, _ := sensitiveWorld(t)
	for _, masked := range []bool{false, true} {
		l := openMasked(t, srcs, masked)
		_, err := l.Query(context.Background(), `SELECT nope FROM memories_all`)
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.InvalidInput || !strings.Contains(e.Message, "no such column: nope") {
			t.Fatalf("masked=%v: %v", masked, err)
		}
	}
}
