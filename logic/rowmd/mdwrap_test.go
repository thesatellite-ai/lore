package rowmd

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/mdwrap"
	"github.com/thesatellite-ai/lore/model/row"
)

// TestViewNotHardWrapped: the Markdown view `lore edit` opens, for every
// synced table, full and empty rows, keeps one physical line per paragraph
// outside the frontmatter. The fixture text values are one line, so any
// continued line is lore's own text.
func TestViewNotHardWrapped(t *testing.T) {
	for _, def := range tables() {
		for name, r := range map[string]row.Row{"full": fullRow(t, def), "empty": stored(t, def, nil)} {
			if ps := mdwrap.Check(string(render(t, def, r))); len(ps) > 0 {
				t.Errorf("%s %s row view is hard-wrapped:\n%s", def.Name, name, mdwrap.Lines(ps))
			}
		}
	}
}
