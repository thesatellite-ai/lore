package wire

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/model/errs"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestSQLStoreReadsThatStoreAlone pins devco D-799 (owner P-16): in a
// repo set up with lore that attaches a store through .lore/stores.yaml
// (neither an org nor `me`), `lore sql run --store agency` read the repo:
// the plain name res_person gave the repo's person, res_person_all and
// projects_all held the repo's rows beside the store's, and `--store nope`
// ran on the repo instead of being refused. Now --store names the one
// store the query reads, `--store repo` the repo's own rows (a repo
// resource of a kind defined in the store keeps its view and its masked
// field), and an unknown name is E_NOT_FOUND.
func TestSQLStoreReadsThatStoreAlone(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	a := lensRepo(t, func(context.Context) (bool, error) { return false, nil })
	for _, s := range []struct{ op, in string }{
		{opssettings.OpKindAdd, `{"kind":"person","to":"agency"}`},
		{opssettings.OpFieldAdd, `{"kind":"person","field":"phone","type":"text","sensitive":true,"to":"agency"}`},
		{opssettings.OpResAdd, `{"kind":"person","name":"bob_store","set":["phone=555-0101"],"to":"agency"}`},
		{opssettings.OpResAdd, `{"kind":"person","name":"carol_repo","set":["phone=555-0202"],"to":"repo"}`},
		// a kind only the store has rows of: --store repo must not read them
		{opssettings.OpKindAdd, `{"kind":"server","to":"agency"}`},
		{opssettings.OpResAdd, `{"kind":"server","name":"hub_prod","to":"agency"}`},
	} {
		if _, err := run(t, a, s.op, s.in, true); err != nil {
			t.Fatalf("%s %s: %v", s.op, s.in, err)
		}
	}
	query := func(store, q string, show bool) []string {
		t.Helper()
		in := `{"query":"` + q + `","store":"` + store + `"`
		if show {
			in += `,"show_sensitive":true`
		}
		out, err := run(t, a, opssql.OpRun, in+`}`, show)
		if err != nil {
			t.Fatalf("sql --store %s %q: %v", store, q, err)
		}
		return runRows(t, out)
	}
	for _, c := range []struct {
		store, q string
		show     bool
		want     []string
	}{
		{"agency", "SELECT name FROM res_person", false, []string{"bob_store"}},
		{"agency", "SELECT source, name FROM res_person_all", false, []string{"agency|bob_store"}},
		{"agency", "SELECT name FROM res__all ORDER BY name", false, []string{"bob_store", "hub_prod"}},
		{"agency", "SELECT name FROM projects", false, []string{"prj-agency"}},
		{"agency", "SELECT source, name FROM projects_all", false, []string{"agency|prj-agency"}},
		{"repo", "SELECT name, phone FROM res_person", false, []string{"carol_repo|" + sqlite.MaskedValue}},
		{"repo", "SELECT source, name FROM res_person_all", false, []string{"repo|carol_repo"}},
		{"repo", "SELECT source, name FROM res__all", false, []string{"repo|carol_repo"}},
		{"repo", "SELECT source, name FROM projects_all", false, []string{"repo|prj-repo"}},
		// unmasked, a table only the store holds still reads no store row
		// (its plain name never falls through to the store's file)
		{"repo", "SELECT name FROM res_server", true, []string{}},
		{"repo", "SELECT name FROM res_server_all", true, []string{}},
		{"repo", "SELECT name, phone FROM res_person", true, []string{"carol_repo|555-0202"}},
	} {
		if got := query(c.store, c.q, c.show); !slices.Equal(got, c.want) {
			t.Errorf("sql --store %s %q = %v, want %v", c.store, c.q, got, c.want)
		}
	}
	// no --store: every attached store, the repo nearest, as before
	if got := query("", "SELECT source, name FROM res_person_all ORDER BY source", false); !slices.Equal(got, []string{"agency|bob_store", "repo|carol_repo"}) {
		t.Errorf("sql (no --store) res_person_all = %v", got)
	}
	_, err := run(t, a, opssql.OpRun, `{"query":"SELECT name FROM res_person","store":"nope"}`, false)
	if !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), "nope") {
		t.Errorf("sql --store nope: %v, want E_NOT_FOUND naming nope", err)
	}
}
