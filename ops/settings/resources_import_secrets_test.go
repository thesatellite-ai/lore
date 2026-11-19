package settings_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceImportSecretsPlannedForTheBatch (devco:D-717): a resource
// import screens every record before the first write. A file with keys
// in two records is refused once naming both (it named only the first),
// with the import to rerun, and nothing is written, a dry run too; two
// --secret-ref values are counted against the whole file and each record
// stores its own (each write saw both refs for its one finding and
// refused "1 findings but 2 --secret-ref values").
func TestResourceImportSecretsPlannedForTheBatch(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, ownRepo) // a client repo refuses secret references (BATCH 5)
	h.MustYes(settings.OpKindAdd, `{"kind":"server"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"server","field":"access_key","type":"text"}`)
	keyA := "AKIA" + strings.Repeat("Q", 16) // built at run time: no key-shaped literal in source
	keyB := "AKIA" + strings.Repeat("Z", 16)
	file := filepath.Join(t.TempDir(), "servers.csv")
	if err := os.WriteFile(file, []byte("name,access_key\nhub_a,"+keyA+"\nhub_plain,none\nhub_b,"+keyB+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := `{"file":` + opstest.Quote(file) + `,"kind":"server"}`
	dry := `{"file":` + opstest.Quote(file) + `,"kind":"server","dry_run":true}`
	for _, c := range []struct{ name, input string }{{"dry run", dry}, {"import", in}} {
		_, err := h.RunYes(settings.OpResImport, c.input)
		if !errs.Has(err, errs.NeedsConfirm) && !errs.Has(err, errs.SecretDetected) {
			t.Fatalf("%s of two keyed records: %v, want the batch refusal", c.name, err)
		}
		msg := err.Error()
		var ee *errs.Error
		if errors.As(err, &ee) {
			msg += "\n" + ee.Hint
		}
		for _, want := range []string{"resource import: 2 row(s)", "server/hub_a", "server/hub_b", "nothing written", "lore resource import --file " + file + " --from csv --kind server --yes --secret-unstored"} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s refusal does not name %q:\n%s", c.name, want, msg)
			}
		}
		if strings.Contains(msg, keyA) || strings.Contains(msg, keyB) {
			t.Fatalf("%s refusal echoes a key:\n%s", c.name, msg)
		}
	}
	if got := h.JSON(h.Must(settings.OpResList, `{"kind":"server"}`)); strings.Contains(got, "hub_") {
		t.Fatalf("a refused import wrote rows: %s", got)
	}

	e, ok := h.App.Registry().Lookup(settings.OpResImport)
	if !ok {
		t.Fatal("no resource.import")
	}
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	refs := []string{"gopass://acme/hub/a", "gopass://acme/hub/b"}
	if _, err = h.App.Run(context.Background(), e, op.JSONDecoder{Body: []byte(in)}, op.Caller{Actor: actor, Yes: true, Secret: op.SecretChoice{Refs: refs}}); err != nil {
		t.Fatalf("import with one ref per finding: %v", err)
	}
	for i, name := range []string{"hub_a", "hub_b"} {
		got := h.JSON(h.Must(settings.OpResShow, `{"id":"`+name+`"}`))
		if strings.Contains(got, keyA) || strings.Contains(got, keyB) || !strings.Contains(got, "${secret:"+refs[i]+"}") {
			t.Errorf("%s stored %s, want ${secret:%s} and no key", name, got, refs[i])
		}
	}
}
