package setup

// rename_test.go — editing a setup that exists (owner report 2026-11-08):
// the kind step of a finished setup saves onto it (kind, default, a new
// name as a rename), setup.rename, and the one name rule the kind step's
// save, setup.rename and setup.name_free share.

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// RenameSetup makes fakeStore a SetupRenamer (config only, as wire).
func (f *fakeStore) RenameSetup(_ context.Context, from, to string) (bool, error) {
	at := -1
	for i, s := range f.cfg.Setups {
		if s.Name == to && from != to {
			return true, errs.New(errs.SetupNameTaken, "taken")
		}
		if s.Name == from {
			at = i
		}
	}
	if at < 0 {
		return false, nil
	}
	f.cfg.Setups[at].Name = to
	return true, nil
}

// boolAns is a bool answer.
func boolAns(b bool) onboard.Answer { return onboard.Answer{Bool: b, IsBool: true} }

// finishedPersonal is a finished solo setup (an entry, no progress rows)
// beside another setup, "work".
func finishedPersonal() fakeEnv {
	return newFakeEnv(
		onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Default: true, Folders: []string{"~/code/**"}},
		onboard.SetupEntry{Name: "work", Kind: onboard.KindSolo},
	)
}

func TestKindStepEditsExistingSetup(t *testing.T) {
	t.Run("own name saves the edit", func(t *testing.T) {
		env := finishedPersonal()
		e := newTestEngine(env)
		ans := onboard.Answers{"kind": text("solo"), "name": text("personal"), "default": boolAns(false)}
		out, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "kind", Answers: ans})
		if err != nil {
			t.Fatalf("kind step of a finished setup with its own name: %v", err)
		}
		if out.Status != onboard.StepDone {
			t.Errorf("status = %q, want done", out.Status)
		}
		if got, _ := env.store.cfg.Setup("personal"); got.Default {
			t.Errorf("default not cleared: %+v", got)
		}
		if n := len(env.store.cfg.Setups); n != 2 {
			t.Errorf("%d setups, want 2 (no second entry)", n)
		}
		if rows := env.store.progress["personal"]; len(rows) != 0 {
			t.Errorf("a finished setup gained progress rows: %v", rows)
		}
	})
	t.Run("new name renames", func(t *testing.T) {
		env := finishedPersonal()
		e := newTestEngine(env)
		ans := onboard.Answers{"kind": text("solo"), "name": text("home"), "default": boolAns(true)}
		if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "kind", Answers: ans}); err != nil {
			t.Fatalf("rename on the kind step: %v", err)
		}
		if _, ok := env.store.cfg.Setup("personal"); ok {
			t.Error("old name still in setups[]")
		}
		got, ok := env.store.cfg.Setup("home")
		if !ok || len(got.Folders) != 1 || !got.Default {
			t.Errorf("renamed entry = %+v (found %v), want the old entry's folders and default", got, ok)
		}
		if n := len(env.store.cfg.Setups); n != 2 {
			t.Errorf("%d setups, want 2", n)
		}
		if len(env.store.progress["home"]) != 0 || len(env.store.progress["personal"]) != 0 {
			t.Errorf("progress rows after renaming a finished setup: %v", env.store.progress)
		}
	})
	t.Run("another setup's name refused, nothing written", func(t *testing.T) {
		env := finishedPersonal()
		e := newTestEngine(env)
		ans := onboard.Answers{"kind": text("solo"), "name": text("work")}
		_, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "kind", Answers: ans})
		if !errs.Has(err, errs.SetupNameTaken) {
			t.Fatalf("err = %v, want E_SETUP_NAME_TAKEN", err)
		}
		if len(env.applier.calls) != 0 {
			t.Errorf("Applier ran %v for a taken name", env.applier.calls)
		}
		if _, ok := env.store.cfg.Setup("personal"); !ok {
			t.Error("personal renamed away though the save was refused")
		}
	})
	t.Run("kind change to a team kind reopens the setup", func(t *testing.T) {
		env := finishedPersonal()
		e := newTestEngine(env)
		ans := onboard.Answers{"kind": text("admin"), "name": text("personal")}
		if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "kind", Answers: ans}); err != nil {
			t.Fatalf("kind change: %v", err)
		}
		if got, _ := env.store.cfg.Setup("personal"); got.Kind != onboard.KindSolo {
			t.Errorf("entry kind = %q, want solo kept until the team step writes it", got.Kind)
		}
		row, ok := env.store.progress["personal"][onboard.StepKind]
		if !ok || row.Status != onboard.StepDone || answerKind(row.Answers) != onboard.KindAdmin {
			t.Fatalf("kind row = %+v (found %v), want done with kind admin", row, ok)
		}
		plan, err := e.Plan(bg, PlanIn{Setup: "personal"})
		if err != nil {
			t.Fatal(err)
		}
		if !plan.Exists || len(plan.Progress) != 1 {
			t.Errorf("plan exists=%v progress=%v, want an existing setup with only the kind step saved", plan.Exists, plan.Progress)
		}
		s, err := e.load(bg, env.store, "personal", nil)
		if err != nil {
			t.Fatal(err)
		}
		if s.kind != onboard.KindAdmin || s.entry.Kind != onboard.KindAdmin || s.finished() {
			t.Errorf("loaded kind=%q entry kind=%q finished=%v, want admin, admin, unfinished", s.kind, s.entry.Kind, s.finished())
		}
	})
	t.Run("half-made setup's progress follows a rename", func(t *testing.T) {
		env, e := started(t)
		if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "identity", Answers: emailAnswers}); err != nil {
			t.Fatal(err)
		}
		ans := onboard.Answers{"kind": text("solo"), "name": text("home")}
		if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "kind", Answers: ans}); err != nil {
			t.Fatalf("rename of a half-made setup: %v", err)
		}
		if len(env.store.progress["personal"]) != 0 {
			t.Errorf("rows left under the old name: %v", env.store.progress["personal"])
		}
		if _, ok := env.store.progress["home"][onboard.StepIdentity]; !ok {
			t.Errorf("identity row did not follow the rename: %v", env.store.progress["home"])
		}
	})
}

