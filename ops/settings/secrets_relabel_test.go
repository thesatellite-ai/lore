package settings_test

import (
	"testing"

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
