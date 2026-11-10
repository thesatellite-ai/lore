package formatgate

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
)

// TestStepsFeaturesKnown: every format step's feature (and table feature)
// is a known feature with a parseable Since (FORMAT_EVOLUTION rule 3), and
// the table names this package pins match the step's.
func TestStepsFeaturesKnown(t *testing.T) {
	for _, s := range row.Steps() {
		names := []string{s.Feature}
		for tb, f := range s.TableFeatures {
			if tb.String() != TablePlans {
				t.Errorf("step %d: table feature on %s", s.To, tb)
			}
			names = append(names, f)
		}
		for _, n := range names {
			f, ok := Lookup(n)
			if !ok {
				t.Errorf("step %d: feature %q not in Known", s.To, n)
				continue
			}
			if _, ok := versionCore(f.Since); !ok {
				t.Errorf("feature %q: bad Since %q", n, f.Since)
			}
			if strings.HasPrefix(n, pluginPrefix) && s.Tables == nil {
				t.Errorf("step %d: a plugin feature must list its own tables", s.To)
			}
		}
	}
}
