package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestRosterRefusalsHideOrgFolder (§6.5): every refusal naming an org
// roster hides the org folder ("agency org/") without a terminal, exactly
// as the success lines do, and names it on a terminal; the repo's own
// roster ("this repo") is never hidden. Each refusal keeps its code and
// its hint either way.
func TestRosterRefusalsHideOrgFolder(t *testing.T) {
	t.Parallel()
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
	org := store.Source("agency-notes")
	for _, c := range refusals {
		for _, m := range []struct {
			mode     string
			at       rosterAt
			want     string
			forbid   string
			redacted bool
		}{
			{"org, no terminal", newRosterAtFor(org, "agency org/", false), redactedText, "org/", true},
			{"org, zero value", rosterAt{src: org, where: "agency org/"}, redactedText, "org/", true},
			{"org, terminal", newRosterAtFor(org, "agency org/", true), "agency org/", redactedText, false},
			{"repo, no terminal", newRosterAtFor(store.SourceRepo, repoRosterWhere, false), repoRosterWhere, redactedText, false},
		} {
			e := errs.EnvelopeOf(c.err(m.at)).Error
			all := e.Message + "\n" + e.Hint
			if e.Code != c.code {
				t.Errorf("%s / %s: code %s, want %s", c.name, m.mode, e.Code, c.code)
			}
			if !strings.Contains(e.Message, "on the roster in "+m.want) {
				t.Errorf("%s / %s: message %q does not name the roster as %q", c.name, m.mode, e.Message, m.want)
			}
			if strings.Contains(all, m.forbid) {
				t.Errorf("%s / %s: %q shows %q", c.name, m.mode, all, m.forbid)
			}
			if c.hint && e.Hint == "" {
				t.Errorf("%s / %s: hint lost", c.name, m.mode)
			}
		}
	}
}

// newRosterAtFor is newRosterAt with the terminal decision given, so both
// sides of §6.5 are tested without a real terminal.
func newRosterAtFor(src store.Source, where string, terminal bool) rosterAt {
	r := newRosterAt(src, where)
	r.reveal = terminal
	return r
}
