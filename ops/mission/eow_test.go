package mission_test

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/wire"
)

// TestMissionTargetWeekEnd (OV15c, TASK 12.1): mission --target eow reads
// tasks.week_end, so with the week ending on Wednesday "eow" on a Monday
// is that Wednesday, and with the default (Friday) it is the Friday; an
// edit reads it the same way.
func TestMissionTargetWeekEnd(t *testing.T) {
	monday := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	h := opstest.New(t, func(o *wire.Options) { o.Wall = func() time.Time { return monday } })
	fri := addMission(t, h, `{"title":"default week","target":"eow"}`)
	if got := h.JSON(h.Must("mission.show", `{"id":"`+fri+`"}`)); !strings.Contains(got, `"target_date":"2026-11-06`) {
		t.Errorf("default eow: %s", got)
	}
	h.MustYes(settings.OpPolicySet, `{"key":"tasks.week_end","value":"wed"}`)
	wed := addMission(t, h, `{"title":"wednesday week","target":"eow"}`)
	if got := h.JSON(h.Must("mission.show", `{"id":"`+wed+`"}`)); !strings.Contains(got, `"target_date":"2026-11-04`) {
		t.Errorf("week_end wed eow: %s", got)
	}
	h.Must("mission.edit", `{"id":"`+fri+`","target":"eow"}`)
	if got := h.JSON(h.Must("mission.show", `{"id":"`+fri+`"}`)); !strings.Contains(got, `"target_date":"2026-11-04`) {
		t.Errorf("edit eow with week_end wed: %s", got)
	}
}
