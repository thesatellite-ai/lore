package wire

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

// Ids of the v0.1.17 roster rows TestMigrateOrgV1RosterRows writes.
const (
	v1RosterActor = "act_01a1a5aca7f57659be7320c5f4b7a001"
	v1RosterTeam  = "tem_01a1a5aca7f57659be7320c5f4b7a002"
)

// TestMigrateOrgV1RosterRows (orgcore migrateOrg, later 288.3): a v0.1.17
// org store (v1 marker) holding `_v: 1` actor and team rows: `migrate run
// --dry-run` lists one roster step per table and leaves both files as they
// were; the run rewrites them `_v: 2` with the same ids and the fields
// kept; a second run lists no roster step and leaves the files as the
// first run wrote them.
func TestMigrateOrgV1RosterRows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeV1Marker(t, dir)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: "agency", URL: fileuri.URL(dir, "")}}, nil
	}
	opts := Options{OrgStores: hook, RepoIsClient: func(context.Context) (bool, error) { return false, nil }}
	a := openWith(t, gitRepo(t), opts)
	withProject(t, a)
	prj := projectID(t, a)
	v1Row(t, dir, schema.TableActors, v1RosterActor, `"kind":"human","display_name":"Alice Rivera","handle":"alice"`)
	v1Row(t, dir, schema.TableTeams, v1RosterTeam, `"project_id":"`+prj+`","handle":"backend","`+schema.FieldTeamMembers+`":["`+v1RosterActor+`"]`)
	files := map[string]string{
		schema.TableActors: filepath.Join(dir, ".lore", "data", schema.TableActors, v1RosterActor+".json"),
		schema.TableTeams:  filepath.Join(dir, ".lore", "data", schema.TableTeams, v1RosterTeam+".json"),
	}
	read := func() map[string]string {
		out := map[string]string{}
		for k, p := range files {
			out[k] = string(mustRead(t, p))
		}
		return out
	}
	rosterSteps := func(steps []string) int {
		n := 0
		for _, s := range steps {
			if strings.HasPrefix(s, "org store agency:") && strings.Contains(s, "row(s) _v: 1 → _v: 2") {
				n++
			}
		}
		return n
	}
	before := read()
	out, err := run(t, a, opsmigrate.OpRun, `{"dry_run":true}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if n := rosterSteps(migrateOut(t, out).Steps); n != 2 {
		t.Fatalf("dry run lists %d roster steps, want 2: %s", n, out)
	}
	for k, v := range read() {
		if v != before[k] {
			t.Fatalf("a dry run rewrote the %s row:\n%s", k, v)
		}
	}
	if out, err = run(t, a, opsmigrate.OpRun, `{}`, true); err != nil {
		t.Fatal(err)
	}
	if n := rosterSteps(migrateOut(t, out).Steps); n != 2 {
		t.Fatalf("run reports %d roster steps, want 2: %s", n, out)
	}
	first := read()
	for k, v := range first {
		if !strings.Contains(v, `"_v": 2`) && !strings.Contains(v, `"_v":2`) {
			t.Fatalf("%s row not rewritten _v: 2:\n%s", k, v)
		}
	}
	if act, tm := first[schema.TableActors], first[schema.TableTeams]; !strings.Contains(act, v1RosterActor) || !strings.Contains(act, "Alice Rivera") ||
		!strings.Contains(tm, v1RosterTeam) || !strings.Contains(tm, "backend") || !strings.Contains(tm, v1RosterActor) {
		t.Fatalf("the rewrite lost an id or a field:\n%s\n%s", act, tm)
	}
	b := openWith(t, gitRepo(t), opts)
	withProject(t, b)
	if out, err = run(t, b, opsmigrate.OpRun, `{}`, true); err != nil || rosterSteps(migrateOut(t, out).Steps) != 0 {
		t.Fatalf("second run: %s %v", out, err)
	}
	for k, v := range read() {
		if v != first[k] {
			t.Fatalf("a second run rewrote the %s row", k)
		}
	}
}
