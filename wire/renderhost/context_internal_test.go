package renderhost

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// Store ids of the --internal test (arbitrary sto_ ids).
const (
	internalTestShareID = "sto_01a1a5aca7f57659be7320c5f4b700a1"
	internalTestInnerID = "sto_01a1a5aca7f57659be7320c5f4b700a2"
)

// internalStoreAt makes a folder store with the given visibility.
func internalStoreAt(t *testing.T, id string, vis rowfs.Visibility) string {
	t.Helper()
	dir := t.TempDir()
	sid, err := ids.Parse[ids.Store](id)
	if err != nil {
		t.Fatal(err)
	}
	if err = folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: vis}); err != nil {
		t.Fatal(err)
	}
	return "file://" + filepath.ToSlash(dir)
}

// internalApp is an App over a fresh git repo with a shareable store
// "share" and an internal store "inner", each holding one memory, plus
// one memory in the repo store; the marker word is in every body.
func internalApp(t *testing.T) *wire.App {
	t.Helper()
	ctx := context.Background()
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	yaml := "stores:\n  - name: share\n    url: " + internalStoreAt(t, internalTestShareID, rowfs.VisibilityShareable) +
		"\n  - name: inner\n    url: " + internalStoreAt(t, internalTestInnerID, rowfs.VisibilityInternal) + "\n"
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(attach.FileName)), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := wire.New(wire.Options{RepoDir: repo, Home: t.TempDir(), FlagActor: "human:alice@example.com",
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	text := func(s string) value.Value {
		v, terr := value.TextOf(s)
		if terr != nil {
			t.Fatal(terr)
		}
		return v
	}
	for _, target := range []store.Source{store.SourceRepo, "share", "inner"} {
		p, cerr := a.Create(ctx, target, schema.TableProjects, map[string]value.Value{"name": text("p-" + string(target))})
		if cerr != nil {
			t.Fatal(cerr)
		}
		if _, cerr = a.Create(ctx, target, schema.TableMemories, map[string]value.Value{
			"project_id": text(p.Row.ID.String()), "kind": text(schema.MemoryKindCore),
			"body": text("zebra note in " + string(target)), "source_kind": text("manual"),
		}); cerr != nil {
			t.Fatal(cerr)
		}
	}
	return a
}

// TestCoreExternal (P3-CMDLORE2, `lore context --internal` on cmd/lore):
// only the internal store's rows print (never the repo's, never a
// shareable store's, never a project row); the text filter and --table
// narrow them; a table outside the private render's set is refused; the
// JSON is the flat row object with id, table, _source and _label.
func TestCoreExternal(t *testing.T) {
	ctx := context.Background()
	a := internalApp(t)
	ext := CoreExternal(a)

	cases := []struct {
		name, table, text string
		want              int
	}{
		{"all", "", "", 1},
		{"text hit", "", "ZEBRA", 1},
		{"text miss", "", "giraffe", 0},
		{"table memories", string(schema.TableMemories), "", 1},
		{"table rules", string(schema.TableRules), "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b strings.Builder
			n, data, err := ext(ctx, &b, c.table, c.text)
			if err != nil {
				t.Fatal(err)
			}
			if n != c.want || strings.Count(b.String(), "\n") != c.want {
				t.Fatalf("n=%d lines=%q, want %d", n, b.String(), c.want)
			}
			var got []map[string]json.RawMessage
			if err := json.Unmarshal(data, &got); err != nil || len(got) != c.want {
				t.Fatalf("json %s: %v (len %d)", data, err, len(got))
			}
			if c.want == 0 {
				if string(data) != "[]" {
					t.Fatalf("empty json = %s, want []", data)
				}
				return
			}
			line := b.String()
			if !strings.Contains(line, "zebra note in inner") || !strings.Contains(line, "[inner]") ||
				strings.Contains(line, "in share") || strings.Contains(line, "in repo") {
				t.Fatalf("line = %q", line)
			}
			o := got[0]
			var src struct {
				Store   string `json:"store"`
				StoreID string `json:"store_id"`
			}
			if err := json.Unmarshal(o["_source"], &src); err != nil || src.Store != "inner" || src.StoreID != internalTestInnerID {
				t.Fatalf("_source = %s (%v)", o["_source"], err)
			}
			if string(o["_label"]) != `"[inner]"` || string(o["table"]) != `"memories"` ||
				!strings.Contains(string(o["body"]), "zebra note in inner") || !strings.HasPrefix(string(o["id"]), `"mem_`) {
				t.Fatalf("row object = %v", o)
			}
		})
	}

	var b strings.Builder
	_, _, err := ext(ctx, &b, string(schema.TableResources), "")
	if errs.CodeOf(err) != errs.InvalidInput {
		t.Fatalf("--table resources: err = %v, want E_INVALID_INPUT", err)
	}
}
