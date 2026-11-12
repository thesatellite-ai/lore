package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestRosterRefusalsNameOrgFolder: every refusal naming an org roster
// names its org folder ("agency org/") as the success lines do, in a pipe
// and in CI too (an org folder is no secret; refusals said "<redacted>"),
// and the repo's own roster as "this repo". Each refusal keeps its code
// and its hint.
func TestRosterRefusalsNameOrgFolder(t *testing.T) {
	t.Setenv(ciMarkers[0], "1")
	id, err := ids.ParseAny("act_0190f0f0f0f07000800000000000dead")
	if err != nil {
		t.Fatal(err)
	}
	refusals := []struct {
		name string
		code errs.Code
		hint bool
		err  func(rosterAt) error
	}{
		{"handle taken", errs.InvalidInput, true, func(r rosterAt) error { return r.handleTakenErr("mira", "team ops") }},
		{"handle missing", errs.InvalidInput, true, func(r rosterAt) error { return r.handleMissErr("nobody") }},
		{"caller left", errs.StatusChange, true, func(r rosterAt) error { return r.goneErr("left", "@alice") }},
		{"caller unregistered", errs.InvalidInput, true, func(r rosterAt) error { return r.unregisteredErr() }},
		{"deleted row", errs.NotFound, false, func(r rosterAt) error { return r.deletedRowErr(schema.TableActors, id) }},
		{"actor missing", errs.NotFound, true, func(r rosterAt) error { return r.actorMissErr("@nobody") }},
	}
	for _, c := range refusals {
		for _, m := range []struct {
			mode string
			at   rosterAt
			want string
		}{
			{"org", newRosterAt(store.Source("agency-notes"), "agency org/"), "agency org/"},
			{"repo", newRosterAt(store.SourceRepo, repoRosterWhere), repoRosterWhere},
		} {
			e := errs.EnvelopeOf(c.err(m.at)).Error
			if e.Code != c.code {
				t.Errorf("%s / %s: code %s, want %s", c.name, m.mode, e.Code, c.code)
			}
			if !strings.Contains(e.Message, "on the roster in "+m.want) || strings.Contains(e.Message+e.Hint, redactedText) {
				t.Errorf("%s / %s: message %q does not name the roster as %q", c.name, m.mode, e.Message, m.want)
			}
			if c.hint && e.Hint == "" {
				t.Errorf("%s / %s: hint lost", c.name, m.mode)
			}
		}
	}
}
