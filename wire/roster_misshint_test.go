package wire

// roster_misshint_test.go — t-e2e4 (4f6b9a4a): an @handle that is not on
// the roster is refused with a hint that fits where the roster lives. An
// org roster is a store copy that may predate a teammate's registration, so
// its miss says to register and then `lore sync`; a repo's own roster has
// no copy to be stale, so its miss only says to register.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// orgRepoApp is an own repo of org "agency" (its @handles resolve on the
// org roster) with a project.
func orgRepoApp(t *testing.T) *App {
	t.Helper()
	w := newOrgWorld(t)
	mira := w.home(t, "mira")
	if _, err := orgHostAt(t, w.repo, mira).Create(context.Background(), opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org",
		Own: []string{"github.com/agency/*"}, Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("create: %v", err)
	}
	dir := w.ownRepo(t, mira)
	app, err := New(StandaloneOptions(dir, mira, dir, "test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	withProject(t, app)
	return app
}

// TestRosterMissHintBySource: task add --assign @ghost and the handle
// lookup itself (teams allowed or not) refuse with E_NOT_FOUND /
// E_INVALID_INPUT; on the org roster the hint names `lore sync`, on the
// repo roster it never does; both name `lore actor register`.
func TestRosterMissHintBySource(t *testing.T) { // not parallel: the org world sets env
	repoApp, _, _ := standaloneTask(t)
	for _, c := range []struct {
		name     string
		app      *App
		wantSync bool
	}{
		{"org roster", orgRepoApp(t), true},
		{"repo roster", repoApp, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			check := func(how string, err error, code errs.Code) {
				t.Helper()
				var e *errs.Error
				if !errors.As(err, &e) || e.Code != code {
					t.Fatalf("%s: err = %v, want %s", how, err, code)
				}
				if !strings.Contains(e.Hint, "lore actor register") {
					t.Errorf("%s: hint %q does not say how people join", how, e.Hint)
				}
				if got := strings.Contains(e.Hint, "lore sync"); got != c.wantSync {
					t.Errorf("%s: hint %q names `lore sync` = %v, want %v", how, e.Hint, got, c.wantSync)
				}
			}
			_, err := run(t, c.app, opstask.OpAdd, `{"title":"ship","assign":["@ghost"]}`, true)
			check("task add --assign @ghost", err, errs.NotFound)
			team := standaloneTeam{a: c.app}
			_, err = team.ResolveHandle(background(), "@ghost", true)
			check("handle, teams allowed", err, errs.NotFound)
			_, err = team.ResolveHandle(background(), "@ghost", false)
			check("handle, people only", err, errs.InvalidInput)
		})
	}
}
