package settings_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourcePromptNonInteractive (later 38, FA-RES-1): --prompt never
// waits without a terminal: E_NEEDS_CONFIRM naming the fields it would
// ask, deduplicated, comma lists split, @required naming only required
// fields not already given; an unknown field is E_RESOURCE_INVALID; an
// empty prompt list asks nothing and the add goes through.
func TestResourcePromptNonInteractive(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"ip","type":"text","required":true,"sensitive":true}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text","required":true}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"note","type":"text"}`)
	cases := []struct {
		name, prompt, sets string
		want               string // the fields named after "terminal: "
	}{
		{"one", `["note"]`, `["ip=1.2.3.4","os=x"]`, "note"},
		{"comma and dedupe", `["note,ip","ip"]`, `["os=x"]`, "note, ip"},
		{"required minus given", `["@required"]`, `["os=x"]`, "ip"},
		{"required all", `["@required"]`, `[]`, "ip, os"},
	}
	for _, c := range cases {
		msg := h.Fails(settings.OpResAdd, `{"kind":"host","name":"h_`+strings.ReplaceAll(c.name, " ", "_")+`","prompt":`+c.prompt+`,"set":`+c.sets+`}`, errs.NeedsConfirm)
		if !strings.Contains(msg, "--prompt needs a person at a terminal: "+c.want) {
			t.Errorf("%s: %q, want fields %q", c.name, msg, c.want)
		}
	}
	h.Fails(settings.OpResAdd, `{"kind":"host","name":"bad","prompt":["nope"],"set":["ip=1.2.3.4","os=x"]}`, errs.ResourceInvalid)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"ok","prompt":[" , "],"set":["ip=1.2.3.4","os=x"]}`)
}

// TestResourceRefusalCodes (later 38, RES 3.1/3.4): more than 200 allowed
// values is E_KIND_LIMIT with nothing written; a new ref to an archived
// resource is E_RESOURCE_ARCHIVED, its hint naming the replacement when
// repoint recorded one and another resource otherwise; no hint names an
// unarchive, because no such verb exists (INTEG-QUESTIONS-5).
func TestResourceRefusalCodes(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	vals := make([]string, 201)
	for i := range vals {
		vals[i] = "v" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + strings.Repeat("0", i/26)
	}
	msg := h.Fails(settings.OpFieldAdd, `{"kind":"host","field":"tier","type":"text","values":"`+strings.Join(vals, ",")+`"}`, errs.KindLimit)
	if !strings.Contains(msg, "over the cap of 200") {
		t.Errorf("values cap: %q", msg)
	}
	if s := opstest.Text(h.Must(settings.OpKindShow, `{"id":"host"}`)); strings.Contains(s, "tier") {
		t.Errorf("a refused field was written: %s", s)
	}

	h.MustYes(settings.OpKindAdd, `{"kind":"service"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"runs_on","type":"ref","ref_kind":"host"}`)
	for _, n := range []string{"h1", "h2", "h3"} {
		h.MustYes(settings.OpResAdd, `{"kind":"host","name":"`+n+`"}`)
	}
	h.MustYes(settings.OpResRepoint, `{"old":"h1","new":"h2"}`) // archives h1 with replaced_by h2
	h.MustYes(settings.OpResArchive, `{"id":"h3"}`)
	_, err := h.RunYes(settings.OpResAdd, `{"kind":"service","name":"s1","set":["runs_on=h1"]}`)
	if e := errs.EnvelopeOf(err).Error; !errs.Has(err, errs.ResourceArchived) || !strings.Contains(e.Hint, "ref its replacement host h2") || strings.Contains(e.Hint, "unarchive") {
		t.Errorf("ref to a repointed resource: %v hint %q", err, e.Hint)
	}
	_, err = h.RunYes(settings.OpResAdd, `{"kind":"service","name":"s2","set":["runs_on=h3"]}`)
	if e := errs.EnvelopeOf(err).Error; !errs.Has(err, errs.ResourceArchived) || e.Hint != "ref another resource of that kind" {
		t.Errorf("ref to an archived resource: %v hint %q", err, e.Hint)
	}
	// The code's description (docs/reference errors table) names no
	// unarchive either (run:tests:q-resource-unarchive).
	if d := errs.DefOf(errs.ResourceArchived).Description; strings.Contains(d, "unarchive") {
		t.Errorf("E_RESOURCE_ARCHIVED description names a verb that does not exist: %q", d)
	}
}

// TestResourceReportOffboarding (later 38, RES 3.22): report --meta
// selects only the matching resources and lists the inbound refs into
// them from resources outside the set (an archived referrer is left out);
// without --project/--meta the report has no inbound line.
func TestResourceReportOffboarding(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"client","type":"text"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"service"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"runs_on","type":"ref","ref_kind":"host"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h1","meta":["client=acme"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h2"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"service","name":"s1","set":["runs_on=h1"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"service","name":"s2","set":["runs_on=h1"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"service","name":"s3","set":["runs_on=h2"]}`)
	h.MustYes(settings.OpResArchive, `{"id":"s2"}`)

	var rep settings.ResReportOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpResReport, `{"meta_where":["client=acme"]}`))), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Total != 1 || len(rep.Inbound) != 1 || rep.Inbound[0] != "service s1.runs_on → host h1" {
		t.Fatalf("report --meta: %+v", rep)
	}
	if txt := opstest.Text(h.Must(settings.OpResReport, `{"meta_where":["client=acme"]}`)); !strings.Contains(txt, "inbound refs 1") {
		t.Fatalf("report --meta text: %s", txt)
	}
	if txt := opstest.Text(h.Must(settings.OpResReport, `{}`)); strings.Contains(txt, "inbound refs") {
		t.Fatalf("plain report has an inbound line: %s", txt)
	}
}
