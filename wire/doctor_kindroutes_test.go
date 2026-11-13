package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestDoctorKindRoutes (G-META-9): no kind, or kinds with ordinary names,
// gives no finding; a kind written outside kind add (an earlier build, a
// hand edit) named like a resource command (lint, report) is one warning
// per kind naming the route and the rename; doctor's check list runs it.
func TestDoctorKindRoutes(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	if got, err := a.DoctorKindRoutes(background()); err != nil || len(got) != 0 {
		t.Fatalf("no kinds: %v %v", got, err)
	}
	if _, err := run(t, a, settings.OpKindAdd, `{"kind":"lint_rule"}`, true); err != nil {
		t.Fatal(err)
	}
	if got, err := a.DoctorKindRoutes(background()); err != nil || len(got) != 0 {
		t.Fatalf("ordinary kind: %v %v", got, err)
	}
	for _, n := range []string{"lint", "report"} {
		txt, err := value.TextOf(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.Create(background(), store.SourceRepo, schema.TableKinds, map[string]value.Value{"name": txt}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := a.DoctorKindRoutes(background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 findings, got %+v", got)
	}
	for i, n := range []string{"lint", "report"} {
		f := got[i]
		if f.Level != DoctorWarning || f.Check != DoctorCheckKindRoute || !strings.Contains(f.Message, "/v1/resources/"+n+" runs lore resource "+n) ||
			f.Fix != "lore resource kind rename --id "+n+" --new-name <new_name>" {
			t.Errorf("finding %+v", f)
		}
	}
	found := false
	for _, r := range a.DoctorChecks(background(), false) {
		if r.Check == string(DoctorCheckKindRoute) {
			found = len(r.Findings) == 2
		}
	}
	if !found {
		t.Fatal("doctor's check list does not run kind_route_collision")
	}
}
