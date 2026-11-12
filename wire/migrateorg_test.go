package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
)

// TestMigrateOrgMarker (orgcore migrateOrg): a repo whose org brings a
// v0.1.17 org store (v1 lore-store.yaml, no sto_ id): `migrate run
// --dry-run` lists the marker step and changes nothing; the run writes
// marker v2 with a new sto_ id and visibility internal; a second run lists
// no org step and keeps the id. (v1 roster rows → _v: 2 are not covered
// here: see the report.)
func TestMigrateOrgMarker(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeV1Marker(t, dir)
	url := fileuri.URL(dir, "")
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: "agency", URL: url, Folder: "acme"}}, nil
	}
	a := openWith(t, gitRepo(t), Options{OrgStores: hook, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, a)
	before := string(mustRead(t, dir+"/"+rowfs.MarkerFile))

	orgStep := func(steps []string) bool {
		for _, s := range steps {
			if strings.HasPrefix(s, "org store agency") && strings.Contains(s, "v1 → v2") {
				return true
			}
		}
		return false
	}
	out, err := run(t, a, opsmigrate.OpRun, `{"dry_run":true}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if !orgStep(migrateOut(t, out).Steps) {
		t.Fatalf("dry run does not list the org marker step: %s", out)
	}
	if after := string(mustRead(t, dir+"/"+rowfs.MarkerFile)); after != before {
		t.Fatalf("a dry run rewrote the marker:\n%s", after)
	}

	if out, err = run(t, a, opsmigrate.OpRun, `{}`, true); err != nil {
		t.Fatal(err)
	}
	if !orgStep(migrateOut(t, out).Steps) {
		t.Fatalf("run does not report the org marker step: %s", out)
	}
	// the run's own last lines say whether each store commit was pushed; a
	// step asking for `lore sync` contradicted a run that pushed (B4-10)
	for _, s := range migrateOut(t, out).Steps {
		if strings.Contains(s, "lore sync") {
			t.Errorf("a step asks for lore sync after a run that pushes: %q", s)
		}
	}
	m, err := rowfs.ReadMarker(dir)
	if err != nil || m.Version != rowfs.MarkerVersion || m.ID.IsZero() || m.Visibility != rowfs.VisibilityInternal {
		t.Fatalf("marker after run: %+v %v", m, err)
	}

	b := openWith(t, gitRepo(t), Options{OrgStores: hook, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, b)
	if out, err = run(t, b, opsmigrate.OpRun, `{}`, true); err != nil || orgStep(migrateOut(t, out).Steps) {
		t.Fatalf("second run: %s %v", out, err)
	}
	if again, _ := rowfs.ReadMarker(dir); again.ID != m.ID {
		t.Fatalf("second run changed the store id: %s → %s", m.ID, again.ID)
	}
}
