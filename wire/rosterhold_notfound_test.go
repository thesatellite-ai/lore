package wire

import (
	"errors"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// TestHeldIDNotFoundNamesTheHold (devco:D-858): a not-found of a row id a
// store holds back names the hold and the git command that shows the
// commit, never the branch hint; any other error stays as it is.
func TestHeldIDNotFoundNamesTheHold(t *testing.T) {
	const id = "mem_019a00000000700080000000000000a1"
	h := heldChange{src: "acme", dir: "/tmp/acme-clone", held: gitbackend.HeldChange{
		Path: "web/memories/" + id + ".json", Table: "memories", ID: id,
		Commit: "abc1234def5678", Author: "eve@outside.test", Reason: "the commit's author is on no roster",
	}}
	line := func(h heldChange) string {
		return "held back in acme: " + h.held.Table + " " + h.held.ID + " (commit " + h.held.Short() + " by " + h.held.Author + "): " + h.held.Reason
	}
	nf := errs.New(errs.NotFound, `memory "`+id+`" not found`)
	got := heldAnswer(nf, []heldChange{h}, line)
	var e *errs.Error
	if !errors.As(got, &e) || e.Code != errs.NotFound {
		t.Fatalf("want E_NOT_FOUND, got %v", got)
	}
	for _, want := range []string{"held back in acme", id, "commit abc1234 by eve@outside.test", "on no roster"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("message %q lacks %q", e.Message, want)
		}
	}
	for _, want := range []string{"`git -C /tmp/acme-clone show abc1234`", "`lore doctor`"} {
		if !strings.Contains(e.Hint, want) {
			t.Errorf("hint %q lacks %q", e.Hint, want)
		}
	}
	if strings.Contains(e.Hint, "landed") {
		t.Errorf("hint names a branch: %q", e.Hint)
	}

	other := errs.New(errs.NotFound, `memory "mem_019a00000000700080000000000000b2" not found`)
	if got := heldAnswer(other, []heldChange{h}, line); got != error(other) {
		t.Errorf("a not-found of another id changed: %v", got)
	}
	refused := errs.New(errs.Refused, "no: "+id)
	if got := heldAnswer(refused, []heldChange{h}, line); got != error(refused) {
		t.Errorf("a refusal naming the id changed: %v", got)
	}
	if !looksLikeRowIDIn(nf.Message) || looksLikeRowIDIn(`no store named "acme" is attached`) {
		t.Errorf("looksLikeRowIDIn: row id message %v, store message %v", looksLikeRowIDIn(nf.Message), looksLikeRowIDIn(`no store named "acme" is attached`))
	}
}

// TestWithSetupHeldReplacesAnEmptyRun (devco:D-858): where doctor skips
// the store checks, the setup's held-change findings take the place of a
// held-change run that found nothing; a run that found some is kept.
func TestWithSetupHeldReplacesAnEmptyRun(t *testing.T) {
	setup := []opsdoctor.CheckRun{{Check: string(DoctorCheckHeldChanges), Findings: []opsdoctor.Finding{{Check: string(DoctorCheckHeldChanges), Level: opsdoctor.LevelWarning, Line: "held back in acme"}}}}
	empty := []opsdoctor.CheckRun{{Check: "cache"}, {Check: string(DoctorCheckHeldChanges)}, {Check: "trash"}}
	got := withSetupHeld(empty, setup)
	if len(got) != 3 || got[1].Check != string(DoctorCheckHeldChanges) || len(got[1].Findings) != 1 || got[2].Check != "trash" {
		t.Fatalf("empty run not replaced in place: %+v", got)
	}
	found := []opsdoctor.CheckRun{{Check: string(DoctorCheckHeldChanges), Findings: setup[0].Findings}}
	if got := withSetupHeld(found, setup); len(got) != 1 || len(got[0].Findings) != 1 {
		t.Fatalf("a run with findings read twice: %+v", got)
	}
	if got := withSetupHeld([]opsdoctor.CheckRun{{Check: "cache"}}, setup); len(got) != 2 || got[1].Check != string(DoctorCheckHeldChanges) {
		t.Fatalf("setup run not added: %+v", got)
	}
}
