package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestFieldRuleOptions: RES 3.1 field options — a default fills an add, a
// pattern and min/max refuse values outside them, a bad definition is
// refused, a deprecated field warns naming its replacement, a loosening
// asks, and --secret-manager changes what a bare secret_ref path means.
func TestFieldRuleOptions(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"service","secret_manager":"op"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"port","type":"number","min":"1","max":"65535","default":"8080"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"code","type":"text","pattern":"^[A-Z]{3}$"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"repo","type":"text"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"git","type":"text","deprecated":true,"replaced_by":"repo"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"keys","type":"secret_ref"}`)
	for _, bad := range []string{
		`{"kind":"service","field":"a1","type":"text","min":"3"}`,
		`{"kind":"service","field":"a2","type":"number","pattern":"x"}`,
		`{"kind":"service","field":"a3","type":"number","default":"abc"}`,
		`{"kind":"service","field":"a4","type":"number","min":"9","max":"1"}`,
		`{"kind":"service","field":"a5","type":"text","remind_before":"30d"}`,
		`{"kind":"service","field":"a6","type":"text","replaced_by":"repo"}`,
	} {
		if _, err := h.RunYes(settings.OpFieldAdd, bad); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: want E_INVALID_INPUT, got %v", bad, err)
		}
	}
	got := opstest.Text(h.MustYes(settings.OpResAdd, `{"kind":"service","name":"api","set":["git=x","keys=infra/api"]}`))
	if !strings.Contains(got, "default: port") || !strings.Contains(got, "service.git is deprecated; use repo") {
		t.Fatalf("add %q", got)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"api"}`)); !strings.Contains(s, "op://infra/api") {
		t.Fatalf("secret manager %q", s)
	}
	for _, set := range []string{"port=0", "port=70000", "code=ab"} {
		if _, err := h.RunYes(settings.OpResEdit, `{"id":"api","set":["`+set+`"]}`); !errs.Has(err, errs.ResourceInvalid) {
			t.Errorf("%s: want E_RESOURCE_INVALID, got %v", set, err)
		}
	}
	h.MustYes(settings.OpResEdit, `{"id":"api","set":["code=ABC"]}`)
	if _, err := h.Run(settings.OpFieldEdit, `{"kind":"service","field":"port","max":"70000"}`); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("loosening must ask: %v", err)
	}
	if s := opstest.Text(h.Must(settings.OpKindShow, `{"id":"service"}`)); !strings.Contains(s, "min 1") || !strings.Contains(s, "replaced by repo") {
		t.Fatalf("kind show %q", s)
	}
}
