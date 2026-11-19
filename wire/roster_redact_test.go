package wire

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	opsinfra "github.com/thesatellite-ai/lore/ops/infra"

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
			{"org", newRosterAt(store.Source("agency/org"), "agency org/"), "agency (agency/org)"},
			{"repo", newRosterAt(store.SourceRepo, repoRosterWhere), repoRosterWhere},
		} {
			e := errs.EnvelopeOf(c.err(m.at)).Error
			if e.Code != c.code {
				t.Errorf("%s / %s: code %s, want %s", c.name, m.mode, e.Code, c.code)
			}
			if !strings.Contains(e.Message, "the roster of "+m.want) || strings.Contains(e.Message+e.Hint, redactedText) {
				t.Errorf("%s / %s: message %q does not name the roster as %q", c.name, m.mode, e.Message, m.want)
			}
			if c.hint && e.Hint == "" {
				t.Errorf("%s / %s: hint lost", c.name, m.mode)
			}
		}
	}
}

// TestRosterSuccessLinesD668: a success line names the roster it wrote
// to the one way every roster sentence does, "in the roster of agency
// (agency/org)" (or "of this repo"), never the folder shape "registered
// in agency org/" (devco D-668): shown is what each op's Where carries.
func TestRosterSuccessLinesD668(t *testing.T) {
	for _, c := range []struct {
		at   rosterAt
		want string
	}{
		{newRosterAt(store.Source("agency/org"), "agency org/"), "agency (agency/org)"},
		{newRosterAt(store.SourceRepo, repoRosterWhere), repoRosterWhere},
	} {
		if got := c.at.shown(); got != c.want {
			t.Errorf("shown() = %q, want %q", got, c.want)
		}
		line := opsinfra.ActorRegisterOut{ID: "act_1", Kind: opsinfra.RegisterKindHuman, Name: "Mira", Created: true, Where: c.at.shown()}.Text()
		if !strings.Contains(line, "registered in the roster of "+c.want) || strings.Contains(line, " org/") {
			t.Errorf("register line %q does not say \"in the roster of %s\"", line, c.want)
		}
	}
}

// rosterPossessive is a roster's place glued to "'s roster" in source: a
// Where (a label since D-668) or a folder spelling followed by a
// possessive, "on acme (acme/org)'s roster".
var rosterPossessive = regexp.MustCompile(`(Where\(\)|DefaultWhere|shown\(\)|\.where)\s*\+\s*"'s roster`)

// TestNoRosterPossessiveD668 walks the ops and wire sources: no sentence
// names a roster as "<where>'s roster"; each says "the roster of <where>"
// (devco D-668 found offboard's and the company-rows admin refusal's).
func TestNoRosterPossessiveD668(t *testing.T) {
	for _, dir := range []string{".", filepath.Join("..", "ops")} {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for i, l := range strings.Split(string(b), "\n") {
				if rosterPossessive.MatchString(l) {
					t.Errorf("%s:%d names a roster as \"<where>'s roster\"; say \"the roster of \"+<where>: %s", p, i+1, strings.TrimSpace(l))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
