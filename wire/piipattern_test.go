package wire

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	opspii "github.com/thesatellite-ai/lore/ops/piipattern"
	"github.com/thesatellite-ai/lore/schema"
)

// v017PIIDDL is the v0.1.17 ent pii_patterns table (dbent/schema/pii_pattern.go).
const v017PIIDDL = `CREATE TABLE pii_patterns (id TEXT PRIMARY KEY, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, name TEXT NOT NULL, regex TEXT NOT NULL, enabled BOOL NOT NULL DEFAULT false, source_kind TEXT NOT NULL)`

// v0.1.17 pii pattern ids (prefix pip + 32 hex).
const (
	piiRowA = "pip_0190e6a0c7d27b4c8a1e2f3a4b5c6d70"
	piiRowB = "pip_0190e6a0c7d27b4c8a1e2f3a4b5c6d71"
)

// listPII runs pii_pattern.list on env and returns its JSON output and text.
func listPII(t *testing.T, env opspii.Env) (opspii.ListOut, string) {
	t.Helper()
	var e op.Entry
	for _, x := range opspii.Ops(env) {
		if x.Spec().Name == opspii.OpList {
			e = x
		}
	}
	in, err := e.Decode(op.JSONDecoder{Body: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Invoke(context.Background(), in)
	if err != nil {
		t.Fatalf("pii_pattern.list: %v", err)
	}
	b, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var out opspii.ListOut
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, out.Text()
}

// TestPIIPatternsMachineLocal (P3-PII-1): pii_patterns is a built-in
// machine-local table; `lore migrate run`'s machine_local copy brings the
// v0.1.17 rows over by column name with no pii-specific code, and
// `pii-pattern list` reads them through the engine, by name, with the
// v0.1.17 on/off text. Before the copy the list is empty, never an error;
// a rerun copies nothing twice.
func TestPIIPatternsMachineLocal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	found := false
	for _, d := range schema.Builtin() {
		if d.Name == opspii.Table {
			found = d.MachineLocal
		}
	}
	if !found {
		t.Fatalf("%s is not a machine-local built-in table (schema patch P3-PII-1)", opspii.Table)
	}
	eng := newCopyEngineOver(t, t.TempDir(), schema.Builtin())

	if out, text := listPII(t, eng); out.Count != 0 || out.Data == nil || text != "(no patterns)\n" {
		t.Fatalf("empty list = %+v %q", out, text)
	}

	dbPath := filepath.Join(t.TempDir(), "lore.db")
	writeV017DB(t, dbPath, v017PIIDDL,
		`INSERT INTO pii_patterns VALUES ('`+piiRowA+`', '2025-05-01 10:00:00.5+00:00', '2025-05-01 10:00:00.5+00:00', 'zeta_host', 'hub-[0-9]+', 0, 'user')`,
		`INSERT INTO pii_patterns VALUES ('`+piiRowB+`', '2025-05-02 10:00:00+00:00', '2025-05-02 10:00:00+00:00', 'alpha_key', 'AK-[0-9]{6}', 1, 'team')`)
	steps, n, err := copyMachineLocal(ctx, eng, dbPath, false)
	if err != nil || n != 2 {
		t.Fatalf("copy = %q, %d, %v; want 2 rows", steps, n, err)
	}

	out, text := listPII(t, eng)
	if out.Count != 2 || out.Kind != "pii-pattern.list" || out.SchemaVersion != 1 {
		t.Fatalf("envelope = %+v", out)
	}
	a, z := out.Data[0], out.Data[1]
	if a.ID != piiRowB || a.Name != "alpha_key" || a.Regex != "AK-[0-9]{6}" || !a.Enabled || a.SourceKind != "team" || a.CreatedAt == "" {
		t.Errorf("first = %+v", a)
	}
	if z.ID != piiRowA || z.Name != "zeta_host" || z.Enabled || z.SourceKind != "user" {
		t.Errorf("second = %+v", z)
	}
	if want := "[on] alpha_key  (team)\n[off] zeta_host  (user)\n"; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}

	if _, n, err = copyMachineLocal(ctx, eng, dbPath, false); err != nil || n != 0 {
		t.Fatalf("rerun copied %d, %v", n, err)
	}
}
