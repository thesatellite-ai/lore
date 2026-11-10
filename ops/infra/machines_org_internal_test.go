package infra

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/store"
)

// storesOnly is a work.Env that answers only Stores (orgRows reads
// nothing else; any other call would panic on the nil embedded Env).
type storesOnly struct {
	work.Env
	names []store.Source
}

func (s storesOnly) Stores() []store.Source { return s.names }

// TestMachinesOrgRows (OV31, CLI_REFERENCE 3.8): machine list --org keeps
// the rows of the store named <org> and its linked folders <org>/<folder>
// only — never a store whose name merely starts with the org name — and
// an org with no attached store is E_NOT_FOUND.
func TestMachinesOrgRows(t *testing.T) {
	env := storesOnly{names: []store.Source{store.SourceRepo, "me", "agency", "agency/acme", "agency/org", "agencyx", "other/agency"}}
	rows := []work.Sourced{}
	for _, s := range env.names {
		rows = append(rows, work.Sourced{Source: s})
	}
	for _, c := range []struct {
		org  string
		want []store.Source
		code errs.Code
	}{
		{"agency", []store.Source{"agency", "agency/acme", "agency/org"}, ""},
		{"agencyx", []store.Source{"agencyx"}, ""},
		{"other", []store.Source{"other/agency"}, ""},
		{"me", []store.Source{"me"}, ""},
		{"agen", nil, errs.NotFound},
		{"globex", nil, errs.NotFound},
		{"Agency", nil, errs.NotFound},
	} {
		t.Run(c.org, func(t *testing.T) {
			got, err := orgRows(env, slices.Clone(rows), c.org)
			if errs.CodeOf(err) != c.code {
				t.Fatalf("err %v, want %q", err, c.code)
			}
			var names []store.Source
			for _, r := range got {
				names = append(names, r.Source)
			}
			if !slices.Equal(names, c.want) {
				t.Fatalf("rows %v, want %v", names, c.want)
			}
		})
	}
}
