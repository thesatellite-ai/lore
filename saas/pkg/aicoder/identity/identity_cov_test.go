package identity

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saas/pkg/aicoder/lorecfg"
)

// refResolver resolves "ref://<name>" from a map; other values are left
// alone (handled=false), like a resolver for one secret-store scheme.
type refResolver map[string]string

func (r refResolver) Resolve(_ context.Context, ref string) (string, bool, error) {
	name, ok := strings.CutPrefix(ref, "ref://")
	if !ok {
		return "", false, nil
	}
	return r[name], true, nil
}

// RefFields lists both identity values, so a resolver can fill either from
// a reference, and leaves literal values alone.
func TestFileRefFieldsThroughLoad(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	body := "display_name: ref://name\nstable_key: human:alice@agency.example\n"
	if err := os.WriteFile(filepath.Join(dir, FileBase+lorecfg.ExtYAML), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	o := lorecfg.Options{Notices: &bytes.Buffer{}, Resolver: refResolver{"name": "Alice"}}
	loc, err := lorecfg.Locate(ctx, dir, FileBase, o)
	if err != nil {
		t.Fatal(err)
	}
	f, err := lorecfg.Load[File](ctx, loc, o)
	if err != nil {
		t.Fatal(err)
	}
	if f.DisplayName != "Alice" || f.StableKey != "human:alice@agency.example" {
		t.Fatalf("resolved: %+v", f)
	}
	var g File
	ps := g.RefFields()
	if len(ps) != 2 || ps[0] != &g.DisplayName || ps[1] != &g.StableKey {
		t.Fatal("RefFields points into the receiver, both fields")
	}
}
