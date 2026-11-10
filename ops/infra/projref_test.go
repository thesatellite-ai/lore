package infra_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestProjectVerbsTakeAName pins that `lore project show|archive|unarchive|
// delete` take a project name as well as a prj_ id, resolving it across
// every attached store as `project member` does: a name held only by
// another store's row resolves, a name two projects share refuses with
// E_PROJECT_NAME_COLLISION listing both ids and changes nothing, and an
// unknown name or a malformed id keeps its own refusal.
func TestProjectVerbsTakeAName(t *testing.T) {
	h := opstest.WithStores(t, []string{"agency"})
	// demo is the repo's project; acme lives only in the agency store.
	acme := h.SeedTo(store.Source("agency"), schema.TableProjects, map[string]string{"name": `"acme"`})

	for _, c := range []struct{ ref, want string }{
		{opstest.ProjectName, h.Project},
		{"  " + opstest.ProjectName + " ", h.Project},
		{h.Project, h.Project},
		{"acme", acme},
	} {
		if got := h.ID(h.Must(infra.OpProjectShow, `{"id":`+opstest.Quote(c.ref)+`}`)); got != c.want {
			t.Errorf("show %q: id %q, want %q", c.ref, got, c.want)
		}
	}
	if got := opstest.Text(h.Must(infra.OpProjectArchive, `{"id":"acme"}`)); got != "✓ acme archived\n" {
		t.Errorf("archive by name: %q", got)
	}
	// An archived project still answers to its name (unarchive needs it).
	if got := opstest.Text(h.Must(infra.OpProjectUnarchive, `{"id":"acme"}`)); got != "✓ acme unarchived\n" {
		t.Errorf("unarchive by name: %q", got)
	}

	refusals := []struct {
		name, ref string
		code      errs.Code
		msg       string
	}{
		{"unknown name", "nope", errs.NotFound, `no project "nope"`},
		{"malformed id", "prj_zz", errs.InvalidID, `"prj_zz" is not a project id`},
		{"unknown id", "prj_0190f0f0f0f07000800000000000dead", errs.NotFound, ""},
		{"id of another kind", "mem_01a1a679339b7c56896a5b5dd2cace7d", errs.InvalidID, "is not a project id"},
	}
	for _, op := range []string{infra.OpProjectShow, infra.OpProjectArchive, infra.OpProjectUnarchive, infra.OpProjectDelete} {
		for _, c := range refusals {
			_, err := h.RunYes(op, `{"id":`+opstest.Quote(c.ref)+`}`)
			if !errs.Has(err, c.code) || !strings.Contains(errText(err), c.msg) {
				t.Errorf("%s %s: err %v, want %s %q", op, c.name, err, c.code, c.msg)
			}
		}
	}

	// A second "acme" (this time in the repo store): every verb refuses
	// by name, naming both ids, and neither row is touched.
	twin := h.Seed(schema.TableProjects, map[string]string{"name": `"acme"`})
	for _, op := range []string{infra.OpProjectShow, infra.OpProjectArchive, infra.OpProjectUnarchive, infra.OpProjectDelete} {
		_, err := h.RunYes(op, `{"id":"acme"}`)
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.ProjectNameCollision {
			t.Fatalf("%s twin name: err %v, want E_PROJECT_NAME_COLLISION", op, err)
		}
		if !strings.Contains(e.Message, acme) || !strings.Contains(e.Message, twin) || !strings.Contains(e.Hint, "prj_ id") {
			t.Errorf("%s twin name: %q / %q must name %s and %s and the way out", op, e.Message, e.Hint, acme, twin)
		}
	}
	for _, id := range []string{acme, twin} {
		if got := contenttest.Str(t, h.JSON(h.Must(infra.OpProjectShow, `{"id":"`+id+`"}`)), "data", "archived_at"); got != "" {
			t.Errorf("%s archived by a refused verb: %q", id, got)
		}
	}
	// By id each twin still works.
	if got := opstest.Text(h.Must(infra.OpProjectArchive, `{"id":"`+twin+`"}`)); got != "✓ acme archived\n" {
		t.Errorf("archive twin by id: %q", got)
	}
}
