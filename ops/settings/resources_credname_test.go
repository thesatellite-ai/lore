package settings_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestCredentialFieldNames (RES 3.1, OV27): a field whose name looks like a
// credential is refused with E_RESOURCE_NAME unless it is a secret_ref, on
// add and on rename (the rename is the other way to get such a name), in
// ordinary names pass; upper case is refused by the snake_case rule.
func TestCredentialFieldNames(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"svc"}`)
	refused := []string{"password", "db_password", "passwd", "api_key", "apikey", "client_secret", "auth_token", "private_key", "credentials"}
	for _, n := range refused {
		if _, err := h.RunYes(settings.OpFieldAdd, `{"kind":"svc","field":"`+n+`","type":"text"}`); !errs.Has(err, errs.ResourceName) {
			t.Errorf("text field %q: %v, want E_RESOURCE_NAME", n, err)
		}
		h.MustYes(settings.OpFieldAdd, `{"kind":"svc","field":"`+n+`","type":"secret_ref"}`)
	}
	// Upper case never reaches the credential rule: names are snake_case.
	for _, n := range []string{"DB_PASSWORD", "Api_Key"} {
		if _, err := h.RunYes(settings.OpFieldAdd, `{"kind":"svc","field":"`+n+`","type":"secret_ref"}`); !errs.Has(err, errs.ResourceName) {
			t.Errorf("%q: %v, want E_RESOURCE_NAME (snake_case)", n, err)
		}
	}
	for _, n := range []string{"port", "owner", "region", "pass_count"} {
		h.MustYes(settings.OpFieldAdd, `{"kind":"svc","field":"`+n+`","type":"text"}`)
	}
	_, err := h.RunYes(settings.OpFieldRename, `{"kind":"svc","field":"owner","new_name":"admin_password"}`)
	if !errs.Has(err, errs.ResourceName) {
		t.Errorf("rename a text field to a credential name: %v, want E_RESOURCE_NAME", err)
	}
	h.MustYes(settings.OpFieldAdd, `{"kind":"svc","field":"vault_entry","type":"secret_ref"}`)
	h.MustYes(settings.OpFieldRename, `{"kind":"svc","field":"vault_entry","new_name":"admin_password"}`)
}
