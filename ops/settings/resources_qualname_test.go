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

// TestResourceQualifiedNames (RES 3.4): every resource lookup accepts the
// qualified name <place>:<kind>/<name> that `resource add` prints, the
// short form <kind>/<name>, the bare name and the id; a qualified name
// with the wrong place or kind is E_NOT_FOUND; a ref value takes the
// qualified name too.
func TestResourceQualifiedNames(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"domain"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"domain","field":"points_to","type":"ref","ref_kind":"host"}`)
	add := opstest.Text(h.MustYes(settings.OpResAdd, `{"kind":"host","name":"hub_prod"}`))
	if !strings.Contains(add, "repo:host/hub_prod") {
		t.Fatalf("add label: %q", add)
	}
	id := h.ID(h.Must(settings.OpResShow, `{"id":"hub_prod"}`))
	if !strings.HasPrefix(id, "res_") {
		t.Fatalf("show id %q", id)
	}
	for _, key := range []string{"repo:host/hub_prod", "host/hub_prod", "hub_prod", id} {
		if got := h.ID(h.Must(settings.OpResShow, `{"id":"`+key+`"}`)); got != id {
			t.Errorf("show %q: id %q, want %q", key, got, id)
		}
	}
	for _, key := range []string{"org:host/hub_prod", "repo:domain/hub_prod", "repo:host/hub_stag", "domain/hub_prod"} {
		h.Fails(settings.OpResShow, `{"id":"`+key+`"}`, errs.NotFound)
	}
	h.MustYes(settings.OpResAdd, `{"kind":"domain","name":"hub_web","set":["points_to=repo:host/hub_prod"]}`)
	if got := opstest.Text(h.Must(settings.OpResShow, `{"id":"repo:domain/hub_web"}`)); !strings.Contains(got, "hub_prod") {
		t.Errorf("ref by qualified name: %q", got)
	}
}

// TestResourceAmbiguousAcrossLevels (RES 3.4, BUGFIX-11): a bare name held
// by resources at two levels is E_RESOURCE_AMBIGUOUS whose hint names both
// qualified names, never a silent pick of the nearest; so is a bare name
// held by two kinds in one place; the qualified names and the ids each
// resolve to their own row, and a ref field fixed to one kind still sees
// the level ambiguity.
func TestResourceAmbiguousAcrossLevels(t *testing.T) {
	t.Parallel()
	h := opstest.WithStores(t, []string{"infra"}, ownRepo)
	h.MustYes(settings.OpKindAdd, `{"kind":"server","to":"infra"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"service","to":"infra"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"runs_on","type":"ref","ref_kind":"server"}`)
	repo := h.ID(h.MustYes(settings.OpResAdd, `{"kind":"server","name":"hub_db"}`))
	store := h.ID(h.MustYes(settings.OpResAdd, `{"kind":"server","name":"hub_db","to":"infra"}`))
	if repo == store || !strings.HasPrefix(repo, "res_") || !strings.HasPrefix(store, "res_") {
		t.Fatalf("ids %q %q", repo, store)
	}
	_, err := h.Run(settings.OpResShow, `{"id":"hub_db"}`)
	e := errs.EnvelopeOf(err).Error
	if !errs.Has(err, errs.ResourceAmbiguous) || !strings.Contains(e.Hint, "repo:server/hub_db") || !strings.Contains(e.Hint, "infra:server/hub_db") {
		t.Fatalf("bare name at two levels: %v hint %q", err, e.Hint)
	}
	_, err = h.RunYes(settings.OpResAdd, `{"kind":"service","name":"api","set":["runs_on=hub_db"]}`)
	if !errs.Has(err, errs.ResourceAmbiguous) {
		t.Errorf("ref to a bare name at two levels: %v", err)
	}
	for key, want := range map[string]string{"repo:server/hub_db": repo, "infra:server/hub_db": store, repo: repo, store: store} {
		if got := h.ID(h.Must(settings.OpResShow, `{"id":"`+key+`"}`)); got != want {
			t.Errorf("show %q: %q, want %q", key, got, want)
		}
	}
	h.MustYes(settings.OpResAdd, `{"kind":"service","name":"cache"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"server","name":"cache"}`)
	_, err = h.Run(settings.OpResShow, `{"id":"cache"}`)
	if e := errs.EnvelopeOf(err).Error; !errs.Has(err, errs.ResourceAmbiguous) || !strings.Contains(e.Hint, "repo:service/cache") || !strings.Contains(e.Hint, "repo:server/cache") {
		t.Errorf("bare name in two kinds: %v hint %q", err, e.Hint)
	}
}

// TestResourceImportQualifiedRefs (RES 3.4/3.20, FB-RES-1): a ref between
// two rows of one import takes the qualified name of the row the same file
// creates; a bare name two rows of the file share is refused with
// E_RESOURCE_AMBIGUOUS naming the qualified names, and nothing is written.
func TestResourceImportQualifiedRefs(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"box"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"peer","type":"ref","ref_kind":"host"}`)
	file := func(body string) string {
		p := filepath.Join(t.TempDir(), "in.json")
		if err := os.WriteFile(p, []byte(`{"resources":[`+body+`]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return `{"file":"` + p + `"}`
	}
	h.MustYes(settings.OpResImport, file(`{"kind":"host","name":"a","fields":{"peer":"repo:host/b"}},{"kind":"host","name":"b"}`))
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"a"}`)); !strings.Contains(s, "peer = b") {
		t.Errorf("qualified ref inside one import: %q", s)
	}
	_, err := h.RunYes(settings.OpResImport, file(`{"kind":"host","name":"c","fields":{"peer":"x"}},{"kind":"host","name":"x"},{"kind":"host","name":"x"}`))
	if !errs.Has(err, errs.ResourceAmbiguous) || !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "repo:host/x") {
		t.Errorf("same name twice in one import: %v hint %q", err, errs.EnvelopeOf(err).Error.Hint)
	}
	h.Fails(settings.OpResShow, `{"id":"c"}`, errs.NotFound)
}
