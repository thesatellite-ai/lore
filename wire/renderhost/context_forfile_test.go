package renderhost

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestContextForFileNoteWords (devco D-830): `lore context --for-file`
// prints a note holding every meaningful word of the path (D-815) where
// a monorepo's container folder is no such word (apps/app1/main.go finds
// the app1 note), and a note naming only one of two path words stays out
// (the billing team's lunch, a bigger font on the invoice header).
func TestContextForFileNoteWords(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	ctx := context.Background()
	a, project := repoApp(t)
	mem := func(body string) string {
		t.Helper()
		w, err := a.Create(ctx, store.SourceRepo, schema.TableMemories, map[string]value.Value{
			"project_id": text(t, project), "source_kind": text(t, "manual"),
			"kind": text(t, schema.MemoryKindCore), "body": text(t, body)})
		if err != nil {
			t.Fatal(err)
		}
		return w.Row.ID.String()
	}
	invoice := mem("billing rounds invoice totals per line, never per invoice")
	app1 := mem("app1 runs locally on port 7400")
	lunch := mem("The billing team has lunch together on Thursdays.")
	font := mem("Customers asked for a bigger font on the invoice header.")
	h := New(Options{App: a})
	for _, c := range []struct {
		file        string
		want, never []string
	}{
		{"billing/invoice.go", []string{invoice}, []string{lunch, font, app1}},
		{"apps/app1/main.go", []string{app1}, []string{invoice, lunch, font}},
		{"packages/app1/index.ts", []string{app1}, []string{invoice, lunch, font}},
	} {
		f := contextFlags{forFile: c.file}
		ans, err := h.contextAnswer(ctx, &f, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := picksOf(t, ans.Data)
		for _, id := range c.want {
			if _, ok := got[id]; !ok {
				t.Errorf("--for-file %s misses %s:\n%s", c.file, id, ans.Text)
			}
		}
		for _, id := range c.never {
			if _, ok := got[id]; ok {
				t.Errorf("--for-file %s picks %s:\n%s", c.file, id, ans.Text)
			}
		}
	}
}
