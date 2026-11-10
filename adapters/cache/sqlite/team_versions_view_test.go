package sqlite

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestTeamVersionsViewInLens (TEAMV-VIEW in lore sql, fd3408e2): every
// cache file holds the team_versions view with the columns actor,
// machine, version, last_seen, giving exactly TeamVersions' rows (newest
// stamped save per actor and machine, unstamped rows left out); a lens
// over several caches exposes team_versions (nearest store) and
// team_versions_all with a leading source column, a cache with no stamped
// save adds no row, and the masked lens reads it too.
func TestTeamVersionsViewInLens(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	repo := f.open("repo.db")
	agency := f.open("agency.db")
	empty := f.open("empty.db")
	prj := newPrj()
	m := must(ids.ParseMachine("mch_ab12" + strings.Repeat("0", 60)))
	f.apply(repo, store.SourceRepo, "1",
		f.stamped(newMem(), prj, "0.1.18", m),
		f.stamped(newMem(), prj, "0.1.19", m), // newer: wins for (actor, machine)
		f.memory(newMem(), prj, "unstamped"),
	)
	f.apply(agency, store.Source("agency"), "1", f.stamped(newMem(), prj, "0.1.18", ids.MachineID{}))
	f.apply(empty, store.Source("empty"), "1", f.memory(newMem(), prj, "no stamp"))
	want, err := repo.TeamVersions(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	srcs := []LensSource{{Name: "repo", Path: f.dir + "/repo.db"}, {Name: "agency", Path: f.dir + "/agency.db"}, {Name: "empty", Path: f.dir + "/empty.db"}}
	for _, masked := range []bool{false, true} {
		l, err := OpenLensWith(f.ctx, srcs, LensOptions{MaskSensitive: masked})
		if err != nil {
			t.Fatal(err)
		}
		res, err := l.Query(f.ctx, "SELECT actor, machine, version, last_seen FROM team_versions")
		if err != nil {
			t.Fatalf("masked=%v: %v", masked, err)
		}
		if len(res.Rows) != len(want) || len(want) != 1 {
			t.Fatalf("masked=%v: team_versions %+v, TeamVersions %+v", masked, res.Rows, want)
		}
		r := res.Rows[0]
		if r[0].String() != want[0].Actor || r[1].String() != m.String() || r[2].String() != "0.1.19" || r[3].String() != want[0].LastSeen {
			t.Fatalf("masked=%v: view row %v, want %+v", masked, r, want[0])
		}
		all, err := l.Query(f.ctx, "SELECT source, version FROM team_versions_all ORDER BY source")
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(all.Rows))
		for _, row := range all.Rows {
			got = append(got, row[0].String()+"="+row[1].String())
		}
		if strings.Join(got, ",") != "agency=0.1.18,repo=0.1.19" {
			t.Fatalf("masked=%v: team_versions_all = %v", masked, got)
		}
		_ = l.Close() // read-only lens
	}
}
