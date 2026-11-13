package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

// TestMigrateRunLeavesNoV1Silently (owner items T-2, M-1, M-2): a 0.1.17
// repo holding a `_v: 1` row of every synced table (actors, projects and
// repos among them), entity_tags links (a memory and the project tagged)
// and a `_v: 1` _meta.json at the data root, migrated offline
// (`--offline --no-push`) and online (a bare remote): the dry run lists
// actors, projects, repos and entity_tags as rows to upgrade; after the
// run no row file is at `_v: 1` (entity_tags included, M-1), the links are
// folded into tag_ids (offline too), the run leaves nothing (the folder
// file is not a leftover, M-2) and says nothing about `_v: 1`, and a
// second run upgrades nothing.
func TestMigrateRunLeavesNoV1Silently(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input string
		remote      bool
	}{
		{"offline", `{"offline":true,"no_push":true}`, false},
		{"online", `{}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, files := fixtureRepo(t)
			repo := copyTree(t, files)
			idOf := fixtureIDs(fixtureTables(t))
			tag, project := idOf[schema.TableTags], idOf[schema.TableProjects]
			v1EntityTag(t, repo, string(schema.TableMemories), idOf[schema.TableMemories], tag)
			v1EntityTag(t, repo, string(schema.TableProjects), project, tag)
			meta := filepath.Join(repo, ".lore", "data", "_meta.json")
			if err := os.WriteFile(meta, []byte(`{"_v":1,"project_id":"`+project+`"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.remote {
				bare := filepath.Join(t.TempDir(), "origin.git")
				gitIn(t, repo, nil, "init", "-q", "--bare", bare)
				gitIn(t, repo, nil, "remote", "add", "origin", bare)
			}
			a, err := New(Options{RepoDir: repo, Home: srcHome(t, "identity:\n  key: human:pat@example.com\n  name: Pat\n")})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			if len(v1Rows(t, repo)) == 0 {
				t.Fatal("fixture: no row file at _v: 1")
			}

			dryIn := strings.TrimSuffix(tc.input, "}")
			if dryIn != "{" {
				dryIn += ","
			}
			raw, err := run(t, a, opsmigrate.OpRun, dryIn+`"dry_run":true}`, true)
			if err != nil {
				t.Fatal(err)
			}
			dry := migrateOut(t, raw)
			for _, table := range []row.Table{schema.TableActors, schema.TableProjects, schema.TableRepos} {
				if want := table.String() + "/" + idOf[table]; !slices.Contains(dry.Rows, want) {
					t.Errorf("dry run does not upgrade %s: %v", want, dry.Rows)
				}
			}
			if n := countPrefix(dry.Rows, entityTagsTable+"/"); n != 2 {
				t.Errorf("dry run upgrades %d entity_tags rows, want 2: %v", n, dry.Rows)
			}
			checkNothingLeft(t, "dry run", dry)

			if raw, err = run(t, a, opsmigrate.OpRun, tc.input, true); err != nil {
				t.Fatal(err)
			}
			out := migrateOut(t, raw)
			checkNothingLeft(t, "run", out)
			if left := v1Rows(t, repo); len(left) > 0 {
				t.Errorf("row files still at _v: 1 after the run: %v", left)
			}
			if got := tagIDsOf(t, a, project); !slices.Contains(got, tag) {
				t.Errorf("project tag_ids = %v, want the entity_tags link %s folded in", got, tag)
			}

			raw, err = run(t, a, opsmigrate.OpRun, tc.input, true)
			if err != nil {
				t.Fatal(err)
			}
			again := migrateOut(t, raw)
			if len(again.Rows) != 0 {
				t.Errorf("second run upgraded %v", again.Rows)
			}
			checkNothingLeft(t, "second run", again)
		})
	}
}

// checkNothingLeft: out leaves no file at `_v: 1` and its text never says
// so (M-2: a folder file is not a leftover).
func checkNothingLeft(t *testing.T, label string, out opsmigrate.RunOut) {
	t.Helper()
	if len(out.Left) > 0 {
		t.Errorf("%s: left %+v, want nothing", label, out.Left)
	}
	if text := out.Text(); strings.Contains(text, "at _v: 1") {
		t.Errorf("%s: text names files at _v: 1:\n%s", label, text)
	}
}

// countPrefix counts the lines of rows that start with prefix.
func countPrefix(rows []string, prefix string) int {
	n := 0
	for _, r := range rows {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// v1Rows lists every row file under repo/.lore/data (any table, folder
// files excluded) whose "_v" is 1, as "table/id".
func v1Rows(t *testing.T, repo string) []string {
	t.Helper()
	var out []string
	for rel, b := range dataFiles(t, repo) {
		var doc struct {
			V json.RawMessage `json:"_v"`
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 4 || strings.HasPrefix(parts[3], "_") || json.Unmarshal(b, &doc) != nil || string(doc.V) != "1" {
			continue
		}
		out = append(out, parts[2]+"/"+strings.TrimSuffix(parts[3], ".json"))
	}
	slices.Sort(out)
	return out
}

// TestOnlyLeftBefore: in a migrated repo, v1 rows only of tables a run
// before T-2 left (actors, projects, repos) are the earlier run's, a note;
// any core table, a live-ref or lore.db line, or no line at all is not.
func TestOnlyLeftBefore(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		pending []string
		want    bool
	}{
		{[]string{"actors: 8 rows at v1", "projects: 1 row at v1", "repos: 1 row at v1"}, true},
		{[]string{"actors: 8 rows at v1", "memories: 1 row at v1"}, false},
		{[]string{"live ref of repo: 2 rows not in its row files yet"}, false},
		{[]string{"entity_tags: 169 rows at v1"}, true}, // M-1: a run before it left the links
		{nil, false},
	} {
		if got := onlyLeftBefore(tc.pending); got != tc.want {
			t.Errorf("onlyLeftBefore(%v) = %v, want %v", tc.pending, got, tc.want)
		}
	}
}
