package sqlite

import (
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestTeamVersionsMemo (D-739, #7): a second read with no row changed is
// served from the memo (the rows are not parsed again), and every change to
// `_rows`, through Apply or a raw write of another writer, makes the next
// read query the rows again.
func TestTeamVersionsMemo(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	prj := newPrj()
	m := must(ids.ParseMachine("mch_cd34" + strings.Repeat("0", 60)))
	f.apply(c, store.SourceRepo, "1", f.stamped(newMem(), prj, "0.1.18", m))
	first := must(c.TeamVersions(f.ctx))
	if len(first) != 1 || first[0].Version != "0.1.18" {
		t.Fatalf("first read %+v", first)
	}

	// Served from the memo: a marker answer stored under the current
	// generation comes back (on the old code the rows were parsed again).
	if _, err := c.db.ExecContext(f.ctx, `UPDATE "`+tblTeamVersionMemo+`" SET doc = '[{"Actor":"memo","Version":"9.9.9"}]'`); err != nil {
		t.Fatalf("no memo stored after a read: %v", err)
	}
	if got := must(c.TeamVersions(f.ctx)); len(got) != 1 || got[0].Actor != "memo" {
		t.Fatalf("second read %+v, want the memo's answer", got)
	}

	// An Apply moves the generation: the next read sees the new save.
	f.apply(c, store.SourceRepo, "2", f.stamped(newMem(), prj, "0.1.19", m))
	if got := must(c.TeamVersions(f.ctx)); len(got) != 1 || got[0].Version != "0.1.19" {
		t.Fatalf("after apply %+v, want 0.1.19", got)
	}

	// A writer that is not this code (an older lore on the same file)
	// still moves it: the triggers live in the file.
	if _, err := c.db.ExecContext(f.ctx, `UPDATE "`+tblTeamVersionMemo+`" SET doc = '[{"Actor":"stale"}]'`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.ExecContext(f.ctx, `DELETE FROM "`+tblRows+`"`); err != nil {
		t.Fatal(err)
	}
	if got := must(c.TeamVersions(f.ctx)); len(got) != 0 {
		t.Fatalf("after a raw delete %+v, want no versions", got)
	}

	// A memo that does not decode is a miss, never an error.
	f.apply(c, store.SourceRepo, "3", f.stamped(newMem(), prj, "0.1.20", m))
	_ = must(c.TeamVersions(f.ctx))
	if _, err := c.db.ExecContext(f.ctx, `UPDATE "`+tblTeamVersionMemo+`" SET doc = 'not json'`); err != nil {
		t.Fatal(err)
	}
	if got := must(c.TeamVersions(f.ctx)); len(got) != 1 || got[0].Version != "0.1.20" {
		t.Fatalf("damaged memo %+v, want the rows' answer", got)
	}
}

// TestDDLStampCoversInitDDL (D-739): the stamp an existing cache file is
// checked against hashes every internal statement, those appended in init()
// included. It was a package variable, computed before any init() ran, so a
// file stamped by an older binary looked current and never got the memo's
// tables and triggers (nor team_versions.go's view or kindsgood.go's table).
func TestDDLStampCoversInitDDL(t *testing.T) {
	t.Parallel()
	h := fnv.New32a()
	for _, s := range append(slices.Clone(internalDDL), taskTreeDDL...) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	_, _ = h.Write([]byte(strconv.Itoa(layoutVersion)))
	if want := int32(h.Sum32()); ddlStamp() != want {
		t.Fatalf("ddlStamp %d does not cover the internal DDL of this binary (%d)", ddlStamp(), want)
	}
	found := false
	for _, s := range internalDDL {
		found = found || strings.Contains(s, tblRowsGen)
	}
	if !found {
		t.Fatal("the rows generation table is not in the internal DDL")
	}
}
