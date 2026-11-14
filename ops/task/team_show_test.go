package task_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestShowNamesTheTeam (devco:J-13): task show names the team a task is
// assigned to, by its handle, in text ("team: @core") and JSON
// (assigned_team, assigned_team_handle); a task with no team shows none.
func TestShowNamesTheTeam(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	ann := newActor(h, "Ann")
	team := newTeam(h, "core", ann)
	ft := &fakeTeam{people: map[string]string{"@ann": ann}, teams: map[string]string{"@core": team}, board: &fakeBoard{}}
	ctx := opstask.WithTeam(context.Background(), ft)
	id := addID(h, mustCtx(h, ctx, opstask.OpAdd, `{"title":"t","assign":["@core"]}`))
	res := mustCtx(h, ctx, opstask.OpShow, `{"id":"`+id+`"}`)
	if txt, _ := res.Text(); !strings.Contains(txt, "team:     @core") {
		t.Errorf("text:\n%s", txt)
	}
	if j := h.JSON(res); !strings.Contains(j, `"assigned_team": "`+team+`"`) && !strings.Contains(j, `"assigned_team":"`+team+`"`) || !strings.Contains(j, "assigned_team_handle") {
		t.Errorf("json: %s", j)
	}
	plain := addID(h, mustCtx(h, ctx, opstask.OpAdd, `{"title":"p","assign":["@ann"]}`))
	if txt, _ := mustCtx(h, ctx, opstask.OpShow, `{"id":"`+plain+`"}`).Text(); strings.Contains(txt, "team:") {
		t.Errorf("person-assigned task shows a team:\n%s", txt)
	}
}

// TestAssignUnknownEmailNamesSync (devco:J-14): --assign with an email no
// one on the roster here has says the roster may be older than the store
// (an email change) and names `lore sync`.
func TestAssignUnknownEmailNamesSync(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	_, err := h.Run(opstask.OpAdd, `{"title":"t","assign":["mira.new@example.test"]}`)
	var e *errs.Error
	if !errs.Has(err, errs.NotFound) || !errors.As(err, &e) || !strings.Contains(e.Hint, "lore sync") {
		t.Fatalf("err %v", err)
	}
}
