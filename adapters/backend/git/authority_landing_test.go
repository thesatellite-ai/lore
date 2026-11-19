package git

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

// projectRowAt is a project row file of id made by creator at created and
// last written by writer at wall (the stamp's actor part is the writer).
func projectRowAt(id, creator, writer ids.AnyID, created, wall string) string {
	return `{"_table":"projects","_v":2,"_hlc":"` + wall + `/0000/` + writer.String() + `","id":"` + id.String() +
		`","name":"app","created_at":"` + created + `","created_by":"` + creator.String() + `","updated_at":"` + wall +
		`","updated_by":"` + writer.String() + `"}` + "\n"
}

// TestLandingMergeOfAGatedRowIsNotHeld (devco V-2): a member lands a
// branch whose project row a teammate also made on the store branch; the
// landing merges the row field by field (the newer side's stamp and
// writer, the older side's creator). As one merge commit of the store
// branch and the layer, the merged row carries a stamp an accepted
// version has, so nobody's sync holds it back. The same bytes in a
// one-parent commit (the squash landing of the first branch-layer builds)
// read as the member claiming the teammate's write, and are held.
func TestLandingMergeOfAGatedRowIsNotHeld(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		merge    bool
		wantHeld bool
	}{
		{"merge commit", true, false},
		{"squash commit", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := newOrgTeam(t)
			mo := newPerson(t, "mo@example.test")
			o.h.commit(o.boss.email, map[string]string{orgPath("actors", mo.actor): mo.actorRow(), orgPath("actor_periods", mo.period): mo.periodRow("member", "")})
			proj := mustID[ids.Project](t)
			p := orgPath("projects", proj)
			const early, late = "2026-01-03T00:00:00.000Z", "2026-01-03T00:00:05.000Z"

			gitT(t, o.h.dir, "switch", "--quiet", "-c", "layer")
			o.h.commitLocal(o.max.email, map[string]string{p: projectRowAt(proj, o.max.actor, o.max.actor, early, early)})
			gitT(t, o.h.dir, "switch", "--quiet", testBranch)
			o.h.commitLocal(mo.email, map[string]string{p: projectRowAt(proj, mo.actor, mo.actor, late, late)})
			merged := projectRowAt(proj, o.max.actor, mo.actor, early, late)
			if tc.merge {
				gitT(t, o.h.dir, "-c", "user.name=x", "-c", "user.email="+o.max.email, "merge", "--quiet", "--no-ff", "--no-commit", "-s", "ours", "layer")
			}
			o.h.commit(o.max.email, map[string]string{p: merged})

			b := orgFolderClone(t, o.h.remote)
			why := heldReason(t, b, proj)
			if held := why != ""; held != tc.wantHeld {
				t.Fatalf("held = %v (%q), want %v", held, why, tc.wantHeld)
			}
		})
	}
}
