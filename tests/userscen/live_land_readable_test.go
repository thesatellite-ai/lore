//go:build userscen

package userscen

// live_land_readable_test.go — issue 5: with live on (fetch_ttl 0s), a
// comment add lands finished live rows and the rows they carried (the tag,
// the project, the actor) on origin/main, and they leave live once main is
// fetched. The checkout that wrote them never pulled, so every one of them
// must still be readable there: listed, shown by id, and usable by the
// next write.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestLiveLand_RowsStayReadableInTheWritingCheckout(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	app := w.Remote(syncSlugApp)
	p := w.Human("alice", "alice@example.com")
	p.WriteConfig(th.Config{Live: th.FreshFetch()})
	c := p.Clone(app, "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	c.CommitAll("lore")
	c.AdminPush("origin", "main")
	me := trashMe(t, c)
	tag := trashID(t, c.MustLore("tag", "add", "--name", "ttag", "--json"))

	type made struct{ table, list, id, comment string }
	var rows []made
	adds := []struct {
		table string
		args  []string
		list  []string
	}{
		{"architecture_notes", []string{"architecturenote", "add", "--title", "arch title", "--body", "arch body"}, []string{"architecturenote", "list"}},
		{"handoffs", []string{"handoff", "add", "--for", me, "--body", "handoff body"}, []string{"handoff", "list"}},
		{"hotfixes", []string{"hotfix", "add", "--title", "hotfix title", "--body", "hotfix body", "--severity", "high"}, []string{"hotfix", "list"}},
	}
	landed := false
	for _, a := range adds {
		id := trashID(t, c.MustLore(append(a.args, "--json")...))
		c.MustLore("tag", "attach", "--on-table", a.table, "--on-id", id, "--tag", "ttag").Never(t, "E_NOT_FOUND")
		r := c.MustLore("comment", "add", "--on-table", a.table, "--on-id", id, "--body", "comment on "+a.table, "--json")
		r.Never(t, "landed on main") // a write never lands by itself (U-2)
		landed = landed || strings.Contains(c.MustLore("sync").Out(), "landed on main")
		rows = append(rows, made{table: a.table, list: strings.Join(a.list, " "), id: id, comment: trashID(t, r)})
	}
	if !landed {
		t.Fatal("no comment add landed on main: the scenario did not reach the land it tests")
	}
	// The state the issue is about: at least the tag and the first two
	// comments landed on origin/main, left live, and are not in HEAD (this
	// checkout never pulled).
	// what the last sync landed leaves live once main is fetched: this
	// sync fetches it, the next one sees main holds the rows and drops them
	c.MustLore("sync")
	c.MustLore("sync")
	c.MustLore("live", "show")
	onMain := c.Git("ls-tree", "-r", "--name-only", "origin/main")
	inHead := c.Git("ls-tree", "-r", "--name-only", "HEAD")
	inLive := c.Git("ls-tree", "-r", "--name-only", "refs/lore/live")
	for _, id := range []string{tag, rows[0].comment, rows[1].comment} {
		if !strings.Contains(onMain, id) || strings.Contains(inHead, id) || strings.Contains(inLive, id) {
			t.Fatalf("%s: on origin/main %v, in HEAD %v, live %v; want landed, not pulled, left live",
				id, strings.Contains(onMain, id), strings.Contains(inHead, id), strings.Contains(inLive, id))
		}
	}

	// Every row this checkout wrote, read in every way a person reads it.
	c.MustLore("tag", "list").Says(t, "ttag")
	c.MustLore("show", tag).Says(t, "ttag").Never(t, "E_NOT_FOUND")
	c.MustLore("project", "list").Says(t, "app")
	c.MustLore("whoami").Never(t, "E_NOT_FOUND")
	for _, r := range rows {
		c.MustLore(strings.Fields(r.list)...).Says(t, r.id)
		c.MustLore("show", r.id).Never(t, "E_NOT_FOUND")
		c.MustLore("comment", "list", "--on-table", r.table, "--on-id", r.id).Says(t, "comment on "+r.table)
		c.MustLore("show", r.comment).Says(t, "comment on "+r.table).Never(t, "E_NOT_FOUND")
	}
	// The next writes still find what landed.
	c.MustLore("tag", "attach", "--on-table", rows[0].table, "--on-id", rows[0].id, "--tag", "ttag").Never(t, "E_NOT_FOUND")
	c.MustLore("comment", "add", "--on-table", rows[1].table, "--on-id", rows[1].id, "--body", "second comment").Never(t, "E_NOT_FOUND")
	c.MustLore("comment", "list", "--on-id", rows[1].id).Says(t, "comment on handoffs", "second comment")
}
