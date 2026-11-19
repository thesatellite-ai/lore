package stores

// doctor_scope_test.go — devco D-687: `store doctor --setup <company>`
// listed another setup's store clone (the ops store) with its warning, and
// `store doctor --store <org>` printed "stores this repo and machine use:
// none" and then checked that store's clone anyway.

import (
	"strings"
	"testing"
)

func scopeFixture() ClonesOut {
	return ClonesOut{
		doctor:  true,
		Checked: []StoreCheck{{Store: "acme", Source: "org", Kind: StoreKindGit, URL: "git@github.com:acme-co/lore-team.git", Shown: "git@github.com:acme-co/lore-team.git"}},
		Stores: []CloneView{
			{URL: "git@github.com:acme-co/lore-team.git", Shown: "git@github.com:acme-co/lore-team.git", Entries: []string{"acme"}},
			{URL: "git@github.com:acme-co/lore-ops.git", Shown: "git@github.com:acme-co/lore-ops.git", Entries: []string{"acmeops"}, Warnings: []string{"a leftover branch"}},
		},
	}
}

func TestDoctorOrgScopeKeepsTheNamedSetupsClones(t *testing.T) {
	out := orgScoped(scopeFixture(), []string{"acme"})
	if len(out.Stores) != 1 || out.Stores[0].Entries[0] != "acme" {
		t.Fatalf("scoped to acme: %+v, want only acme's clone", out.Stores)
	}
	if text := out.Text(); strings.Contains(text, "lore-ops") || strings.Contains(text, "leftover") {
		t.Fatalf("scoped report names another setup's store:\n%s", text)
	}
	if all := orgScoped(scopeFixture(), nil); len(all.Stores) != 2 {
		t.Fatalf("no scope dropped clones: %+v", all.Stores)
	}
}

func TestDoctorStoreFilterNeverSaysNoneThenChecks(t *testing.T) {
	in := scopeFixture()
	in.Checked = nil // the named store is no store this repo or machine uses
	out, err := onlyStore(in, "acmeops")
	if err != nil {
		t.Fatal(err)
	}
	out.only = "acmeops"
	text := out.Text()
	if strings.Contains(text, doctorUsedHead) || strings.Contains(text, doctorNoneUsed) {
		t.Fatalf("--store acmeops says no store is used, then checks it:\n%s", text)
	}
	if !strings.Contains(text, "lore-ops") {
		t.Fatalf("--store acmeops does not check its clone:\n%s", text)
	}
	// with no filter an empty first section still says none
	in.Stores = nil
	if text := in.Text(); !strings.Contains(text, doctorNoneUsed) {
		t.Fatalf("unfiltered empty report lost its none line:\n%s", text)
	}
}
