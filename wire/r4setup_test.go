package wire

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestJoinBeforeAdminSyncNamesLoreSync (devco:K-1): joining (importing) a
// store that holds no org yet, with or without commits, says the admin has
// not sent the org and that the admin runs lore sync; never "ask the team
// to commit <org_folder>/lore-org.yaml" nor "make it a lore store first".
func TestJoinBeforeAdminSyncNamesLoreSync(t *testing.T) {
	url := "git@git.example:acme/team.git"
	for name, cause := range map[string]error{
		"no org file": fmt.Errorf("%w: git.example/acme/team@main", orgcfg.ErrNoOrgFile),
		"no commits":  fmt.Errorf("%w (%w): git.example/acme/team has no commits yet", orgcfg.ErrStoreEmpty, orgcfg.ErrNoStoreBranch),
	} {
		err := importError(cause, url)
		var le *errs.Error
		if !errors.As(err, &le) || le.Code != errs.InvalidInput {
			t.Fatalf("%s: %v", name, err)
		}
		all := le.Message + " | " + le.Hint
		if !strings.Contains(le.Message, "the admin who made the org has not sent it") || le.Hint != "ask the org's admin to run lore sync, then run this again" ||
			strings.Contains(all, "<org_folder>") || strings.Contains(all, "lore store init") {
			t.Fatalf("%s: %s", name, all)
		}
	}
	if err := orgFileError(fmt.Errorf("%w: x", orgcfg.ErrNoOrgFile), url); !strings.Contains(err.Error(), "has not sent it") {
		t.Fatalf("orgFileError: %v", err)
	}
}

// TestProjectMissOutsideRepoPointsIntoTheRepo (devco:J-10): member add
// --project naming a repo's project from outside any repo says such a
// project is read only inside its repo and offers no command that fails
// there (lore project list) or makes a second project (store folder add).
func TestProjectMissOutsideRepoPointsIntoTheRepo(t *testing.T) {
	var le *errs.Error
	if !errors.As(projectMissError("jeoga", " (searched the org's stores: acme)", false), &le) || le.Code != errs.NotFound {
		t.Fatalf("outside: %+v", le)
	}
	if !strings.Contains(le.Message, "read only from inside that repo") || !strings.Contains(le.Hint, "from inside the repo of jeoga") ||
		strings.Contains(le.Hint, "lore project list") || strings.Contains(le.Hint, "store folder add") {
		t.Fatalf("outside: %s | %s", le.Message, le.Hint)
	}
	if !errors.As(projectMissError("jeoga", "", true), &le) || !strings.Contains(le.Hint, "lore project list") {
		t.Fatalf("inside: %+v", le)
	}
}

// TestStoreNamingNoOrgCannotTell (devco:D-721): a lore store that holds no
// lore-org.yaml and names no org in its lore-store.yaml is either an org's
// main store not sent yet or another store of an org; the refusal said "the
// admin who made the org has not sent it", which lore cannot know. It now
// says lore cannot tell whose store it is, prints the join by the org's
// main store url, and carries errNoOrgNamed so setup join refuses too.
func TestStoreNamingNoOrgCannotTell(t *testing.T) {
	url := "git@git.example:acme/ops.git"
	err := orgFileError(&orgcfg.OrgStoreNoteError{Note: orgcfg.OrgStoreNote{Store: "git.example/acme/ops"}, Branch: "main"}, url)
	var le *errs.Error
	if !errors.As(err, &le) || le.Code != errs.InvalidInput || !errors.Is(err, errNoOrgNamed) {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(le.Message, "lore cannot tell whose store "+url) || strings.Count(err.Error(), errNoOrgNamed.Error()) != 1 ||
		strings.Contains(le.Message+le.Hint, "has not sent it") || !strings.Contains(le.Hint, "lore setup join --invite <the org's main store url>") {
		t.Fatalf("message %q hint %q", err.Error(), le.Hint)
	}
}
