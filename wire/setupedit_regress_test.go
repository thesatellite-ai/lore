package wire_test

// setupedit_regress_test.go — a setup opened from the list is edited
// (owner report 2026-11-08): on a finished setup the kind step showed
// setup.name_free "name is free" and then refused Save with
// E_SETUP_NAME_TAKEN for the setup's own name. The check and the save
// now share one rule, through the real wire ops.

import (
	"strings"
	"testing"
)

// nameFree is the setup.name_free result of out ("" when absent).
func nameFree(out rwCheckOut) string {
	for _, c := range out.Checks {
		if c.Check == "setup.name_free" {
			return c.Status
		}
	}
	return ""
}

func TestKindStepEditsFinishedSetup(t *testing.T) {
	w := newRegressWorld(t, rwShape{GHAccounts: 1, SSH: "default_only"})
	a := w.Accounts[0]
	w.WriteConfig(rwPersonalConfig(a.Login, a.Email) +
		"  - name: work\n    kind: solo\n")

	own := rwAnswers{"kind": str("solo"), "name": str("personal"), "default": []byte("false")}
	ownOut := w.Check("personal", "kind", own)
	if got := nameFree(ownOut); got != "ok" {
		t.Fatalf("setup.name_free on the setup's own name = %q, want ok", got)
	}
	for _, c := range ownOut.Checks {
		if c.Check == "setup.name_free" && c.Detail != "this setup's own name" {
			t.Errorf("setup.name_free on the setup's own name says %q, want \"this setup's own name\" (\"name is free\" read as a new setup)", c.Detail)
		}
	}
	if err := w.Apply("personal", "kind", own); err != nil {
		t.Fatalf("Save of the kind step with the setup's own name: %v", err)
	}
	if cfg := w.Config(); strings.Contains(cfg, "default: true") {
		t.Errorf("default not cleared:\n%s", cfg)
	}

	taken := rwAnswers{"kind": str("solo"), "name": str("work")}
	if got := nameFree(w.Check("personal", "kind", taken)); got != "fail" {
		t.Errorf("setup.name_free on another setup's name = %q, want fail", got)
	}
	if err := w.Apply("personal", "kind", taken); err == nil || !strings.Contains(err.Error(), "E_SETUP_NAME_TAKEN") {
		t.Errorf("Save onto another setup's name: %v, want E_SETUP_NAME_TAKEN", err)
	}

	renamed := rwAnswers{"kind": str("solo"), "name": str("home")}
	if got := nameFree(w.Check("personal", "kind", renamed)); got != "ok" {
		t.Errorf("setup.name_free on a free new name = %q, want ok", got)
	}
	if err := w.Apply("personal", "kind", renamed); err != nil {
		t.Fatalf("rename on the kind step: %v", err)
	}
	cfg := w.Config()
	if !strings.Contains(cfg, "name: home") || strings.Contains(cfg, "name: personal") || strings.Count(cfg, "kind: solo") != 2 {
		t.Errorf("after the rename config.yaml should hold home and work only:\n%s", cfg)
	}
	if !strings.Contains(cfg, a.Email) {
		t.Errorf("the renamed setup lost its identity:\n%s", cfg)
	}
	// The identity step of the renamed setup saves onto it, as an edit.
	if err := w.Apply("home", "identity", rwAnswers{"name": str("Pat Example"), "email": str(a.Email)}); err != nil {
		t.Errorf("identity step of the renamed setup: %v", err)
	}
}

// A kind change reopens the setup on the new kind's steps, and switching
// back saves too: the kind step's questions are judged by the kind it
// answers, and an answer that kind does not ask (default is solo only) is
// dropped, never refused (the page sends the default it showed).
func TestKindStepKindSwitch(t *testing.T) {
	w := newRegressWorld(t, rwShape{GHAccounts: 1, SSH: "default_only"})
	a := w.Accounts[0]
	w.WriteConfig(rwPersonalConfig(a.Login, a.Email))

	admin := rwAnswers{"kind": str("admin"), "name": str("personal"), "default": []byte("true")}
	if err := w.Apply("personal", "kind", admin); err != nil {
		t.Fatalf("kind solo → admin with the default the page showed: %v", err)
	}
	if cfg := w.Config(); !strings.Contains(cfg, "kind: solo") {
		t.Errorf("config.yaml changed kind before the team step wrote the team:\n%s", cfg)
	}
	var plan struct {
		Steps    []struct{ ID string } `json:"steps"`
		Progress []rwProgress          `json:"progress"`
		Exists   bool                  `json:"exists"`
	}
	rwMust(w, "setup.plan", map[string]string{"setup": "personal"}, &plan)
	team := false
	for _, s := range plan.Steps {
		team = team || s.ID == "team"
	}
	if !team || !plan.Exists || len(plan.Progress) != 1 {
		t.Errorf("after the kind change the plan should be the admin one (team step %v), exist (%v), with only the kind step saved (%v)", team, plan.Exists, plan.Progress)
	}

	solo := rwAnswers{"kind": str("solo"), "name": str("personal"), "default": []byte("false")}
	if err := w.Apply("personal", "kind", solo); err != nil {
		t.Fatalf("kind admin → solo with a default answer: %v", err)
	}
	if cfg := w.Config(); !strings.Contains(cfg, "kind: solo") || strings.Contains(cfg, "default: true") {
		t.Errorf("after switching back, personal should be solo and not the default:\n%s", cfg)
	}
}
