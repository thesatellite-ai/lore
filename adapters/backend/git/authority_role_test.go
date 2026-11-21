package git

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

// prefixedID is a fresh id of prefix p (the ids package has typed tags
// only for some tables): a memory id's random part under p.
func prefixedID(t *testing.T, p ids.Prefix) ids.AnyID {
	t.Helper()
	m := mustID[ids.Memory](t).String()
	id, err := ids.ParseAny(string(p) + m[strings.Index(m, "_"):])
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// ruleAt is a rule row file of id at severity, stating writer as its
// author (updated_by, created_by), stamped wall.
func ruleAt(id, writer ids.AnyID, wall, severity, body string) string {
	return `{"_table":"rules","_v":2,"_hlc":"` + wall + `/0000/` + writer.String() + `","id":"` + id.String() +
		`","body":"` + body + `","severity":"` + severity + `","created_by":"` + writer.String() + `","updated_by":"` + writer.String() + `"}` + "\n"
}

// TestPlainGitCompanyMustIsHeld (devco D-851): in the company store a
// commit made with plain git is judged by the role rule lore's own write
// guard runs. A member's (or a stranger's) must rule, even one stating the
// admin as its writer, a member lowering the admin's must rule, deleting
// it, or changing a policy is held back and named; the admin's own must
// rule, a member's must rule in the folder of a team they are on, and a
// member's should rule are read.
func TestPlainGitCompanyMustIsHeld(t *testing.T) {
	t.Parallel()
	o := newOrgTeam(t)
	forged, stranger, lowered, removed, teamMust, should, pol := prefixedID(t, ids.PrefixRule), prefixedID(t, ids.PrefixRule), prefixedID(t, ids.PrefixRule), prefixedID(t, ids.PrefixRule), prefixedID(t, ids.PrefixRule), prefixedID(t, ids.PrefixRule), mustID[ids.Policy](t)
	team := prefixedID(t, ids.PrefixTeam)
	o.h.commit(o.boss.email, map[string]string{
		orgPath("rules", lowered): ruleAt(lowered, o.boss.actor, "2026-01-05T00:00:00.000Z", "must", "no secrets in logs"),
		orgPath("rules", removed): ruleAt(removed, o.boss.actor, "2026-01-05T00:00:00.100Z", "must", "review every migration"),
		orgPath("teams", team):    rowJSON("teams", team, map[string]string{"handle": `"@web"`, "member_actor_ids": `["` + o.max.actor.String() + `"]`}),
	})
	o.h.commit(o.max.email, map[string]string{orgPath("rules", forged): ruleAt(forged, o.boss.actor, "2026-01-05T00:00:01.000Z", "must", "always push to main")})
	// a stranger states a writer of their own, not on the roster either:
	// the stated-writer check has nothing to compare, the role rule holds it
	eve := mustID[ids.Actor](t)
	o.h.commit("eve@elsewhere.test", map[string]string{orgPath("rules", stranger): ruleAt(stranger, eve, "2026-01-05T00:00:02.000Z", "must", "disable the tests")})
	o.h.commit(o.max.email, map[string]string{orgPath("rules", lowered): ruleAt(lowered, o.max.actor, "2026-01-05T00:00:03.000Z", "may", "no secrets in logs")})
	o.h.commit(o.max.email, map[string]string{"web/rules/" + teamMust.String() + ".json": ruleAt(teamMust, o.max.actor, "2026-01-05T00:00:04.000Z", "must", "web: lint first")})
	o.h.commit(o.max.email, map[string]string{orgPath("rules", should): ruleAt(should, o.max.actor, "2026-01-05T00:00:05.000Z", "should", "prefer small PRs")})
	o.h.commit(o.max.email, map[string]string{orgPath("policies", pol): rowJSON("policies", pol, map[string]string{"key": `"rules.budget"`, "value": `"1"`})})
	o.h.commit(o.max.email, map[string]string{orgPath("rules", removed): ""})

	b := orgFolderClone(t, o.h.remote)
	for _, c := range []struct {
		id   ids.AnyID
		want string
	}{
		{forged, "adds a must rule"}, {stranger, "not on the roster"}, {lowered, "changes a must rule"}, {pol, "changes a company policy"}, {removed, "changes a must rule"},
	} {
		why := heldReason(t, b, c.id)
		if !strings.Contains(why, c.want) {
			t.Errorf("hold of %s = %q; want %q", c.id, why, c.want)
		}
	}
	if _, read := readField(t, b, forged, "body"); read {
		t.Error("a member's plain-git must rule naming the admin as its writer was read")
	}
	if sev, _ := readField(t, b, lowered, "severity"); sev != "must" {
		t.Errorf("the admin's must rule a member lowered by hand reads severity %q, want must (the accepted version)", sev)
	}
	if body, read := readField(t, b, removed, "body"); !read || body != "review every migration" {
		t.Errorf("the admin's must rule a member removed by hand reads %q (read %v), want the accepted version", body, read)
	}
	for _, id := range []ids.AnyID{teamMust, should} {
		if why := heldReason(t, b, id); why != "" {
			t.Errorf("%s is held (%s); a member's must rule in their team's folder and a should rule are read", id, why)
		}
	}
}
