//go:build userscen

package userscen

import (
	"strings"
	"testing"
)

// TestMigrateFreshOrgNeedsNoPeriodStep (#22d): in a repo of an org that
// 0.1.18 created (`lore org create`), `lore migrate run` has no
// actor_periods step for the roster row org create wrote, and the
// person's company period still reads from the roster.
func TestMigrateFreshOrgNeedsNoPeriodStep(t *testing.T) {
	o := newOrgScen(t)
	for _, args := range [][]string{{"migrate", "run", "--dry-run"}, {"migrate", "run", "--no-push"}} {
		out := o.app.Lore(args...).OK(t).Stdout
		if strings.Contains(out, "actor_periods") || strings.Contains(out, "company period") {
			t.Fatalf("lore %s in a fresh 0.1.18 org reports a period step:\n%s", strings.Join(args, " "), out)
		}
	}
	o.app.MustLore("actor", "period", "list", "@mira").Says(t, "employee", "(from the roster: active/left_at)")
}
