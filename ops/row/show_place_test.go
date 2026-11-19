package row_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/ops/row"
)

// TestShowHeaderNamesThePlace (devco D-685): a row of an org store attached
// at a folder under the bare org name is headed by the folder label, as
// search, the lists and the sql source column name it; --json keeps the
// attached name --store takes.
func TestShowHeaderNamesThePlace(t *testing.T) {
	out := row.ShowOut{ID: "pol_0c3a", Table: "policies", Store: "acme-co", Place: "acme-co/web"}
	first := strings.SplitN(out.Text(), "\n", 2)[0]
	if first != "pol_0c3a  policies  acme-co/web" {
		t.Fatalf("header %q, want the folder label acme-co/web", first)
	}
	out.Place = ""
	if first = strings.SplitN(out.Text(), "\n", 2)[0]; first != "pol_0c3a  policies  acme-co" {
		t.Fatalf("header without a place %q, want the store name", first)
	}
}
