package wire

// folderproject_order_test.go — devco:D-745: the first write into a fresh
// `--to <store>/<folder>` folder names its files in one fixed order, the
// writer's actor row, then the folder's project row, then the row itself,
// for a person and for an agent alike. D-740 moved the folder's project
// row from a commit of its own (made before the write) into the write's
// batch, after the writer's actor row (D-694: an agent's actor row must be
// in place before the project row that states it as the writer), so the
// first `lore memory add --to notes/<folder>` printed "lore: wrote
// …/actors/…" before "…/projects/…" and one commit fewer was pushed; the
// docs transcripts still showed the old order and count. This test pins
// the order the pages show, so a change to it fails here, in the quick
// gate, and not only in the docs example run.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestFolderWriteNamesActorThenProjectThenRow: one write into a `--to`
// folder with no project row yet stages the writer's actor row, the
// folder's project row and the row in that order, and the write lines
// name them in it, as a person and as an agent.
func TestFolderWriteNamesActorThenProjectThenRow(t *testing.T) {
	for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_REMOTE_SESSION_ID", "CODEX_THREAD_ID", "LORE_SESSION"} {
		t.Setenv(k, "")
	}
	for _, who := range []struct{ name, actor, human string }{
		{"person", "", "1"},
		{"agent", "agent:bot", ""},
	} {
		t.Run(who.name, func(t *testing.T) {
			t.Setenv("LORE_ACTOR", who.actor)
			t.Setenv("LORE_HUMAN", who.human)
			tables := firstFolderWriteTables(t)
			first := func(table string) int {
				for i, tb := range tables {
					if tb == table {
						return i
					}
				}
				return -1
			}
			act, p, mem := first("actors"), first("projects"), first("memories")
			if act < 0 || p < 0 || mem < 0 || act > p || p > mem {
				t.Fatalf("[devco:D-745] as a %s the first write into the api folder wrote %s, want the writer's actor row, then the folder's project row, then the memory (the order the docs transcripts show)", who.name, strings.Join(tables, ","))
			}
		})
	}
}

// firstFolderWriteTables writes one memory into a fresh api folder (its
// _meta.json names a project id, no project row exists yet) and returns
// the table of each file the write names in that folder, in order.
func firstFolderWriteTables(t *testing.T) []string {
	t.Helper()
	repo, storeDir, yamlPath, yaml := folderStoreRepo(t)
	if err := os.MkdirAll(filepath.Join(storeDir, "api"), 0o750); err != nil {
		t.Fatal(err)
	}
	folderID, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "api", rowfs.MetaFileName), []byte(`{"_v":1,"project_id":"`+folderID.String()+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	yaml += "  - name: northwind/api\n    url: file://" + filepath.ToSlash(storeDir) + "\n    folder: api\n"
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{RepoDir: repo, Home: t.TempDir(), RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	withProject(t, a)
	api := store.Source("northwind/api")
	ctx, l := WithWriteLog(background())
	ctx = context.WithValue(ctx, newFolderKey{}, newFolder{target: api, folder: "api"})
	ref, err := value.RefOf(folderID.Any())
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{repoProjectField: ref}
	for k, v := range map[string]string{"body": "api answers in json", "kind": schema.MemoryKindCore, "source_kind": "manual"} {
		tv, terr := value.TextOf(v)
		if terr != nil {
			t.Fatal(terr)
		}
		fields[k] = tv
	}
	if _, err := a.Create(ctx, api, schema.TableMemories, fields); err != nil {
		t.Fatalf("a write into the api folder: %v", err)
	}
	var tables []string
	for _, w := range l.Files() {
		if w.Store != string(api) || w.File == "" || w.extra {
			continue
		}
		tables = append(tables, filepath.Base(filepath.Dir(w.File)))
	}
	return tables
}
