package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceHistoryQuarantineReport: every value write records field
// changes (sensitive masked), retype --keep-as-text quarantines misfits and
// show/list/lint mark them, report counts kinds, unchecked and quarantined
// (RES 3.15, 3.22, OV49).
func TestResourceHistoryQuarantineReport(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"ip","type":"text","sensitive":true}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"cut_over","type":"text"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"hub","set":["os=ubuntu-22.04","ip=203.0.113.10","cut_over=soon"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"hub2","set":["cut_over=2026-12-01"]}`)
	h.MustYes(settings.OpResEdit, `{"id":"hub","set":["os=ubuntu-24.04","ip=203.0.113.11"]}`)

	hist := opstest.Text(h.Must(settings.OpResHistory, `{"id":"hub"}`))
	if !strings.Contains(hist, "os  ubuntu-22.04 → ubuntu-24.04") || !strings.Contains(hist, "os  set ubuntu-22.04") {
		t.Fatalf("history %q", hist)
	}
	if strings.Contains(hist, "203.0.113") || !strings.Contains(hist, "ip  •••") {
		t.Fatalf("history must mask sensitive values: %q", hist)
	}
	if one := opstest.Text(h.Must(settings.OpResHistory, `{"id":"hub","field":"os"}`)); strings.Contains(one, "ip") || strings.Count(one, "\n") != 2 {
		t.Fatalf("--field os %q", one)
	}

	got := opstest.Text(h.MustYes(settings.OpFieldRetype, `{"kind":"host","field":"cut_over","type":"date","keep_as_text":true}`))
	if !strings.Contains(got, "1 values converted; 1 kept as text and quarantined") {
		t.Fatalf("retype --keep-as-text %q", got)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"hub"}`)); !strings.Contains(s, "quarantined: cut_over (does not fit date)") {
		t.Fatalf("show %q", s)
	}
	if s := opstest.Text(h.Must(settings.OpResList, `{"kind":"host"}`)); !strings.Contains(s, "hub quarantined: cut_over") || strings.Contains(s, "hub2 quarantined") {
		t.Fatalf("list %q", s)
	}
	if s := opstest.Text(h.MustYes(settings.OpResLint, `{}`)); !strings.Contains(s, "quarantined") {
		t.Fatalf("lint %q", s)
	}

	rep := opstest.Text(h.Must(settings.OpResReport, `{}`))
	for _, want := range []string{"resources   2 (host 2)", "quarantined 1", "unchecked   1: host hub2"} {
		if !strings.Contains(rep, want) {
			t.Fatalf("report lacks %q: %q", want, rep)
		}
	}
}
