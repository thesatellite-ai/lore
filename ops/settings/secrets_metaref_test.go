package settings_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestMetaKeySecretRefTypeRefused (METADATA RX8, G-META-11): metadata has
// no ref or secret_ref type (a ${secret:…} reference in a text value
// serves it), so meta add refuses both with E_META_TYPE and writes no key
// row; before, the engine skipped such a key and the first write
// registered a second, text key of the same name beside it.
func TestMetaKeySecretRefTypeRefused(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := multiStore(t)
	for _, typ := range []string{"secret_ref", "ref"} {
		_, err := h.RunYes(settings.OpMetaAdd, `{"name":"vault_entry","type":"`+typ+`","to":"kb"}`)
		if !errs.Has(err, errs.MetaType) || !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "${secret:manager://entry}") {
			t.Fatalf("meta add --type %s: %v, want E_META_TYPE with the ${secret:} hint", typ, err)
		}
	}
	var keys settings.MetaKeysOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpMetaKeys, `{}`))), &keys); err != nil {
		t.Fatal(err)
	}
	if keys.Count != 0 {
		t.Fatalf("refused meta add wrote keys: %+v", keys.Data)
	}
	// the supported form: a ${secret:} reference in a text value is listed
	// and rewritten (OV13b, b7953e6a)
	mem := h.ID(h.MustYes(memAdd, `{"body":"deploy","to":"kb","meta":["vault_entry=${secret:env://LORE_META_OLD}"]}`))
	h.MustYes(settings.OpSecretsRenameEntry, `{"manager":"env","from":"LORE_META_OLD","to_entry":"LORE_META_NEW"}`)
	var locs []string
	for _, u := range secretsOf(t, h, settings.OpSecretsList, ``).Data {
		if u.Row == mem {
			locs = append(locs, u.Field+"="+u.Locator)
		}
	}
	if strings.Join(locs, ",") != "meta.vault_entry=LORE_META_NEW" {
		t.Fatalf("references after rename-entry: %v", locs)
	}
}

// TestSecretsLinkMetaPlaceholder (SECRETS 7, G-META-11): an unstored
// placeholder in a metadata value is numbered after the row's field
// placeholders, as secrets list --unstored prints it, and secrets link
// --at fills exactly that one: the body's placeholder stays unstored.
func TestSecretsLinkMetaPlaceholder(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := multiStore(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"token_note","type":"text","to":"kb"}`)
	mem := h.ID(h.MustYes(memAdd, `{"body":"token ${secret:unstored}","to":"kb","meta":["token_note=${secret:unstored}"]}`))
	at := map[string]int{}
	for _, u := range secretsOf(t, h, settings.OpSecretsList, `{"unstored":true}`).Data {
		if u.Row == mem {
			at[u.Field] = u.At
		}
	}
	if at["body"] != 1 || at["meta.token_note"] != 2 {
		t.Fatalf("--unstored numbering %v, want body 1, meta.token_note 2", at)
	}
	h.MustYes(settings.OpSecretsLink, `{"id":"`+mem+`","at":2,"ref":"env://LORE_META_LINKED","holder":"`+holderA+`"}`)
	left := map[string]string{}
	for _, u := range secretsOf(t, h, settings.OpSecretsList, ``).Data {
		if u.Row == mem {
			left[u.Field] = u.Ref
		}
	}
	if left["body"] != "unstored" || !strings.HasPrefix(left["meta.token_note"], "env://LORE_META_LINKED") {
		t.Fatalf("after link --at 2: %v", left)
	}
}
