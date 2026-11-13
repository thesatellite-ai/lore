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

// TestMigrateRunLeavesNoV1Silently (owner item T-2): a 0.1.17 repo holding
// a `_v: 1` row of every synced table (actors, projects and repos among
// them), an entity_tags link and a `_v: 1` _meta.json at the data root,
// migrated offline (`--offline --no-push`) and online (a bare remote):
// the dry run lists actors, projects and repos as rows to upgrade; after
// the run every file still at `_v: 1` is named in the run's Left with a
// reason (entity_tags and _meta.json, nothing else), its text says so,
// and a second run upgrades nothing and names the same files.
func TestMigrateRunLeavesNoV1Silently(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input, entityWhy string
		remote                 bool
	}{
		{"offline", `{"offline":true,"no_push":true}`, leftWhyEntityTagsOffline, false},
		{"online", `{}`, leftWhyEntityTags, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, files := fixtureRepo(t)
			repo := copyTree(t, files)
			idOf := fixtureIDs(fixtureTables(t))
			v1EntityTag(t, repo, string(schema.TableMemories), idOf[schema.TableMemories], idOf[schema.TableTags])
			meta := filepath.Join(repo, ".lore", "data", "_meta.json")
			if err := os.WriteFile(meta, []byte(`{"_v":1,"project_id":"`+idOf[schema.TableProjects]+`"}`), 0o600); err != nil {
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
			checkLeft(t, "dry run", dry, nil, tc.entityWhy)

			if raw, err = run(t, a, opsmigrate.OpRun, tc.input, true); err != nil {
				t.Fatal(err)
			}
			out := migrateOut(t, raw)
			checkLeft(t, "run", out, v1Files(t, repo), tc.entityWhy)
			if text := out.Text(); !strings.Contains(text, "Left 2 files at _v: 1:") || !strings.Contains(text, ".lore/data/_meta.json — ") {
				t.Errorf("run text does not name what it left:\n%s", text)
			}

			raw, err = run(t, a, opsmigrate.OpRun, tc.input, true)
			if err != nil {
				t.Fatal(err)
			}
			again := migrateOut(t, raw)
			if len(again.Rows) != 0 {
				t.Errorf("second run upgraded %v", again.Rows)
			}
			checkLeft(t, "second run", again, v1Files(t, repo), tc.entityWhy)
		})
	}
}

// checkLeft: out.Left names exactly the entity_tags link (with entityWhy)
// and the root _meta.json, and every file of onDisk (the `_v: 1` files
// after a real run; nil for a dry run) is among the files it names.
func checkLeft(t *testing.T, label string, out opsmigrate.RunOut, onDisk []string, entityWhy string) {
	t.Helper()
	named := map[string]bool{}
	var groups []string
	for _, l := range out.Left {
		groups = append(groups, l.What)
		if l.Why == "" || len(l.Files) == 0 {
			t.Errorf("%s: left group %+v has no files or no reason", label, l)
		}
		if l.What == entityTagsTable && l.Why != entityWhy {
			t.Errorf("%s: entity_tags left because %q, want %q", label, l.Why, entityWhy)
		}
		for _, f := range l.Files {
			named[f] = true
		}
	}
	if want := []string{entityTagsTable, ".lore/data/_meta.json"}; !slices.Equal(groups, want) {
		t.Errorf("%s: left %v, want %v (%+v)", label, groups, want, out.Left)
	}
	for _, f := range onDisk {
		if !named[f] {
			t.Errorf("%s: %s is at _v: 1 and the run does not say so (left: %+v)", label, f, out.Left)
		}
	}
}

// v1Files lists every file under repo/.lore/data whose "_v" is 1: a row
// as "table/id", a folder file as its slash path from repo.
func v1Files(t *testing.T, repo string) []string {
	t.Helper()
	var out []string
	for rel, b := range dataFiles(t, repo) {
		var doc struct {
			V json.RawMessage `json:"_v"`
		}
		if json.Unmarshal(b, &doc) != nil || string(doc.V) != "1" {
			continue
		}
		rel = filepath.ToSlash(rel)
		if parts := strings.Split(rel, "/"); len(parts) == 4 && !strings.HasPrefix(parts[3], "_") {
			rel = parts[2] + "/" + strings.TrimSuffix(parts[3], ".json")
		}
		out = append(out, rel)
	}
	slices.Sort(out)
	if len(out) == 0 {
		t.Fatal("no file at _v: 1: the fixture lost its entity_tags link and _meta.json")
	}
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
		{[]string{"entity_tags: 1 row at v1"}, false},
		{nil, false},
	} {
		if got := onlyLeftBefore(tc.pending); got != tc.want {
			t.Errorf("onlyLeftBefore(%v) = %v, want %v", tc.pending, got, tc.want)
		}
	}
}
