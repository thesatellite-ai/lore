package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceBulkEditCheck: edit --where repoints matching rows in one
// batch (dry run writes nothing), --archive and check --where stamp every
// match, non-matching rows stay, and --meta is refused in the bulk form.
func TestResourceBulkEditCheck(t *testing.T) {
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"host"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"provider","type":"text"}`},
		[2]string{settings.OpKindAdd, `{"kind":"service"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"service","field":"runs_on","type":"ref","ref_kind":"host"}`},
	)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h1","set":["provider=hetzner"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h2","set":["provider=aws"]}`)
	for _, n := range []string{"a", "b"} {
		h.MustYes(settings.OpResAdd, `{"kind":"service","name":"`+n+`","set":["runs_on=h1"]}`)
	}
	h.MustYes(settings.OpResAdd, `{"kind":"service","name":"c","set":["runs_on=h2"]}`)

	dry := opstest.Text(h.Must(settings.OpResEdit, `{"kind":"service","where":["runs_on=h1"],"set":["runs_on=h2"],"dry_run":true}`))
	if !strings.Contains(dry, "would edit 2 service") || !strings.Contains(dry, "runs_on h1 → h2") {
		t.Fatalf("dry run %q", dry)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"a"}`)); !strings.Contains(s, "runs_on = h1") {
		t.Fatalf("dry run wrote: %q", s)
	}
	got := opstest.Text(h.MustYes(settings.OpResEdit, `{"kind":"service","where":["runs_on=h1"],"set":["runs_on=h2"]}`))
	if !strings.Contains(got, "edit 2 service resources; batch bat_") {
		t.Fatalf("bulk edit %q", got)
	}
	for _, n := range []string{"a", "b", "c"} {
		if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"`+n+`"}`)); !strings.Contains(s, "runs_on = h2") {
			t.Fatalf("%s after bulk edit %q", n, s)
		}
	}
	if _, err := h.RunYes(settings.OpResEdit, `{"kind":"service","where":["runs_on=h2"],"meta":["k=v"]}`); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("bulk --meta: %v", err)
	}
	if s := opstest.Text(h.MustYes(settings.OpResCheck, `{"kind":"host","where":["provider=hetzner"]}`)); !strings.Contains(s, "check 1 host resources") {
		t.Fatalf("bulk check %q", s)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"h1"}`)); !strings.Contains(s, "(by manual)") {
		t.Fatalf("h1 not checked %q", s)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"h2"}`)); strings.Contains(s, "checked") {
		t.Fatalf("h2 must not be checked %q", s)
	}
	h.MustYes(settings.OpResEdit, `{"kind":"host","where":["provider=aws"],"archive":true}`)
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"h2"}`)); !strings.Contains(s, "archived") {
		t.Fatalf("h2 not archived %q", s)
	}
}
