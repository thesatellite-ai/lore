package ask

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
	contextop "github.com/thesatellite-ai/lore/ops/context"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
)

// TestFileOf: the path a coding question names, and no path in a
// question about a store folder, a URL or a date.
func TestFileOf(t *testing.T) {
	t.Parallel()
	for q, want := range map[string]string{
		"add a due_date column to invoices in db/migrations/0042_invoice_due.sql": "db/migrations/0042_invoice_due.sql",
		"what applies to web/src/auth/Login.tsx?":                                 "web/src/auth/Login.tsx",
		"make internal/queue/ consumers retry":                                    "internal/queue/",
		"fix ./billing/tax.go.":                                                   "billing/tax.go",
		"what did we decide in acme/orbit":                                        "",
		"read https://example.com/a/b.html first":                                 "",
		"what happened on 2026-11-02":                                             "",
		"who is alice-ng":                                                         "",
	} {
		if got := fileOf(q); got != want {
			t.Errorf("fileOf(%q) = %q, want %q", q, got, want)
		}
	}
	// the path's pieces are not question words
	q := parseQuestion("set the issue date in billing/invoice.go", now)
	if slices.Contains(q.words, "billing") || slices.Contains(q.words, "invoice.go") || q.file != "billing/invoice.go" {
		t.Errorf("words %q file %q", q.words, q.file)
	}
}

// fileEnv is fakeEnv with a context host that answers --for-file.
type fileEnv struct {
	fakeEnv
	asked []contextop.FetchIn
}

func (f *fileEnv) AskLens(_ context.Context, _ bool, fn func(q Query) error) error {
	return fn(func(_ context.Context, s string) (opssql.Result, error) {
		f.stmts = append(f.stmts, s)
		txt := func(s string) value.Value { v, _ := value.TextOf(s); return v }
		if strings.Contains(s, "resources_all") {
			return opssql.Result{}, nil
		}
		return opssql.Result{Rows: [][]value.Value{
			// one shared word ("invoice"): not about the change
			{txt("row"), txt("memories"), txt("mem_font"), txt("repo"), txt(""), txt("Customers asked for a bigger invoice font."), txt("2026-11-01T00:00:00.000000000Z"), txt("0"), txt("0")},
			// the hotfix the context host also picks: printed once
			{txt("row"), txt("hotfixes"), txt("hfx_tz"), txt("repo"), txt("Invoices dated a day off"), txt("Use clock.UTC() for the issue date."), txt("2026-11-01T00:00:00.000000000Z"), txt("0"), txt("0")},
		}}, nil
	})
}

func (f *fileEnv) ContextHost(context.Context) (contextop.Host, error) { return f, nil }

func (f *fileEnv) Fetch(_ context.Context, in contextop.FetchIn, _ io.Reader) (contextop.Answer, error) {
	f.asked = append(f.asked, in)
	picks := []Applied{{ID: "rul_cents", Kind: "rule", Why: "glob billing/**"}, {ID: "hfx_tz", Kind: "hotfix", Why: "scope dir:billing"}}
	data, _ := json.Marshal(picks)
	return contextop.Answer{Data: data, Text: "# lore context for " + in.ForFile + "\n\n## Rules that apply here\n\n<rule id=\"rul_cents\" severity=\"must\" why=\"glob billing/**\">\nAmounts are int64 cents.\n</rule>\n\n## Hotfixes\n\n<hotfix id=\"hfx_tz\" severity=\"critical\" why=\"scope dir:billing\">\n**Invoices dated a day off**\n\nUse clock.UTC() for the issue date.\n</hotfix>\n"}, nil
}

// TestRunFileContext (devco D-815): a question naming a file answers with
// what lore context --for-file picks for it, first and cited; the word
// rows add nothing that holds a single question word, and a row the
// context printed is not printed again. Without a context host (a build
// without the machinery) the question is answered as before.
func TestRunFileContext(t *testing.T) {
	t.Parallel()
	env := &fileEnv{}
	out, err := run(context.Background(), env, RunIn{Query: "set the issue date when an invoice is finalized in billing/invoice.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(env.asked) != 1 || env.asked[0].ForFile != "billing/invoice.go" || !strings.HasPrefix(env.asked[0].ForPrompt, "set the issue date when an invoice is finalized in") || env.asked[0].MaxBytes != fileBudget(DefaultMaxChars) {
		t.Fatalf("context asked %+v", env.asked)
	}
	if out.File != "billing/invoice.go" || len(out.Applies) != 2 || !slices.Equal(out.Cited, []string{"rul_cents", "hfx_tz"}) {
		t.Fatalf("out: file %q applies %+v cited %q", out.File, out.Applies, out.Cited)
	}
	if len(out.Rows) != 0 {
		t.Fatalf("rows %+v: a one-word match or a row the context printed came back", out.Rows)
	}
	text := out.Text()
	if !strings.Contains(text, "applies to billing/invoice.go (lore context --for-file):") || strings.Count(text, "clock.UTC()") != 1 || strings.Contains(text, "nothing in the store answers") {
		t.Fatalf("text:\n%s", text)
	}
	// a build without the context host: no file part, no error
	plain, err := run(context.Background(), &fakeEnv{}, RunIn{Query: "fix rounding in billing/tax.go"})
	if err != nil || plain.File != "billing/tax.go" || len(plain.Applies) != 0 {
		t.Fatalf("without a host: %+v %v", plain, err)
	}
}
