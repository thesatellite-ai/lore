package wire_test

// lens_clientkey_test.go — later 9 (FB-TASK-4, TQ8): the time_spent view's
// client column follows time.client_key, given by the key's name or by its
// mky_ id; unset reads the "client" key.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/wire"
)

// lensSQL runs sql.run and returns its rows as "a|b" lines.
func lensSQL(t *testing.T, h *opstest.H, q string) []string {
	t.Helper()
	in, err := json.Marshal(opssql.RunIn{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	var out opssql.RunOut
	if err = json.Unmarshal([]byte(h.JSON(h.Must(opssql.OpRun, string(in)))), &out); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, len(out.Rows))
	for _, r := range out.Rows {
		parts := make([]string, 0, len(r.Cells))
		for _, c := range r.Cells {
			parts = append(parts, c.Value)
		}
		lines = append(lines, strings.Join(parts, "|"))
	}
	return lines
}

func TestTimeSpentClientKey(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := opstest.New(t, func(o *wire.Options) { // time data lives in internal stores only (PC-LEAK-1)
		o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
	})
	task := h.ID(h.Must(opstask.OpAdd, `{"title":"acme work","meta":["client=zed","customer=acme"]}`))
	h.Must(opstask.OpTimeLog, `{"id":"`+task+`","duration":"1h","at":"2026-11-02"}`)
	ids := lensSQL(t, h, `SELECT id FROM meta_keys WHERE name = 'customer'`)
	if len(ids) != 1 || !strings.HasPrefix(ids[0], "mky_") {
		t.Fatalf("customer key id: %q", ids)
	}
	q := `SELECT DISTINCT coalesce(client, '(none)') FROM time_spent`
	cases := []struct{ name, key, want string }{
		{"unset reads the client key", "", "zed"},
		{"by name", "customer", "acme"},
		{"by mky_ id", ids[0], "acme"},
		{"a key no task sets", "region", "(none)"},
		{"back to client by name", "client", "zed"},
	}
	for _, c := range cases {
		if c.key != "" {
			h.MustYes("policy.set", `{"key":"time.client_key","value":"`+c.key+`"}`)
		}
		if got := lensSQL(t, h, q); !slices.Equal(got, []string{c.want}) {
			t.Fatalf("%s: client = %q, want %q", c.name, got, c.want)
		}
	}
}
