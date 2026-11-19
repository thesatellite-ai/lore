package wire

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/op"
	opsrow "github.com/thesatellite-ai/lore/ops/row"
	"github.com/thesatellite-ai/lore/store"
)

// TestBulkMoveFromAtAHomeFolder (devco:D-712): `lore move --from
// <org>/<folder> --to <org>/<folder>` run from a folder that is no repo
// (the home folder, one org setup with two folders) attaches the --from
// folder beside the target, writable: the dry run lists the rows and
// writes nothing, and the move puts every row in the target folder and
// none is left live in the source. Before the fix only --to was attached
// outside a repo and the move failed E_NOT_FOUND "no attached store or
// folder" for the source (only story O guarded it).
func TestBulkMoveFromAtAHomeFolder(t *testing.T) {
	t.Parallel()
	ctx := background()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	const (
		legacy  store.Source = "agency/legacy"
		archive store.Source = "agency/archive"
	)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{
			{Name: rosterStore, URL: orgURL, Folder: "org"},
			{Name: legacy, URL: orgURL, Folder: "legacy"},
			{Name: archive, URL: orgURL, Folder: "archive"},
		}, nil
	}
	plain, home := t.TempDir(), t.TempDir()
	app := func() *App {
		a, err := New(Options{RepoDir: plain, Home: home, FlagActor: testEmail, OrgStores: hook, Wall: time.Now})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	a := app()
	add, _ := a.Registry().Lookup("rule.add")
	for _, body := range []string{"bulk home rule one", "bulk home rule two"} {
		if _, err := a.Run(ctx, add, op.JSONDecoder{Body: []byte(`{"body":"` + body + `","to":"` + string(legacy) + `"}`)}, op.Caller{Yes: true}); err != nil {
			t.Fatalf("rule add --to %s: %v", legacy, err)
		}
	}
	if _, err := a.Run(ctx, add, op.JSONDecoder{Body: []byte(`{"body":"archive seed","to":"` + string(archive) + `"}`)}, op.Caller{Yes: true}); err != nil {
		t.Fatalf("rule add --to %s: %v", archive, err)
	}
	// each run on its own App, its op looked up there (a registry's ops
	// run on the App that built it), as each CLI call opens its own
	run := func(name, body string, c op.Caller) (op.Result, error) {
		b := app()
		e, ok := b.Registry().Lookup(name)
		if !ok {
			t.Fatalf("no %s", name)
		}
		return b.Run(ctx, e, op.JSONDecoder{Body: []byte(body)}, c)
	}
	in := `{"from":"` + string(legacy) + `","to":"` + string(archive) + `","where":["body~=bulk home"]`
	res, err := run(opsrow.OpMove, in+`,"dry_run":true}`, op.Caller{})
	if err != nil {
		t.Fatalf("lore move --from %s --to %s --dry-run from a home folder: %v", legacy, archive, err)
	}
	if out, jerr := res.JSON(); jerr != nil || strings.Count(string(out), "bulk home rule") != 2 {
		t.Fatalf("dry run does not list the two rows: %s %v", out, jerr)
	}
	res, err = run(opsrow.OpMove, in+`}`, op.Caller{Yes: true})
	if err != nil {
		t.Fatalf("lore move --from %s --to %s --yes from a home folder: %v", legacy, archive, err)
	}
	if out, jerr := res.JSON(); jerr != nil || strings.Count(string(out), "bulk home rule") != 2 {
		t.Fatalf("the move does not report the two rows: %s %v", out, jerr)
	}
	// nothing left live in the source: the same move finds no row
	res, err = run(opsrow.OpMove, in+`}`, op.Caller{Yes: true})
	if err != nil {
		t.Fatalf("the move run again: %v", err)
	}
	if out, _ := res.JSON(); strings.Contains(string(out), "bulk home rule") {
		t.Fatalf("rows still live in %s after the move: %s", legacy, out)
	}
	res, err = run("rule.list", `{"store":"`+string(archive)+`"}`, op.Caller{})
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := res.JSON(); strings.Count(string(out), "bulk home rule") != 2 {
		t.Fatalf("%s does not hold the two moved rules: %s", archive, out)
	}
}
