package task_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// runImportAs is runImport with the caller's secret answer given up front.
func runImportAs(h *opstest.H, input string, ch op.SecretChoice) (op.Result, error) {
	h.T.Helper()
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		h.T.Fatal(err)
	}
	e := opstask.ImportOps(h.App)[0]
	return h.App.Run(context.Background(), e, op.JSONDecoder{Body: []byte(input)}, op.Caller{Actor: actor, Yes: true, Secret: ch})
}

// TestImportSecretsPlannedForTheBatch (devco D-717): task import screens
// every record before the first write. A file with keys in two records
// is refused once, naming BOTH records by file line with the import to
// rerun, and nothing is written (the dry run refuses the same way); two
// --secret-ref values for the two findings give each task its own
// reference. Before, each create screened its own record: the refusal
// named only the first, and the second ref was "1 findings but 2
// --secret-ref values".
func TestImportSecretsPlannedForTheBatch(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	before := dataFiles(t, h, schema.TableTasks)
	k1 := "AKIA" + strings.Repeat("Q", 4) + "7XRT2MNB5KZ3" // made up, built at run time
	k2 := "AKIA" + strings.Repeat("W", 4) + "3PLM8VCX4HJ6"
	f := writeFile(t, "keys.csv", "id,title,body\nK-1,one,key "+k1+"\nK-2,plain,nothing here\nK-3,three,key "+k2+"\n")
	in := `{"file":` + opstest.Quote(f) + `}`

	for _, dry := range []bool{true, false} {
		body := in
		if dry {
			body = `{"file":` + opstest.Quote(f) + `,"dry_run":true}`
		}
		_, err := runImportAs(h, body, op.SecretChoice{})
		if err == nil {
			t.Fatalf("dry=%v: an import with keys in two records: want a refusal", dry)
		}
		msg := err.Error()
		var e *errs.Error
		if errors.As(err, &e) {
			msg += " " + e.Hint
		}
		for _, want := range []string{"2 row(s) hold what looks like a secret", "line 2 (K-1)", "line 4 (K-3)", "lore task import --file", "--secret-unstored", "--not-a-secret"} {
			if !strings.Contains(msg, want) {
				t.Errorf("dry=%v refusal lacks %q:\n%s", dry, want, msg)
			}
		}
		if strings.Contains(msg, k1) || strings.Contains(msg, k2) {
			t.Errorf("dry=%v refusal repeats a key value", dry)
		}
		if got := dataFiles(t, h, schema.TableTasks) - before; got != 0 {
			t.Fatalf("dry=%v: a refused import wrote %d tasks, want 0", dry, got)
		}
	}

	res, err := runImportAs(h, in, op.SecretChoice{Refs: []string{"gopass://acme/k1", "gopass://acme/k2"}})
	if err != nil {
		t.Fatalf("two refs for two findings in two records: %v", err)
	}
	got := importIDs(h, res)
	if len(got) != 3 {
		t.Fatalf("import ids %v: want 3", got)
	}
	for i, want := range map[int]string{0: "${secret:gopass://acme/k1}", 2: "${secret:gopass://acme/k2}"} {
		_, r := get(h, schema.TableTasks, got[i])
		if b := field(r, "body"); !strings.Contains(b, want) || strings.Contains(b, "AKIA") {
			t.Errorf("task %d body %q: want its own reference %s", i, b, want)
		}
	}
}
