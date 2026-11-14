package doctor

// all_homewritable_test.go — doctor --all names a lore home lore cannot
// write. Reads work on such a home (their caches go to a temp folder), so
// before HomeEnv nothing failed and --all read HEALTHY while every setup,
// store clone and machine-state write was lost at exit. Plain doctor stays
// quiet about it: a CI runner's read-only HOME reads HEALTHY.

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// unwritableHomeEnv is a setupEnv whose lore home cannot be written.
type unwritableHomeEnv struct{ *setupEnv }

func (unwritableHomeEnv) HomeWritable(context.Context) error {
	return errs.New(errs.ReadOnlyFS, "the lore home /h/.lore (~/.lore) is not writable").WithHint("chmod u+w /h/.lore")
}

func TestAllNamesAnUnwritableHome(t *testing.T) {
	env := unwritableHomeEnv{newEnv(t)}
	r := runAllT(t, env, RunIn{})
	if r.Exit != 2 || len(r.Machine.Checks) != 1 || r.Machine.Checks[0].Check != onboard.CheckSetupConfig {
		t.Fatalf("doctor --all on an unwritable home: %+v", r)
	}
	if c := r.Machine.Checks[0]; !strings.Contains(c.Detail, "/h/.lore") || !strings.Contains(c.Fix.Text, "chmod") {
		t.Fatalf("the finding does not name the home and its fix: %+v", c)
	}
	plain, err := Run(context.Background(), env, RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Exit == 2 || len(plain.Machine.Checks) != 0 {
		t.Fatalf("plain doctor judged the lore home (a CI runner's read-only HOME must read healthy): %+v", plain)
	}
}
