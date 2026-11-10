package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceDurationGrammar (#22b): a duration field takes the same
// forms as --remind-before (30d, 2w beside Go's 1h30m) in --set,
// --default and --max; a refusal names the accepted form; text reads
// back trimmed (1h30m, 720h, no nine zero digits) while JSON keeps the
// stored form.
func TestResourceDurationGrammar(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"job"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"job","field":"timeout","type":"duration","max":"60d"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"job","field":"grace","type":"duration","default":"2w"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"job","field":"due","type":"datetime","remind_before":"2w"}`)

	for _, c := range []struct {
		set  string
		code errs.Code
		want string
	}{
		{"timeout=3x", errs.ResourceInvalid, `timeout "3x" is not a duration (want e.g. 30d, 2w or 1h30m)`},
		{"timeout=1d12h", errs.ResourceInvalid, "want e.g. 30d, 2w or 1h30m"},
		{"timeout=90d", errs.ResourceInvalid, "timeout: 2160h is above the maximum 60d"},
		{"due=tomorrow", errs.ResourceInvalid, "want RFC 3339, e.g. 2026-11-07T10:00:00Z"},
	} {
		_, err := h.RunYes(settings.OpResAdd, `{"kind":"job","name":"bad","set":["`+c.set+`"]}`)
		if !errs.Has(err, c.code) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v; want %s naming %q", c.set, err, c.code, c.want)
		}
	}
	if _, err := h.RunYes(settings.OpFieldAdd, `{"kind":"job","field":"x","type":"duration","default":"3y"}`); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "want e.g. 30d") {
		t.Errorf("--default 3y: %v", err)
	}

	add := opstest.Text(h.MustYes(settings.OpResAdd, `{"kind":"job","name":"nightly","set":["timeout=30d","due=2026-11-07T10:00:00.5+02:00"]}`))
	h.MustYes(settings.OpResAdd, `{"kind":"job","name":"hourly","set":["timeout=1h30m"]}`)
	if !strings.Contains(add, "due = 2026-11-07T08:00:00.5Z") || !strings.Contains(add, "grace = 336h") || !strings.Contains(add, "timeout = 720h") {
		t.Errorf("add text %q", add)
	}
	show := opstest.Text(h.Must(settings.OpResShow, `{"id":"nightly"}`))
	for _, want := range []string{"  timeout = 720h\n", "  grace = 336h\n", "  due = 2026-11-07T08:00:00.5Z\n"} {
		if !strings.Contains(show, want) {
			t.Errorf("show text lacks %q:\n%s", want, show)
		}
	}
	list := opstest.Text(h.Must(settings.OpResList, `{"kind":"job"}`))
	if !strings.Contains(list, "\t1h30m\t") || strings.Contains(list, "0m0s") || strings.Contains(list, ".000") {
		t.Errorf("list text %q", list)
	}
	// JSON keeps the stored form (a wire contract).
	js := h.JSON(h.Must(settings.OpResShow, `{"id":"nightly"}`))
	if !strings.Contains(js, `"timeout":"720h0m0s"`) || !strings.Contains(js, `"due":"2026-11-07T08:00:00.500000000Z"`) {
		t.Errorf("json %s", js)
	}
}
