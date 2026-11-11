package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

const memAdd = "memory.add"

// TestMetaEverydayRules: required_on refuses an add without the key (even
// one with no --meta at all), a default fills it, a pattern warns on a
// normal key and refuses on a strict one, min/max bound numbers,
// deprecation warns (METADATA 3.19).
func TestMetaEverydayRules(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"jira","type":"text"}`)
	h.MustYes(settings.OpMetaEdit, `{"id":"jira","required_on":"memories","pattern":"^HUB-[0-9]+$"}`)
	h.Fails(memAdd, `{"body":"no meta at all"}`, errs.MetaRequired)
	res := h.MustYes(memAdd, `{"body":"bad pattern","meta":["jira=X-1"]}`)
	if got := h.JSON(res); !strings.Contains(got, "does not match the pattern") {
		t.Errorf("pattern warning missing: %s", got)
	}
	h.MustYes(settings.OpMetaEdit, `{"id":"jira","strict":true}`)
	h.Fails(memAdd, `{"body":"strict pattern","meta":["jira=X-2"]}`, errs.MetaValue)
	h.MustYes(memAdd, `{"body":"good","meta":["jira=HUB-1"]}`)
	// a default fills the required key on an add without --meta
	h.MustYes(settings.OpMetaEdit, `{"id":"jira","default":"HUB-0","default_on":"memories"}`)
	id := h.ID(h.MustYes(memAdd, `{"body":"defaulted"}`))
	if got := opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+id+`"}`)); got != "jira\tHUB-0\n" {
		t.Errorf("default not applied: %q", got)
	}
	h.MustYes(settings.OpMetaAdd, `{"name":"hours","type":"number","strict":true}`)
	h.MustYes(settings.OpMetaEdit, `{"id":"hours","min":"0","max":"200"}`)
	h.Fails(settings.OpMetaSet, `{"id":"`+id+`","pair":"hours=201"}`, errs.MetaValue)
	h.MustYes(settings.OpMetaSet, `{"id":"`+id+`","pair":"hours=8"}`)
	h.Fails(settings.OpMetaEdit, `{"id":"hours","min":"9","max":"1"}`, errs.InvalidInput)
	h.Fails(settings.OpMetaEdit, `{"id":"jira","min":"1"}`, errs.InvalidInput)
	h.Fails(settings.OpMetaEdit, `{"id":"jira","required_on":"actors"}`, errs.InvalidInput)
}

// TestMetaMaintenance: --unused and forget (refused while used), value
// label/remove (refused while used), bulk set/unset --where with one batch,
// fold with --merge-into, export → import round trip, lint.
func TestMetaMaintenance(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"env","type":"text","values":"prod,dev"}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"unused_one","type":"text"}`)
	a := h.ID(h.MustYes(memAdd, `{"body":"a","meta":["client=acme","env=prod"]}`))
	b := h.ID(h.MustYes(memAdd, `{"body":"b","meta":["client=acme"]}`))
	h.MustYes(memAdd, `{"body":"c","meta":["client=other"]}`)

	if got := opstest.Text(h.Must(settings.OpMetaKeys, `{"unused":true}`)); !strings.Contains(got, "unused_one") || strings.Contains(got, "client") {
		t.Errorf("unused %q", got)
	}
	h.Fails(settings.OpMetaForget, `{"keys":"client","confirm":true}`, errs.MetaInUse)
	h.Fails(settings.OpMetaForget, `{"keys":"unused_one"}`, errs.NeedsConfirm)
	h.MustYes(settings.OpMetaForget, `{"keys":"unused_one","confirm":true}`)
	h.Fails(settings.OpMetaEdit, `{"id":"unused_one","strict":true}`, errs.NotFound)

	h.MustYes(settings.OpMetaEdit, `{"id":"env","value_label":["prod=Production"]}`)
	h.Fails(settings.OpMetaEdit, `{"id":"env","remove_value":"prod"}`, errs.MetaInUse)
	h.MustYes(settings.OpMetaEdit, `{"id":"env","remove_value":"dev"}`)

	dry := opstest.Text(h.MustYes(settings.OpMetaSet, `{"id":"needs_ticket:bool=true","where":["client=acme"],"dry_run":true}`))
	if !strings.Contains(dry, "would set needs_ticket on 2 rows") || !strings.Contains(dry, a) || !strings.Contains(dry, b) {
		t.Errorf("dry run %q", dry)
	}
	set := opstest.Text(h.MustYes(settings.OpMetaSet, `{"id":"needs_ticket:bool=true","where":["client=acme"]}`))
	if !strings.Contains(set, "on 2 rows; batch bat_") {
		t.Errorf("bulk set %q", set)
	}
	un := opstest.Text(h.MustYes(settings.OpMetaUnset, `{"id":"needs_ticket","where":["needs_ticket?"]}`))
	if !strings.Contains(un, "remove needs_ticket on 2 rows") {
		t.Errorf("bulk unset %q", un)
	}

	h.MustYes(settings.OpMetaAdd, `{"name":"customer","type":"text"}`)
	h.MustYes(settings.OpMetaSet, `{"id":"`+a+`","pair":"customer=zeta"}`)
	h.Fails(settings.OpMetaEdit, `{"id":"client","merge_into":"customer"}`, errs.MetaConflict)
	h.MustYes(settings.OpMetaSet, `{"id":"`+a+`","pair":"customer=acme"}`)
	h.Fails(settings.OpMetaEdit, `{"id":"client","merge_into":"client"}`, errs.InvalidInput)
	fold := opstest.Text(h.MustYes(settings.OpMetaEdit, `{"id":"client","merge_into":"customer"}`))
	if !strings.Contains(fold, "3 rows rewritten") {
		t.Errorf("fold %q", fold)
	}
	if got := opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+b+`"}`)); got != "customer\tacme\n" {
		t.Errorf("after fold %q", got)
	}

	exp := h.Must(settings.OpMetaExport, `{}`)
	csv := opstest.Text(exp)
	if !strings.HasPrefix(csv, "id,key,value\n") || !strings.Contains(csv, a+",env,prod\n") {
		t.Errorf("export %q", csv)
	}
	file := filepath.Join(t.TempDir(), "m.csv")
	if err := os.WriteFile(file, []byte("id,key,value\n"+b+",customer,beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.MustYes(settings.OpMetaImport, `{"file":`+opstest.Quote(filepath.ToSlash(file))+`}`)
	if got := opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+b+`"}`)); got != "customer\tbeta\n" {
		t.Errorf("after import %q", got)
	}

	h.MustYes(settings.OpMetaEdit, `{"id":"customer","pattern":"^a"}`)
	if got := opstest.Text(h.MustYes(settings.OpMetaLint, `{}`)); !strings.Contains(got, `"beta" does not match the pattern ^a`) {
		t.Errorf("lint %q", got)
	}
}
