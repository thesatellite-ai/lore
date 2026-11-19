package setup

// join_freshname_test.go — devco:D-722: `lore setup join --invite <org
// name>` on a fresh machine is refused (E_JOIN_ORG_UNKNOWN) because lore
// never guesses an org's store url from its name; the help used to say
// "by its name or store url" without the condition, so a new member typed
// the name. The help (summary, the --invite description, the member
// example) and the refusal must all say: a fresh machine joins by the
// store url, with the exact command.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

func TestJoinHelpSaysStoreURLOnAFreshMachine(t *testing.T) {
	const onlyHere = "the org's name works only on a machine that already has the org"
	found := false
	for _, en := range GuidedOps(newFakeEnv()) {
		s := en.Spec()
		if s.Name != OpJoin {
			continue
		}
		found = true
		if strings.Contains(s.Summary, "by its name or store url") || !strings.Contains(s.Summary, "by its store url") || !strings.Contains(s.Summary, onlyHere) {
			t.Errorf("setup join summary offers the org's name without its condition: %q", s.Summary)
		}
		for _, f := range en.InputFields() {
			if f.Name == "invite" && (strings.Contains(f.Desc, "org's name or store url") || !strings.Contains(f.Desc, onlyHere)) {
				t.Errorf("--invite description offers the org's name without its condition: %q", f.Desc)
			}
		}
		for _, ex := range s.Examples {
			if ex.Title != "join as a member" {
				continue
			}
			// the member example is what a new member copies: it must be
			// the form that works on a fresh machine (a store url)
			arg := strings.Fields(strings.TrimPrefix(ex.CLI, "lore setup join --invite "))[0]
			if !storeURLish(arg) {
				t.Errorf("member example joins by %q, which a fresh machine refuses: %q", arg, ex.CLI)
			}
		}
	}
	if !found {
		t.Fatal("no setup.join op registered")
	}
	_, err := newTestEngine(newFakeEnv()).Join(bg, JoinIn{Invite: "acme-hub"})
	if !errs.Has(err, errs.JoinOrgUnknown) {
		t.Fatalf("join by an unknown org name: %v", err)
	}
	for _, want := range []string{"fresh machine", "does not guess", "lore setup join --invite <store-url>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err.Error(), want)
		}
	}
}
