package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceLintFlagsUsualFieldGap (devco D-490): a domain kind whose
// renews_on is not required and has no remind_before; three of four
// domains set it. Lint named no problem before; now it flags the one gap
// with the row id, the counts and a hint with named flags, and stays quiet
// on a field only some rows use.
func TestResourceLintFlagsUsualFieldGap(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"domain"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"domain","field":"renews_on","type":"date"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"domain","field":"notes","type":"text"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"domain","name":"app1_test","set":["renews_on=2999-01-01","notes=x"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"domain","name":"app2_test","set":["renews_on=2999-02-01"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"domain","name":"app3_test","set":["renews_on=2999-03-01"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"domain","name":"app4_test"}`)
	text := opstest.Text(h.MustYes(settings.OpResLint, `{}`))
	if strings.Contains(text, "no problems found") || !strings.Contains(text, "missing_value") || !strings.Contains(text, "domain app4_test.renews_on") ||
		!strings.Contains(text, "3 of 4 domain rows set it") || !strings.Contains(text, "--id app4_test --set renews_on=") || !strings.Contains(text, "[res_") {
		t.Errorf("lint must flag the empty renews_on of app4_test with its id:\n%s", text)
	}
	if strings.Contains(text, ".notes") || strings.Count(text, "missing_value") != 1 {
		t.Errorf("a field one row of four sets is not a gap:\n%s", text)
	}
}
