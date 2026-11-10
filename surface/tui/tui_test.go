package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// fakeSource is two synced tables (memories with a sensitive field, policies)
// and one cache-only table, in one store; no mocks of any module path.
type fakeSource struct{ rows map[row.Table][]row.Row }

func (f fakeSource) Tables() []schema.TableDef {
	return []schema.TableDef{
		{Name: "policies", Synced: true, Description: "policies"},
		{Name: "memories", Synced: true, Description: "memories", Fields: []schema.FieldDef{{Name: "body"}, {Name: "token", Sensitive: true}}},
		{Name: "row_refs", Synced: false},
	}
}
func (f fakeSource) Stores() []store.Source { return []store.Source{"branch"} }
func (f fakeSource) List(_ context.Context, _ store.Source, t row.Table) ([]row.Row, error) {
	return f.rows[t], nil
}

func memRow(t *testing.T, body, token string) row.Row {
	t.Helper()
	id, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := value.TextOf(body)
	k, _ := value.TextOf(token)
	return row.Row{Table: "memories", ID: id.Any(), Fields: map[string]value.Value{"body": b, "token": k}}
}

func run(t *testing.T, src Source, opts Options, keys ...string) (string, error) {
	t.Helper()
	var out strings.Builder
	err := Run(context.Background(), src, strings.NewReader(strings.Join(keys, "\n")+"\n"), &out, opts)
	return out.String(), err
}

// The first screen lists only synced tables with live counts; 1 opens the
// first table, q quits from the rows screen without the tables screen again.
func TestTablesScreenOpenAndQuit(t *testing.T) {
	src := fakeSource{rows: map[row.Table][]row.Row{"memories": {memRow(t, "invoice export times out", "s3cr3t")}}}
	out, err := run(t, src, Options{}, "1", "q")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "lore tables") || !strings.Contains(out, "memories") || strings.Contains(out, "row_refs") {
		t.Fatalf("tables screen:\n%s", out)
	}
	if !strings.Contains(out, "memories — 1–1 of 1") || !strings.Contains(out, "invoice export times out") {
		t.Fatalf("rows screen:\n%s", out)
	}
	if strings.Count(out, "lore tables") != 1 {
		t.Fatalf("q must quit, not go back:\n%s", out)
	}
}

// --kind opens on that table by its singular; the row screen masks the
// sensitive field; b goes back to the tables screen; q there quits.
func TestKindOpensFirstAndMasks(t *testing.T) {
	src := fakeSource{rows: map[row.Table][]row.Row{"memories": {memRow(t, "hello", "s3cr3t")}}}
	out, err := run(t, src, Options{Kind: "memory"}, "1", "b", "b", "q")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "memories —") {
		t.Fatalf("must open on memories:\n%s", out)
	}
	if strings.Contains(out, "s3cr3t") || !strings.Contains(out, "token: "+masked) {
		t.Fatalf("sensitive field not masked:\n%s", out)
	}
	if !strings.Contains(out, "lore tables") {
		t.Fatalf("b from the rows screen must show the tables screen:\n%s", out)
	}
}

// Kind spellings: table name, singular, -y → -ies; unknown is E_INVALID_INPUT.
func TestResolveKind(t *testing.T) {
	defs := []schema.TableDef{{Name: "memories"}, {Name: "policies"}, {Name: "decisions"}}
	for kind, want := range map[string]row.Table{"memory": "memories", "memories": "memories", "Policy": "policies", "decision": "decisions"} {
		d, err := resolve(defs, kind)
		if err != nil || d.Name != want {
			t.Errorf("resolve(%q) = %q, %v; want %q", kind, d.Name, err, want)
		}
	}
	_, err := resolve(defs, "nope")
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.InvalidInput || !strings.Contains(e.Message, "memories") {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := run(t, fakeSource{}, Options{Kind: "nope"}); err == nil {
		t.Fatal("Run with an unknown kind must fail before showing anything")
	}
}

// EOF on any screen ends cleanly.
func TestEOFEnds(t *testing.T) {
	var out strings.Builder
	if err := Run(context.Background(), fakeSource{}, strings.NewReader(""), &out, Options{Kind: "policies"}); err != nil {
		t.Fatal(err)
	}
}
