package settings_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
)

// TestSecretsRelabelHolders (SECRETS 5.2, OV13b): lore secrets relabel
// sets an actor holder's label to the actor's current handle, a machine
// holder's label to "<handle>.<current machine label>" (handle part kept),
// inside text and json fields alike; a holder lore has no row for keeps
// its label; a second run changes nothing.
func TestSecretsRelabelHolders(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := multiStore(t)
	act := h.Seed(schema.TableActors, map[string]string{"handle": `"mira"`})
	h.Seed(schema.TableMachines, map[string]string{"label": `"new_mac"`, "machine_id": `"` + holderA + `"`})
	actRef := "${secret:env://A?holder=" + act + "&label=mira_old}"
	mchRef := "${secret:env://B?holder=" + holderA + "&label=mira.old_mac}"
	unknownRef := "${secret:env://C?holder=" + holderB + "&label=bob.box}"
	h.MustYes("memory.add", `{"body":"a `+actRef+` b `+mchRef+` c `+unknownRef+`","to":"kb"}`)
	h.MustYes(settings.OpSecretsRelabel, ``)
	got := map[string]string{}
	for _, u := range secretsOf(t, h, settings.OpSecretsList, ``).Data {
		got[u.Holder] = u.Label
	}
	want := map[string]string{act: "mira", holderA: "mira.new_mac", holderB: "bob.box"}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("labels = %v, want %v", got, want)
		}
	}
	again, err := h.RunYes(settings.OpSecretsRelabel, ``)
	if err != nil {
		t.Fatal(err)
	}
	if n := h.Field(again, "count"); n != "" && n != "0" {
		t.Fatalf("second relabel changed %s references", n)
	}
}

// TestSecretsRelabelLabel (later 522, CLI_REFERENCE 3.13): relabel
// --holder <id> --label <text> asks first (nothing written without
// --yes), then sets exactly that label on that holder's references only;
// --label without --holder, a label with edge spaces or a control
// character is E_INVALID_INPUT; a run with nothing left to change is
// E_NOT_FOUND.
func TestSecretsRelabelLabel(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := multiStore(t)
	refA := "${secret:env://A?holder=" + holderA + "&label=old.a}"
	refB := "${secret:env://B?holder=" + holderB + "&label=old.b}"
	h.MustYes("memory.add", `{"body":"x `+refA+` y `+refB+`","to":"kb"}`)
	labels := func() map[string]string {
		got := map[string]string{}
		for _, u := range secretsOf(t, h, settings.OpSecretsList, ``).Data {
			got[u.Holder] = u.Label
		}
		return got
	}
	in := `{"holder":"` + holderA + `","label":"mira.studio"}`
	h.Fails(settings.OpSecretsRelabel, in, errs.NeedsConfirm)
	if got := labels(); got[holderA] != "old.a" {
		t.Fatalf("a refused relabel wrote: %v", got)
	}
	h.MustYes(settings.OpSecretsRelabel, in)
	if got := labels(); got[holderA] != "mira.studio" || got[holderB] != "old.b" {
		t.Fatalf("labels after relabel: %v", got)
	}
	for _, bad := range []string{`{"label":"x"}`, `{"holder":"` + holderA + `","label":" x"}`, `{"holder":"` + holderA + `","label":"a\tb"}`} {
		if _, err := h.RunYes(settings.OpSecretsRelabel, bad); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: %v, want E_INVALID_INPUT", bad, err)
		}
	}
	if _, err := h.RunYes(settings.OpSecretsRelabel, in); !errs.Has(err, errs.NotFound) {
		t.Errorf("nothing left to relabel: %v, want E_NOT_FOUND", err)
	}
}
