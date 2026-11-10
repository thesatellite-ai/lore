package task

import (
	"regexp"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
)

// TestExampleReasonsAreCloseReasons: every task example that passes
// --reason passes a close reason --reason accepts (the machine sweep ran
// the cancel example, `--reason "not needed after the redesign"`, and lore
// refused its own example with E_INVALID_INPUT).
func TestExampleReasonsAreCloseReasons(t *testing.T) {
	reason := regexp.MustCompile(`--reason[ =]("[^"]*"|\S+)`)
	checked := 0
	for _, e := range Ops(nil) {
		for _, ex := range e.Spec().Examples {
			for _, m := range reason.FindAllStringSubmatch(ex.CLI, -1) {
				checked++
				if !slices.Contains(schema.TaskCloseReasons(), m[1]) {
					t.Errorf("%s example %q: --reason %s is not a close reason %v", e.Spec().Name, ex.CLI, m[1], schema.TaskCloseReasons())
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no task example passes --reason; the test reads nothing")
	}
}
