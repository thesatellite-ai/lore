package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

// TestMigrateRunWritesTheMigratorsActorRow (E-3): the entity_tags fold
// writes through the engine, which stamps the migrator as updated_by, so
// the run also writes the migrator's actor row into the repo store: right
// after `lore migrate run` every writer named in .lore/data has its
// actors file there (doctor's actor_roster check finds nothing).
func TestMigrateRunWritesTheMigratorsActorRow(t *testing.T) {
	t.Parallel()
	ctx := background()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{}) // a person with an identity: ensureActor writes their row
	withProject(t, a)
	prj := projectID(t, a)
	const tag = "tag_01a1a59b891c70b789144cd3e711fc11"
	v1Row(t, repo, schema.TableTags, tag, `"project_id":"`+prj+`","name":"deploy"`)
	v1Row(t, repo, schema.TableMemories, commitLineMemA, `"project_id":"`+prj+`","body":"one","kind":"core","source_kind":"manual"`)
	v1EntityTag(t, repo, "memories", commitLineMemA, tag)
	if _, err := opsmigrate.Run(ctx, a, opsmigrate.RunIn{Offline: true}); err != nil {
		t.Fatal(err)
	}
	if got := tagIDsOf(t, a, commitLineMemA); len(got) != 1 || got[0] != tag {
		t.Fatalf("fixture: the fold did not run: tag_ids %v", got)
	}
	data := filepath.Join(repo, ".lore", "data")
	checked := 0
	err := filepath.WalkDir(data, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var r struct {
			UpdatedBy string `json:"updated_by"`
		}
		// a v1 row with no writer upcasts to the zero actor: nobody to register
		if json.Unmarshal(b, &r) != nil || r.UpdatedBy == "" || strings.HasPrefix(r.UpdatedBy, "act_0000000000") {
			return nil
		}
		checked++
		if _, serr := os.Stat(filepath.Join(data, string(schema.TableActors), r.UpdatedBy+".json")); serr != nil {
			t.Errorf("%s names writer %s, which has no actors file in .lore/data", strings.TrimPrefix(p, data), r.UpdatedBy)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("fixture: no row names a writer")
	}
}
