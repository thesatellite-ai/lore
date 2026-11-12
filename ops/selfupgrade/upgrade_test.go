package selfupgrade

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// fakeUpgrader records what it was asked and answers lines or err.
type fakeUpgrader struct {
	got       Flags
	confirmed bool
	lines     []string
	err       error
}

func (f *fakeUpgrader) Upgrade(_ context.Context, fl Flags, confirmed bool) ([]string, error) {
	f.got, f.confirmed = fl, confirmed
	return f.lines, f.err
}

// TestRunPassesFlagsAndConfirm pins that upgrade.run hands every flag and
// the caller's yes to the Upgrader and prints its lines in order.
func TestRunPassesFlagsAndConfirm(t *testing.T) {
	u := &fakeUpgrader{lines: []string{"lore 0.1.20 is available", "Run: lore upgrade"}}
	out, err := Run(context.Background(), u, RunIn{Check: true, Version: " v0.1.20 ", IgnorePin: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	if u.got != (Flags{Check: true, Version: "v0.1.20", IgnorePin: true}) || !u.confirmed {
		t.Fatalf("upgrader got %+v confirmed=%v", u.got, u.confirmed)
	}
	if out.Text() != "lore 0.1.20 is available\nRun: lore upgrade\n" {
		t.Fatalf("text %q", out.Text())
	}
	empty, err := Run(context.Background(), &fakeUpgrader{}, RunIn{}, false)
	if err != nil || empty.Lines == nil || empty.Text() != "" {
		t.Fatalf("empty run: %+v %v", empty, err)
	}
}

// TestRunKeepsTheUpgradersError pins that a refusal (E_NEEDS_CONFIRM for a
// locked-out downgrade) reaches the surface unchanged.
func TestRunKeepsTheUpgradersError(t *testing.T) {
	_, err := Run(context.Background(), &fakeUpgrader{err: errs.New(errs.NeedsConfirm, "downgrade?")}, RunIn{Version: "v0.1.16"}, false)
	if !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("err %v", err)
	}
}

// TestOpsRegister pins the spec: a Local operation that may ask, with
// every error code registered.
func TestOpsRegister(t *testing.T) {
	entries := Ops(nil)
	if len(entries) != 1 {
		t.Fatalf("%d entries", len(entries))
	}
	s := entries[0].Spec()
	if s.Name != OpRun || !s.Local || !s.Confirm.MayAsk || s.Confirm.Always || s.Confirm.When != "" {
		t.Fatalf("spec %+v", s)
	}
	for _, c := range s.Errors {
		if !c.Known() {
			t.Fatalf("unknown code %s", c)
		}
	}
	if s.Permission != op.PermWrite {
		t.Fatalf("permission %v", s.Permission)
	}
}

// sourceUpgrader is a fakeUpgrader for a binary built from source.
type sourceUpgrader struct {
	fakeUpgrader
	source bool
}

func (s *sourceUpgrader) FromSource() bool { return s.source }

// fixEnv hands out one upgrader and counts the fixes run after it.
type fixEnv struct {
	u     Upgrader
	fixes int
}

func (e *fixEnv) Upgrader(context.Context) (Upgrader, error) { return e.u, nil }

func (e *fixEnv) FixAfterUpgrade(context.Context) ([]string, error) {
	e.fixes++
	return []string{"lore fix: ran"}, nil
}

// TestSourceBuildRunsNoFix pins B4-4: a source build installs nothing, so
// upgrade prints its one line and runs no lore fix; an installed release
// still runs it.
func TestSourceBuildRunsNoFix(t *testing.T) {
	src := &fixEnv{u: &sourceUpgrader{fakeUpgrader: fakeUpgrader{lines: []string{"built from source"}}, source: true}}
	out, err := runWithFix(context.Background(), src, RunIn{}, false)
	if err != nil || src.fixes != 0 || out.Text() != "built from source\n" {
		t.Fatalf("source build: fixes=%d out=%q err=%v", src.fixes, out.Text(), err)
	}
	rel := &fixEnv{u: &sourceUpgrader{fakeUpgrader: fakeUpgrader{lines: []string{"Done."}}}}
	out, err = runWithFix(context.Background(), rel, RunIn{}, false)
	if err != nil || rel.fixes != 1 || out.Text() != "Done.\nlore fix: ran\n" {
		t.Fatalf("release: fixes=%d out=%q err=%v", rel.fixes, out.Text(), err)
	}
}
