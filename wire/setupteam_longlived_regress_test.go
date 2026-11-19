package wire

// setupteam_longlived_regress_test.go — devco N-1: the setup page is one
// long-lived App. Its earlier steps (identity, personal store, the
// this-folder check) open the App's state in the org's own repo before the
// org entry exists; the team step then writes the entry, makes the org
// store and registers the admin on its roster. The roster write looked for
// the org store in the state opened before, found none ("roster: no store
// named <org>/org is attached") and every retry on the same page failed the
// same way, while `lore setup apply` (a fresh process) worked.

import (
	"context"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

func TestSetupTeamStepOnAnOpenPageApp(t *testing.T) {
	ctx := context.Background()
	w := newOrgWorld(t)
	w.bareAt(t, "agency/team")
	home := w.home(t, "ana")
	opts, res := StandaloneOptionsOrg(w.repo, home, w.repo, "test")
	app, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	// as the page serves (app/setupweb.go followConfig)
	app.FollowPlaces()
	res.FollowConfig()
	// the page's earlier steps read through this same App first
	if _, err = app.state(ctx); err != nil {
		t.Fatalf("open the page's App before the team step: %v", err)
	}
	ap, err := app.SetupApplier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ans := onboard.Answers{onboard.QIDOrg: {Text: "northwind"}, onboard.QIDStoreURL: {Text: "https://github.com/agency/team"},
		onboard.QIDEmail: {Text: "ana@agency.example"}, onboard.QIDName: {Text: "Ana"},
		onboard.QIDOwn: {List: []string{"github.com/acme/*"}, IsList: true}}
	req := opssetup.ApplyRequest{Setup: onboard.SetupEntry{Name: "northwind", Kind: onboard.KindAdmin}, Step: onboard.StepTeam, Apply: onboard.ApplyTeam, Answers: ans}
	out, err := ap.Apply(ctx, req)
	if err != nil {
		t.Fatalf("team step on an App opened before the org entry existed: %v", err)
	}
	if !out.Changed || !isOrgAdmin(home, "northwind") {
		t.Fatalf("team step = %+v, want the org created with its admin setup", out)
	}
	// the same App now sees the org store it attached (the page's next
	// reads: this-folder check, setup list, whoami)
	if _, err = app.state(ctx); err != nil {
		t.Fatal(err)
	}
	stores := app.Stores()
	found := false
	for _, s := range stores {
		if string(s) == "northwind/org" {
			found = true
		}
	}
	if !found {
		t.Fatalf("after the team step the page's App attaches %v, want northwind/org among them", stores)
	}
	// setup list's "This folder" line names the org store it attaches here,
	// not "no attached store"
	h, err := app.SetupHere(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.Stores, "northwind/org") {
		t.Fatalf("setup list here after the team step = %s (stores %v), want northwind/org named", onboard.HereLine(h), h.Stores)
	}
	// a second save of the same step on the same page is idempotent
	if _, err = ap.Apply(ctx, req); err != nil {
		t.Fatalf("team step saved again on the same page: %v", err)
	}
}
