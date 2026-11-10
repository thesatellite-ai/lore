package settings_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/wire"
)

// secretsListed is `lore secrets list --manager gopass` as (field, ref) pairs of row id.
func secretsListed(t *testing.T, h *opstest.H, id string) map[string]string {
	t.Helper()
	var out struct {
		Data []settings.SecretUse `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpSecretsList, `{"manager":"gopass"}`))), &out); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, u := range out.Data {
		if u.Row == id {
			got[u.Field+"#"+u.Ref] = u.Ref
		}
	}
	return got
}

// TestSecretsInMetadataValues (OV13b, later 508): references kept in
// metadata values are listed by `lore secrets list` (field meta.<name>) and
// rewritten by `lore secrets rename-entry`: a text value, a value with two
// alternates of which one matches, and a json value holding the reference
// in a nested string; a reference to another entry is left as written and
// so is the rest of each value; the row's body references are rewritten in
// the same edit.
func TestSecretsInMetadataValues(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, func(o *wire.Options) {
		o.RepoIsClient = func(context.Context) (bool, error) { return false, nil } // own repo: references may be written
	})
	h.MustYes(settings.OpMetaAdd, `{"name":"dbpass","type":"text"}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"fallback","type":"text"}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"conn","type":"json"}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"other","type":"text"}`)
	id := h.ID(h.Must("memory.add", `{"body":"body uses ${secret:gopass://acme/db-old}","meta":[`+
		`"dbpass=pw is ${secret:gopass://acme/db-old} here",`+
		`"fallback=${secret:op://vault/db|gopass://acme/db-old}",`+
		`"other=${secret:gopass://acme/unrelated}"],`+
		`"meta_json":["conn={\"host\":\"db.internal\",\"auth\":{\"pw\":\"${secret:gopass://acme/db-old}\"}}"]}`))
	before := secretsListed(t, h, id)
	for _, want := range []string{"meta.dbpass#gopass://acme/db-old", "meta.fallback#gopass://acme/db-old", "meta.conn#gopass://acme/db-old",
		"meta.other#gopass://acme/unrelated", "body#gopass://acme/db-old"} {
		if _, ok := before[want]; !ok {
			t.Fatalf("secrets list lacks %s: %v", want, before)
		}
	}
	h.Must(settings.OpSecretsRenameEntry, `{"manager":"gopass","from":"acme/db-old","to_entry":"acme/db-new"}`)
	after := secretsListed(t, h, id)
	for k := range after {
		if strings.Contains(k, "db-old") {
			t.Fatalf("a reference to the old entry is left: %s (%v)", k, after)
		}
	}
	for _, want := range []string{"meta.dbpass#gopass://acme/db-new", "meta.fallback#gopass://acme/db-new", "meta.conn#gopass://acme/db-new",
		"meta.other#gopass://acme/unrelated", "body#gopass://acme/db-new"} {
		if _, ok := after[want]; !ok {
			t.Fatalf("after rename-entry, secrets list lacks %s: %v", want, after)
		}
	}
	shown := h.JSON(h.Must("row.show", `{"id":"`+id+`"}`))
	for _, want := range []string{"pw is ${secret:gopass://acme/db-new} here", "op://vault/db|gopass://acme/db-new", "db.internal"} {
		if !strings.Contains(strings.ReplaceAll(shown, `&`, "&"), want) {
			t.Fatalf("the rewrite changed more than the reference (want %q):\n%s", want, shown)
		}
	}
}
