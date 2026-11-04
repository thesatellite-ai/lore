package sqlite

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// withEnvelope rebuilds a stored row with _o and extra fields set.
func (f *fixture) withEnvelope(r store.Row, origin string, extra map[string]value.Value) store.Row {
	f.t.Helper()
	rr := must(row.Decode(r.Doc))
	rr.Origin = must(value.JSONOf([]byte(origin)))
	for k, v := range extra {
		rr.Fields[k] = v
	}
	return must(store.FromRow(rr))
}

// TestProvenanceViews: _o, moves, shared_as and repos.urls read as views.
func TestProvenanceViews(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	prj := newPrj()
	m := f.withEnvelope(f.memory(newMem(), prj, "x"),
		`{"cmd":"memory add","git":{"branch":"feat/vpn","state":"branch"},"import":{"ext_id":"AGY-1","source":"jira"},"moves":[{"at":"2026-11-01T00:00:00Z","by":"act_x","moved_from":"repo","moved_to":"agency/acme"}],"repo":"rep_1"}`,
		map[string]value.Value{"shared_as": must(value.JSONOf([]byte(`["mem_a","mem_b"]`)))})
	plain := f.memory(newMem(), prj, "old")
	repo := f.live(schema.TableRepos, must(ids.New[ids.Repo]()).Any(), map[string]value.Value{
		"project_id": must(value.RefOf(prj)),
		"mount_name": text(t, "r"),
		"urls":       must(value.JSONOf([]byte(`[{"url":"https://github.com/a/r","kind":"primary","how":"init","from":"2026-01-01"}]`))),
	})
	f.apply(c, store.SourceRepo, "1", m, plain, repo)
	for q, want := range map[string]int{
		`SELECT count(*) FROM row_provenance WHERE has_origin = 0`:                                     2,
		`SELECT count(*) FROM row_provenance WHERE git_branch = 'feat/vpn' AND repo_id = 'rep_1'`:      1,
		`SELECT count(*) FROM row_provenance WHERE import_source = 'jira' AND import_ext_id = 'AGY-1'`: 1,
		`SELECT count(*) FROM row_moves WHERE moved_to = 'agency/acme' AND by_actor = 'act_x'`:         1,
		`SELECT count(*) FROM row_lineage WHERE kind = 'shared_as'`:                                    2,
		`SELECT count(*) FROM repo_urls WHERE kind = 'primary' AND to_date IS NULL`:                    1,
	} {
		if got := count(t, c, q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}
}

// TestProvenanceViewsAttached: the views resolve `_rows` inside their own
// file when the cache is ATTACHed by a lens.
func TestProvenanceViewsAttached(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	m := f.withEnvelope(f.memory(newMem(), newPrj(), "x"), `{"cmd":"memory add","moves":[{"at":"a","moved_from":"x","moved_to":"y"}]}`, nil)
	f.apply(c, store.SourceRepo, "1", m)
	l, err := OpenLens(f.ctx, []LensSource{{Name: "repo", Path: f.dir + "/c.db"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	res, err := l.Query(f.ctx, `SELECT moved_to FROM "`+attachedName(0)+`".row_moves`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].String() != "y" {
		t.Fatalf("rows %+v", res.Rows)
	}
}
