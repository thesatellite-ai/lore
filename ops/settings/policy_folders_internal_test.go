package settings

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// polEnv is a levelEnv whose stores hold the given policies rows.
type polEnv struct {
	levelEnv
	rows map[store.Source][]row.Row
}

func (e polEnv) List(_ context.Context, src store.Source, table row.Table) ([]row.Row, error) {
	if table != schema.TablePolicies {
		return nil, nil
	}
	return e.rows[src], nil
}

// polRow is a live policies row setting key to v.
func polRow(t *testing.T, key, v string) row.Row {
	t.Helper()
	id, err := ids.New[ids.Policy]()
	if err != nil {
		t.Fatal(err)
	}
	val, err := inferText(v)
	if err != nil {
		t.Fatal(err)
	}
	k, err := value.TextOf(key)
	if err != nil {
		t.Fatal(err)
	}
	return row.Row{Table: schema.TablePolicies, ID: id.Any(), Fields: map[string]value.Value{pKey: k, pValue: val}}
}

// TestPolicyShowSeveralFolders (devco:D-701): `policy show --setup` from
// a plain folder reads every folder of the setup; it showed one folder's
// value (22000 of kb/sync) as the effective one though kb/web set 23000.
// Each folder that sets a key now prints its own line, the value of the
// levels above prints once for the other folders, and a read of one
// folder keeps the single line.
func TestPolicyShowSeveralFolders(t *testing.T) {
	t.Parallel()
	const key = "context.budget.internal"
	levels := map[store.Source]policy.Level{"kb": policy.LevelStoreRoot, "kb/org": policy.LevelOrg,
		"kb/sync": policy.LevelFolder, "kb/web": policy.LevelFolder, "kb/docs": policy.LevelFolder}
	rows := map[store.Source][]row.Row{"kb/sync": {polRow(t, key, "22000")}, "kb/web": {polRow(t, key, "23000")}}
	show := func(stores ...store.Source) string {
		env := polEnv{levelEnv: levelEnv{nameEnv: nameEnv{stores: stores}, levels: levels}, rows: rows}
		out, err := ops{env: env}.policyShow(context.Background(), PolicyShowIn{Key: key})
		if err != nil {
			t.Fatal(err)
		}
		return out.Text()
	}
	got := show("kb", "kb/org", "kb/sync", "kb/web", "kb/docs")
	for _, want := range []string{key + "\t22000\tfolder kb/sync (pol_", key + "\t23000\tfolder kb/web (pol_", "\tdefault" + otherFoldersNote} {
		if !strings.Contains(got, want) {
			t.Errorf("several folders: no %q in\n%s", want, got)
		}
	}
	if n := strings.Count(got, key); n != 3 {
		t.Errorf("several folders: %d lines, want 3 (sync, web, the others)\n%s", n, got)
	}
	// every read folder sets it: no line for "the others"
	if got := show("kb", "kb/sync", "kb/web"); strings.Contains(got, otherFoldersNote) || strings.Count(got, key) != 2 {
		t.Errorf("two folders, both set: want two lines\n%s", got)
	}
	// one folder read (a linked repo): its value, one line, no note
	if got := show("kb", "kb/org", "kb/sync"); strings.Count(got, key) != 1 || !strings.Contains(got, "22000\tfolder kb/sync") || strings.Contains(got, otherFoldersNote) {
		t.Errorf("one folder: want its single line\n%s", got)
	}
}
