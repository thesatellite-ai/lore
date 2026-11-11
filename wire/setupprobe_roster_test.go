package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// doctorAllText is `lore doctor --all --fresh`'s text, run in dir as home.
func doctorAllText(t *testing.T, dir, home string) string {
	t.Helper()
	app, err := New(StandaloneOptions(dir, home, dir, "test"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	e, ok := app.Registry().Lookup(opsdoctor.OpRun)
	if !ok {
		t.Fatalf("no op %s", opsdoctor.OpRun)
	}
	res, err := app.Run(background(), e, op.JSONDecoder{Body: []byte(`{"all":true,"fresh":true}`)}, op.Caller{})
	if err != nil {
		t.Fatalf("doctor --all in %s: %v", dir, err)
	}
	txt, _ := res.Text()
	return txt
}

// identityLine is the "identity" area line of a doctor --all text.
func identityLine(t *testing.T, txt string) string {
	t.Helper()
	for _, l := range strings.Split(txt, "\n") {
		if strings.Contains(l, " identity ") {
			return strings.TrimSpace(l)
		}
	}
	t.Fatalf("no identity line in:\n%s", txt)
	return ""
}

// TestDoctorAllReadsRosterAndPeriod (owner report, lore 0.1.18-dev):
// doctor --all printed "roster – · period –" for a person `lore actor
// show` listed with a roster row and an open employee period, because the
// setup prober never read the roster. It now reads the org's roster store
// as actor show does: the line names the roster handle and the open
// period, inside a client repo linked with `org link` and outside any
// repo, also when the org's first store is not the one holding the roster
// (an org with several stores). A period that ended is a warning naming
// the day it ended.
func TestDoctorAllReadsRosterAndPeriod(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newOrgWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira")
	h := orgHostAt(t, w.repo, mira)
	if _, err := h.Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := h.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "acme"}, true); err != nil {
		t.Fatalf("org link: %v", err)
	}

	// The open employee period starts on a day of its own, so the line
	// can only show it by reading the period row.
	app, err := New(StandaloneOptions(w.repo, mira, w.repo, "test"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, app, profilestore.OpPeriodEdit, `{"period":"@mira","started_on":"2025-03-01"}`, true); err != nil {
		t.Fatalf("period edit: %v", err)
	}
	show, err := run(t, app, opsinfra.OpActorShow, `{"id":"@mira"}`, false)
	_ = app.Close()
	if err != nil || !strings.Contains(show, `"started_on":"2025-03-01"`) {
		t.Fatalf("actor show @mira: %v\n%s", err, show)
	}

	// Several stores, the roster's not first, and a setup for the org.
	cfg, err := os.ReadFile(configPath(mira))
	if err != nil {
		t.Fatal(err)
	}
	const rosterStore = "      - url: https://github.com/agency/notes\n"
	if !strings.Contains(string(cfg), rosterStore) {
		t.Fatalf("config.yaml has no agency/notes store entry:\n%s", cfg)
	}
	text := strings.Replace(string(cfg), rosterStore, "      - url: https://github.com/agency/empty\n"+rosterStore, 1)
	text += "setups:\n  - name: agency\n    kind: admin\n    org: agency\n    default: true\n    personal_store: none\n"
	if err := os.WriteFile(configPath(mira), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(w.root, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}

	const want = "identity    mira@agency.example · roster @mira · employee since 2025-03-01"
	for _, dir := range []string{w.repo, outside} {
		if got := identityLine(t, doctorAllText(t, dir, mira)); !strings.HasSuffix(got, want) || !strings.HasPrefix(got, "✓") {
			t.Fatalf("doctor --all in %s: identity line %q, want ✓ … %q", dir, got, want)
		}
	}

	// The stint ends: the period check warns and says when.
	app, err = New(StandaloneOptions(w.repo, mira, w.repo, "test"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = run(t, app, profilestore.OpPeriodEdit, `{"period":"@mira","ended_on":"2026-01-31","end_reason":"moved on"}`, true)
	_ = app.Close()
	if err != nil {
		t.Fatalf("period edit --ended-on: %v", err)
	}
	txt := doctorAllText(t, outside, mira)
	if got := identityLine(t, txt); !strings.HasSuffix(got, "roster @mira · period ⚠") {
		t.Fatalf("ended stint: identity line %q, want … roster @mira · period ⚠\n%s", got, txt)
	}
	if !strings.Contains(txt, "identity.period  no open period in agency: the last one (employee) ended 2026-01-31") {
		t.Fatalf("ended stint: no identity.period line naming the end day:\n%s", txt)
	}
}
