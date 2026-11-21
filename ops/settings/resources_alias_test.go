package settings_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceAliases (devco D-817): a nickname, a full name and a role
// are first-class other names of a resource. alias add stores them, every
// lookup finds the resource by one (any case, bare or <kind>/<alias>),
// show and list print them, a name another resource of the kind already
// uses is refused, remove drops one, and a name a letter off is answered
// with the resource it is near.
func TestResourceAliases(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"person"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"person","name":"robert_kim"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"person","name":"hugo_berg"}`)

	// before any alias: the nickname names nothing
	h.Fails(settings.OpResShow, `{"id":"Bobby"}`, errs.NotFound)

	out := opstest.Text(h.MustYes(settings.OpResAliasAdd, `{"id":"robert_kim","kind":"person","alias":["Bobby","Robert Kim","CFO"]}`))
	if !strings.Contains(out, "alias Bobby, Robert Kim, CFO for person/robert_kim") {
		t.Fatalf("alias add: %s", out)
	}
	for _, key := range []string{"Bobby", "bobby", "person/Bobby", "robert kim", "CFO"} {
		res := h.Must(settings.OpResShow, `{"id":`+opstest.Quote(key)+`}`)
		if txt := opstest.Text(res); !strings.Contains(txt, "person/robert_kim") || !strings.Contains(txt, "aliases: Bobby, Robert Kim, CFO") {
			t.Errorf("show %q:\n%s", key, txt)
		}
		if j := h.JSON(res); !strings.Contains(j, `"aliases":["Bobby","Robert Kim","CFO"]`) {
			t.Errorf("show %q json: %s", key, j)
		}
	}
	// an edit through the alias edits robert_kim
	h.MustYes(settings.OpResCheck, `{"id":"Bobby"}`)

	// adding one again writes nothing; another person cannot take it
	if again := opstest.Text(h.MustYes(settings.OpResAliasAdd, `{"id":"robert_kim","alias":["bobby"]}`)); !strings.Contains(again, "nothing written") {
		t.Errorf("re-add: %s", again)
	}
	if msg := h.Fails(settings.OpResAliasAdd, `{"id":"hugo_berg","alias":["CFO"]}`, errs.ResourceTaken); !strings.Contains(msg, "person/robert_kim") {
		t.Errorf("taken: %s", msg)
	}
	h.Fails(settings.OpResAliasAdd, `{"id":"hugo_berg","alias":["robert_kim"]}`, errs.ResourceTaken)
	h.Fails(settings.OpResAliasAdd, `{"id":"hugo_berg","alias":["hugo_berg"]}`, errs.InvalidInput)
	h.Fails(settings.OpResAliasAdd, `{"id":"hugo_berg","alias":["  "]}`, errs.InvalidInput)

	// list: every resource with an alias
	if l := opstest.Text(h.Must(settings.OpResAliasList, `{"kind":"person"}`)); !strings.Contains(l, "person/robert_kim  Bobby, Robert Kim, CFO") || strings.Contains(l, "hugo_berg") {
		t.Errorf("list:\n%s", l)
	}

	// a typo of a name or alias: not found, naming the near resource
	hint := func(key string) string {
		_, err := h.Run(settings.OpResShow, `{"id":`+opstest.Quote(key)+`}`)
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.NotFound {
			t.Fatalf("show %q: %v, want E_NOT_FOUND", key, err)
		}
		return e.Hint
	}
	if got := hint("robert_kin"); !strings.Contains(got, "did you mean person/robert_kim?") {
		t.Errorf("typo hint: %s", got)
	}
	if got := hint("Boby"); !strings.Contains(got, "person/robert_kim (alias bobby)") {
		t.Errorf("alias typo hint: %s", got)
	}

	// remove
	h.MustYes(settings.OpResAliasRemove, `{"id":"robert_kim","alias":["CFO"]}`)
	h.Fails(settings.OpResShow, `{"id":"CFO"}`, errs.NotFound)
	h.Fails(settings.OpResAliasRemove, `{"id":"robert_kim","alias":["CFO"]}`, errs.NotFound)
	h.MustYes(settings.OpResAliasAdd, `{"id":"hugo_berg","alias":["CFO"]}`) // free again
	if txt := opstest.Text(h.Must(settings.OpResShow, `{"id":"cfo"}`)); !strings.Contains(txt, "person/hugo_berg") {
		t.Errorf("moved alias:\n%s", txt)
	}
}
