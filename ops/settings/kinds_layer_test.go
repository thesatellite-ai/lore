package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestKindLayering: an org kind is extended from a nearer folder (a new field and
// a tightening extension row), the effective kind shows both, the repo's
// resources must honour them, and every loosening or retype from a nearer
// level is E_KIND_CONFLICT before anything is written (RES 3.6, RQ9).
func TestKindLayering(t *testing.T) {
	h := multiStore(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host","to":"kb/org"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"region","type":"text","to":"kb/org"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"ip","type":"ip","sensitive":true}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"provider","type":"text","values":"aws,gcp"}`)
	// a nearer level adds a field
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"ec2_id","type":"text","to":"kb/web"}`)
	// a nearer level tightens: required + a value
	out := h.MustYes(settings.OpFieldEdit, `{"kind":"host","field":"region","required":true,"to":"kb/web"}`)
	if !strings.Contains(h.JSON(out), "fld_") {
		t.Fatalf("extension write %s", h.JSON(out))
	}
	h.MustYes(settings.OpFieldEdit, `{"kind":"host","field":"provider","add_values":["linode"],"to":"kb/web"}`)
	show := h.JSON(h.Must(settings.OpKindShow, `{"id":"host"}`))
	for _, want := range []string{`"ec2_id"`, `"linode"`} {
		if !strings.Contains(show, want) {
			t.Errorf("effective kind lacks %s: %s", want, show)
		}
	}
	h.Fails(settings.OpResAdd, `{"kind":"host","name":"web1","set":["provider=aws"],"to":"kb/web"}`, errs.ResourceInvalid)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"web1","set":["provider=linode","region=eu"],"to":"kb/web"}`)
	checkRefusals(t, h, []refusal{
		{settings.OpFieldEdit, `{"kind":"host","field":"ip","no_sensitive":true,"to":"kb/web"}`, errs.KindConflict, "may not unmark sensitive"},
		{settings.OpFieldEdit, `{"kind":"host","field":"region","no_required":true,"to":"kb/web"}`, errs.KindConflict, "make it optional"},
		{settings.OpFieldEdit, `{"kind":"host","field":"region","required":true,"to":"agency/org"}`, errs.KindConflict, "nearer place"},
		{settings.OpFieldAdd, `{"kind":"host","field":"zone","type":"text","to":"agency/org"}`, errs.KindConflict, "nearer level"},
		{settings.OpKindAdd, `{"kind":"host","to":"kb/web"}`, errs.KindConflict, "may extend it"},
	})
	// an outer change that would break the nearer extension is refused
	h.MustYes(settings.OpFieldEdit, `{"kind":"host","field":"region","pattern":"^[a-z]+$","to":"kb/web"}`)
	h.Fails(settings.OpFieldEdit, `{"kind":"host","field":"region","pattern":"^x"}`, errs.KindConflict)
}

// TestAdoptAndRehome: adopt moves a place's kind and field rows (ids kept,
// asks first, refuses a name defined under another id), and rehome lists
// a kind clash in the view without writing (RES 3.6, R6).
func TestAdoptAndRehome(t *testing.T) {
	h := multiStore(t)
	id := h.ID(h.MustYes(settings.OpKindAdd, `{"kind":"svc","to":"kb/web"}`))
	h.MustYes(settings.OpFieldAdd, `{"kind":"svc","field":"port","type":"number"}`)
	h.Fails(settings.OpResAdopt, `{"from":"kb/web","to":"kb/org"}`, errs.NeedsConfirm)
	out := h.JSON(h.MustYes(settings.OpResAdopt, `{"from":"kb/web","to":"kb/org"}`))
	if !strings.Contains(out, `"fields":1`) {
		t.Fatalf("adopt %s", out)
	}
	show := h.JSON(h.Must(settings.OpKindShow, `{"id":"svc"}`))
	if !strings.Contains(show, id) || !strings.Contains(show, `"store":"kb/org"`) {
		t.Errorf("after adopt %s", show)
	}
	if got := h.JSON(h.Must(settings.OpResRehome, `{}`)); !strings.Contains(got, `"clashes":[]`) {
		t.Errorf("rehome %s", got)
	}
}
