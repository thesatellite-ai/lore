package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceSecretRef: a secret_ref field is internal by default, a bare
// path gets the default manager, a pasted secret is refused without being
// echoed, a sensitive secret_ref is masked, and a credential-looking field
// name must be a secret_ref (RESOURCES 3.1, 3.5, 3.8).
func TestResourceSecretRef(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	if s := opstest.Text(h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"ssh","type":"secret_ref"}`)); !strings.Contains(s, "internal by default") {
		t.Fatalf("field add %q", s)
	}
	if s := opstest.Text(h.Must(settings.OpKindShow, `{"id":"host"}`)); !strings.Contains(s, "internal") {
		t.Fatalf("kind show %q", s)
	}
	if _, err := h.RunYes(settings.OpFieldAdd, `{"kind":"host","field":"db_password","type":"text"}`); !errs.Has(err, errs.ResourceInvalid) {
		t.Fatalf("credential name as text: %v", err)
	}
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"keys","type":"secret_ref","no_internal":true,"sensitive":true}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h1","set":["ssh=acme/h1/ssh_deploy","keys=op://vault/h1"]}`)
	s := opstest.Text(h.Must(settings.OpResShow, `{"id":"h1"}`))
	if !strings.Contains(s, "ssh = gopass://acme/h1/ssh_deploy") || !strings.Contains(s, "keys = •••") {
		t.Fatalf("show %q", s)
	}
	pasted := "aB3dE5fG7hJ9kL2mN4pQ"
	_, err := h.RunYes(settings.OpResEdit, `{"id":"h1","set":["ssh=gopass://acme/`+pasted+`"]}`)
	if !errs.Has(err, errs.SecretDetected) || strings.Contains(err.Error(), pasted) {
		t.Fatalf("pasted secret: %v", err)
	}
	if _, err = h.RunYes(settings.OpResEdit, `{"id":"h1","set":["ssh=gopass://acme/has space"]}`); !errs.Has(err, errs.SecretDetected) {
		t.Fatalf("space: %v", err)
	}
}