func TestRenameOp(t *testing.T) {
	t.Run("renames", func(t *testing.T) {
		env := finishedPersonal()
		e := newTestEngine(env)
		out, err := e.Rename(bg, RenameIn{Name: "personal", To: "home"})
		if err != nil || !out.Changed || out.From != "personal" || out.Name != "home" {
			t.Fatalf("rename = %+v, %v", out, err)
		}
		if _, ok := env.store.cfg.Setup("home"); !ok {
			t.Error("home not in setups[]")
		}
	})
	t.Run("same name is unchanged", func(t *testing.T) {
		e := newTestEngine(finishedPersonal())
		out, err := e.Rename(bg, RenameIn{Name: "personal", To: "personal"})
		if err != nil || out.Changed {
			t.Fatalf("rename onto itself = %+v, %v", out, err)
		}
	})
	for _, c := range []struct {
		name     string
		from, to string
		code     errs.Code
	}{
		{"another setup's name", "personal", "work", errs.SetupNameTaken},
		{"no such setup", "nobody", "home", errs.SetupNotFound},
		{"bad new name", "personal", "Not A Name", errs.InvalidInput},
		{"empty new name", "personal", "", errs.InvalidInput},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := finishedPersonal()
			e := newTestEngine(env)
			_, err := e.Rename(bg, RenameIn{Name: c.from, To: c.to})
			if !errs.Has(err, c.code) {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
			if _, ok := env.store.cfg.Setup("personal"); !ok {
				t.Error("personal renamed though the rename was refused")
			}
		})
	}
	t.Run("progress left under the new name refused", func(t *testing.T) {
		env := finishedPersonal()
		// A trashed setup "old" keeps its progress rows until the purge.
		env.store.progress["old"] = map[onboard.StepID]onboard.Progress{onboard.StepKind: {Setup: "old", Step: onboard.StepKind, Status: onboard.StepDone}}
		e := newTestEngine(env)
		if _, err := e.Rename(bg, RenameIn{Name: "personal", To: "old"}); !errs.Has(err, errs.SetupNameTaken) {
			t.Fatalf("err = %v, want E_SETUP_NAME_TAKEN", err)
		}
	})
}
