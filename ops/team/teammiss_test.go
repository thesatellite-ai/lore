package team

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestTeamMissHintsSyncNotAdd (devco:T-6): a team this clone does not have
// yet is most likely one a teammate made since the last sync; the answer
// names lore sync and never team add (which would make a second team).
func TestTeamMissHintsSyncNotAdd(t *testing.T) {
	t.Parallel()
	var e *errs.Error
	if !errors.As(teamMissErr("web", "acme org/"), &e) || e.Code != errs.NotFound {
		t.Fatalf("not E_NOT_FOUND: %v", e)
	}
	if !strings.Contains(e.Hint, "lore sync") || strings.Contains(e.Hint, "team add") {
		t.Errorf("hint = %q", e.Hint)
	}
}

// TestRosterSentencesD663 (devco:D-663): every sentence naming an org
// roster says "the roster of <org> (<org>/<folder>)", as team list does
// (D-647), never "the roster in agency org/": a team miss (handle or id),
// and the add, remove and rename lines.
func TestRosterSentencesD663(t *testing.T) {
	t.Parallel()
	const label = "agency (agency/org)"
	for _, h := range []string{"web", "tem_0190f0f0f0f07000800000000000dead"} {
		var e *errs.Error
		if !errors.As(teamMissErr(h, label), &e) || !strings.Contains(e.Message, "the roster of "+label) {
			t.Errorf("teamMissErr(%s) = %v", h, e)
		}
	}
	for _, verb := range []string{verbTeamAdded, verbTeamRemoved, verbTeamRenamed} {
		out := TeamWriteOut{Team: Team{ID: "tem_1", Handle: "web"}, Verb: verb, From: "old", Where: "agency org/", label: label}
		if txt := out.Text(); !strings.Contains(txt, "the roster of "+label) || strings.Contains(txt, "org/ ") || strings.Contains(txt, "roster in") {
			t.Errorf("%s: text = %q", verb, txt)
		}
	}
}
