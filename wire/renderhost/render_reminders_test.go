package renderhost

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opsreminder "github.com/thesatellite-ai/lore/ops/reminder"
)

// TestRenderRemindersBlock (D-148, r3 G-5): the rendered file names the
// project's pending reminders, oldest due first, with their due date and
// id, so an agent reading LORE.md hears of an overdue one; a reminder
// marked done leaves the block, and no pending reminder means no block.
// The block holds dates, never "overdue", so the same rows render the
// same bytes on any day.
func TestRenderRemindersBlock(t *testing.T) {
	ctx := context.Background()
	h := opstest.New(t)
	host := New(Options{App: h.App})
	pid, err := host.projectID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	block := func() string {
		t.Helper()
		got, err := host.remindersBlock(ctx, pid)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := block(); got != "" {
		t.Fatalf("no reminder, block %q", got)
	}
	late := h.ID(h.MustYes(opsreminder.OpAdd, `{"due":"2026-11-11","message":"rotate the queue credentials"}`))
	later := h.ID(h.MustYes(opsreminder.OpAdd, `{"due":"2030-01-02","message":"renew the domain"}`))
	done := h.ID(h.MustYes(opsreminder.OpAdd, `{"due":"2026-10-01","message":"old chore"}`))
	h.MustYes(opsreminder.OpDone, `{"id":"`+done+`"}`)
	got := block()
	want := renderRemindersHead +
		"- 2026-11-11  rotate the queue credentials (" + late + ")\n" +
		"- 2030-01-02  renew the domain (" + later + ")\n"
	if got != want {
		t.Fatalf("block\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "old chore") || strings.Contains(got, "(overdue") {
		t.Fatalf("block names a done reminder or a day-dependent state:\n%s", got)
	}
	tails, err := host.renderTails(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(tails) == 0 || tails[0].Name != "reminders" || tails[0].Text != want {
		t.Fatalf("the reminders block is not the first tail: %+v", tails)
	}
}
