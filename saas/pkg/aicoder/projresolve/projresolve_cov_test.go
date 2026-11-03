package projresolve

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saas/pkg/aicoder/lorecfg"
)

type refResolver map[string]string

func (r refResolver) Resolve(_ context.Context, ref string) (string, bool, error) {
	name, ok := strings.CutPrefix(ref, "ref://")
	if !ok {
		return "", false, nil
	}
	return r[name], true, nil
}

func TestPointerRefFieldsThroughLoad(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	body := "db_path: ref://db\nproject_id: prj_018f3b2c9a8e7891b4f51234567890ab\n"
	if err := os.WriteFile(filepath.Join(dir, ModeBFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	o := lorecfg.Options{Notices: &bytes.Buffer{}, Resolver: refResolver{"db": "/data/shared.db"}}
	loc, err := lorecfg.Locate(ctx, dir, ModeBBase, o)
	if err != nil {
		t.Fatal(err)
	}
	p, err := lorecfg.Load[Pointer](ctx, loc, o)
	if err != nil {
		t.Fatal(err)
	}
	if p.DBPath != "/data/shared.db" || p.ProjectID != "prj_018f3b2c9a8e7891b4f51234567890ab" {
		t.Fatalf("resolved: %+v", p)
	}
	var q Pointer
	if ps := q.RefFields(); len(ps) != 2 || ps[0] != &q.DBPath || ps[1] != &q.ProjectID {
		t.Fatal("RefFields points into the receiver")
	}
}

// Every marker file makes a folder a project; the legacy TOML counts too.
func TestProjectMarkerFiles(t *testing.T) {
	got := ProjectMarkerFiles()
	want := map[string]bool{ModeAFile: true, ModeBFile: true, ModeBLegacyFile: true}
	if len(got) != len(want) {
		t.Fatalf("markers %v", got)
	}
	for _, f := range got {
		if !want[f] {
			t.Fatalf("unexpected marker %q", f)
		}
	}
	got[0] = "mutated"
	if ProjectMarkerFiles()[0] == "mutated" {
		t.Fatal("callers get a fresh slice")
	}
	for _, f := range ProjectMarkerFiles() {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, MarkerDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, MarkerDir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := DetectMode(root); err == ErrNotProjectRoot {
			t.Errorf("%s alone must make a project root", f)
		}
	}
}
