package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/secretmgr"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/tests/fakeexec"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestThisHolder (SECRETS 5.2, SQ1, BATCH 11, OV51): the holder of a
// reference written on this machine is the mch_ id, plus a label when this
// machine's machines row has one ("<handle>.<label>" when the actor has a
// handle). No row or no label gives the id alone, never an error; the id
// is the same in every case (it comes from ~/.lore/machine.json).
func TestThisHolder(t *testing.T) {
	t.Parallel()
	ctx := background()
	a := newTestApp(t, gitRepo(t))
	withProject(t, a)
	id0, label0, err := a.ThisHolder(ctx)
	if err != nil || !strings.HasPrefix(id0, "mch_") || label0 != "" {
		t.Fatalf("no machines row: %q %q %v", id0, label0, err)
	}
	tm, err := a.ThisMachine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{}
	for k, v := range tm.Fields {
		fields[k] = v
	}
	if _, err = a.CreateWithID(ctx, store.SourceRepo, schema.TableMachines, tm.RowID, fields); err != nil {
		t.Fatal(err)
	}
	if id, label, herr := a.ThisHolder(ctx); herr != nil || id != id0 || label != "" {
		t.Fatalf("row without label: %q %q %v", id, label, herr)
	}
	lbl, err := value.TextOf("laptop-ü")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Update(ctx, engine.Ref{ID: tm.RowID}, map[string]value.Value{schema.FieldMachineLabel: lbl}); err != nil {
		t.Fatal(err)
	}
	who, err := a.Who(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := "laptop-ü"
	if who.Handle != "" {
		want = who.Handle + "." + want
	}
	if id, label, herr := a.ThisHolder(ctx); herr != nil || id != id0 || label != want {
		t.Fatalf("labelled row: %q %q %v, want label %q", id, label, herr, want)
	}
}

// fakeCLIMode is owner-only execute: the fake manager CLI must run, and
// nobody else may.
const fakeCLIMode = 0o700

// TestSecretManagersTimeout (SECRETS 6, OV51): the managers lore reveals
// through obey policy secrets.manager_timeout: a CLI that hangs is killed
// after the policy's seconds (the error names them), not after the
// default 5 s.
func TestSecretManagersTimeout(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	ctx := background()
	bin := t.TempDir()
	p := filepath.Join(bin, "gopass")
	fakeexec.Write(t, p, "#!/bin/sh\nsleep 20\n", fakeCLIMode)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	a := newTestApp(t, gitRepo(t))
	withProject(t, a)
	addPolicy(t, a, policy.KeySecretsManagerTimeout, `1`)
	set, err := a.SecretManagers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gp, ok := set.Get(secretmgr.Gopass)
	if !ok {
		t.Fatal("no gopass manager")
	}
	_, err = gp.Reveal(context.Background(), secretmgr.Locator{Path: "acme/x"})
	if !errs.Has(err, errs.VaultEntryUnavailable) || !strings.Contains(err.Error(), "timed out after 1s") {
		t.Fatalf("err = %v, want a 1s timeout", err)
	}
}
