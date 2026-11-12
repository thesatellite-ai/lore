package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/mdwrap"
	"github.com/thesatellite-ai/lore/surface/docs"
)

// TestGeneratedDocsNotHardWrapped: the command and error tables `lore spec
// docs` writes from the real registry, and a markdown file with both
// blocks rendered in, keep one physical line per paragraph and table row.
func TestGeneratedDocsNotHardWrapped(t *testing.T) {
	t.Parallel()
	a := newTestApp(t, t.TempDir())
	src := "# Reference\n\nThe commands.\n\n<!-- lore:spec:begin commands -->\n<!-- lore:spec:end commands -->\n\n" +
		"The errors.\n\n<!-- lore:spec:begin errors -->\n<!-- lore:spec:end errors -->\n"
	rendered, err := docs.Render(a.Registry(), "ref.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]string{
		"commands table": docs.CommandsMarkdown(a.Registry()),
		"errors table":   docs.ErrorsMarkdown(),
		"rendered file":  string(rendered),
	} {
		if ps := mdwrap.Check(s); len(ps) > 0 {
			t.Errorf("%s is hard-wrapped:\n%s", name, mdwrap.Lines(ps))
		}
	}
	if len(rendered) <= len(src) {
		t.Fatalf("blocks not rendered:\n%s", rendered)
	}
}
