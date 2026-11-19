package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// TestGuardLinkFixNamesTheLinkedFolder (devco:N-9): whoami's guards-off
// hint in a client repo linked by its remote names the folder of that
// link, not a `<folder>` placeholder.
func TestGuardLinkFixNamesTheLinkedFolder(t *testing.T) {
	s := &orgcfg.Situation{Result: orgresolve.Result{
		Org:   &orgconf.Org{Name: "northwind"},
		Links: []orgresolve.Link{{Entry: "northwind", StoreURL: "git.example.test/northwind/client-store", Folder: "fabrikam"}},
	}}
	if got, want := guardLinkFix(s), "lore org link --org northwind --project fabrikam --yes"; got != want {
		t.Fatalf("guardLinkFix = %q, want %q", got, want)
	}
	// nothing known: the placeholders stay, so the hint is still a command shape
	if got, want := guardLinkFix(&orgcfg.Situation{}), "lore org link --org <org> --project <folder> --yes"; got != want {
		t.Fatalf("guardLinkFix with no link = %q, want %q", got, want)
	}
}
