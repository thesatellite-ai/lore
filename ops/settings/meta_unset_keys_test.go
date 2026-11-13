package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestMetaUnsetKeyFlagNamesAll (U-4): `lore meta unset --id <id> --key k`
// fills "keys" only (the first key's positional is a quiet alias of
// --key), and that alone names the key: --key is every key to remove, not
// only "more keys". The positional key still works, both mix, and no key
// at all is refused.
func TestMetaUnsetKeyFlagNamesAll(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := h.ID(h.MustYes(memAdd, `{"body":"a","meta":["cost_hours=3","client=acme","team_owner=x","region=eu"]}`))
	get := func() string { return opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+id+`"}`)) }

	h.MustYes(settings.OpMetaUnset, `{"id":"`+id+`","keys":["cost_hours"]}`)
	if got := get(); strings.Contains(got, "cost_hours") {
		t.Fatalf("--key alone left the key:\n%s", got)
	}
	h.MustYes(settings.OpMetaUnset, `{"id":"`+id+`","keys":["client","team_owner"]}`)
	if got := get(); strings.Contains(got, "client") || strings.Contains(got, "team_owner") {
		t.Fatalf("repeated --key left a key:\n%s", got)
	}
	h.MustYes(settings.OpMetaUnset, `{"id":"`+id+`","key":"region"}`)
	if got := get(); strings.Contains(got, "region") {
		t.Fatalf("the positional key left it:\n%s", got)
	}
	msg := h.Fails(settings.OpMetaUnset, `{"id":"`+id+`"}`, errs.InvalidInput)
	if !strings.Contains(msg, "name the key") {
		t.Fatalf("no key: %s", msg)
	}
}
