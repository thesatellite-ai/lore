package wire_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/schema"
)

// TestEveryAddKeepsSourceRef (devco D-425): every add whose row keeps a
// source_ref offers the one shared --source-ref flag (json source_ref) and
// stores it, so `<noun> show` gives it back. hotfix, pattern, playbook and
// snapshot add had no such flag: agents put their sources into the body.
func TestEveryAddKeepsSourceRef(t *testing.T) {
	h := opstest.New(t)
	const ref = "https://tickets.example/T-9"
	adds := map[string]string{
		"memory":   `{"body":"the build needs go 1.22"`,
		"rule":     `{"body":"use go 1.22"`,
		"decision": `{"title":"go 1.22","body":"we pin go 1.22"`,
		"hotfix":   `{"title":"retry the push","body":"the remote drops the first push"`,
		"pattern":  `{"title":"wrap errors","body":"fmt.Errorf with %w"`,
		"playbook": `{"title":"release","body":"tag, build, upload"`,
		"snapshot": `{"title":"week 1","body":"what we know"`,
		"task":     `{"title":"pin go 1.22"`, // D-488
	}
	for noun, in := range adds {
		e, ok := h.App.Registry().Lookup(noun + ".add")
		if !ok {
			t.Fatalf("no op %s.add", noun)
		}
		found := false
		for _, f := range e.InputFields() {
			if f.Name == "source_ref" {
				found = f.Flag == "source-ref"
			}
		}
		if !found {
			t.Errorf("%s add has no --source-ref (json source_ref)", noun)
			continue
		}
		id := h.ID(h.MustYes(noun+".add", in+`,"source_ref":"`+ref+`"}`))
		if got := h.JSON(h.Must(noun+".show", `{"id":"`+id+`"}`)); !strings.Contains(got, `"source_ref":"`+ref+`"`) {
			t.Errorf("%s show does not give back the source_ref:\n%s", noun, got)
		}
	}
}

// TestEveryAddTakesTheSourcePair (devco D-488) walks every registered
// <noun>.add: an add whose table keeps source_kind and source_ref, or
// whose input takes --source-ref, takes both --source (json source) and
// --source-ref (json source_ref). (pii_pattern's source_kind says where
// a detection pattern is defined, not a provenance; its table keeps no
// source_ref, so it is not walked.) task add had neither; pattern and
// playbook add had only --source-ref. A new add cannot forget one.
func TestEveryAddTakesTheSourcePair(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range h.App.Registry().Entries() {
		noun, isAdd := strings.CutSuffix(e.Spec().Name, ".add")
		if !isAdd {
			continue
		}
		flags := map[string]string{}
		for _, f := range e.InputFields() {
			flags[f.Name] = f.Flag
		}
		keeps := false
		if table, ok := tableOfNoun(reg, noun); ok {
			if def, ok := reg.Table(table); ok {
				_, hasRef := def.Field("source_ref")
				_, hasKind := def.Field("source_kind")
				keeps = hasRef && hasKind
			}
		}
		_, takesRef := flags["source_ref"]
		if !keeps && !takesRef {
			continue
		}
		checked++
		if flags["source"] != "source" || flags["source_ref"] != "source-ref" {
			t.Errorf("%s: want --source (json source) and --source-ref (json source_ref), got source=%q source_ref=%q", e.Spec().Name, flags["source"], flags["source_ref"])
		}
	}
	if checked < 8 {
		t.Fatalf("walked %d adds that keep a source, want at least 8 (memory, rule, decision, hotfix, pattern, playbook, snapshot, task)", checked)
	}
}

// TestTaskAddKeepsItsSource (devco D-488): task add stores --source and
// --source-ref, and task show --json gives both back.
func TestTaskAddKeepsItsSource(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := h.ID(h.MustYes("task.add", `{"title":"triage the crash","source":"imported","source_ref":"https://tickets.example/T-7"}`))
	got := h.JSON(h.Must("task.show", `{"id":"`+id+`"}`))
	for _, want := range []string{`"source_kind":"imported"`, `"source_ref":"https://tickets.example/T-7"`} {
		if !strings.Contains(got, want) {
			t.Errorf("task show lacks %s:\n%s", want, got)
		}
	}
}
