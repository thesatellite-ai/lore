package orgcmd

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestHostlessPatternHintReruns (bug hunt H-112): org create and org
// config set refuse a pattern with no host before anything is read, and
// the hint is the same command with the suggested pattern, ready to run.
func TestHostlessPatternHintReruns(t *testing.T) {
	err := CheckCreatePatterns(CreateIn{Org: "acme", Store: "acme_team", OrgFolder: DefaultOrgFolder,
		Own: []string{"github.com/acme/*"}, Repos: []string{"client-*"}, Key: "human:cora@acme.test", Name: "Cora Vale"})
	if !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("create: %v, want E_INVALID_INPUT", err)
	}
	want := "lore org create --org acme --store acme_team --own 'github.com/acme/*' --repos 'github.com/acme/client-*' --key human:cora@acme.test --name 'Cora Vale' --yes"
	if got := hintOfErr(err); got != want {
		t.Errorf("create hint:\n got %s\nwant %s", got, want)
	}
	err = CheckConfigSet(ConfigSetIn{Key: ConfigKeyOwn, Add: []string{"tools-*"}, Push: true})
	if !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("config set: %v, want E_INVALID_INPUT", err)
	}
	if got, want := hintOfErr(err), "lore org config set --key own --add 'github.com/<owner>/tools-*' --push --yes"; got != want {
		t.Errorf("config set hint:\n got %s\nwant %s", got, want)
	}
	err = CheckConfigSet(ConfigSetIn{Org: "acme", Key: ConfigKeyRepos, Values: []string{"github.com/acme/*", "acme-labs"}})
	if got, want := hintOfErr(err), "lore org config set --org acme --key repos --value 'github.com/acme/*' --value 'github.com/acme-labs/*' --yes"; got != want {
		t.Errorf("config set --value hint:\n got %s\nwant %s", got, want)
	}
	if err := CheckCreatePatterns(CreateIn{Org: "acme", Own: []string{"*/acme/*"}}); hintOfErr(err) != patternHint {
		t.Errorf("a wildcard host keeps the shape hint: %v (%q)", err, hintOfErr(err))
	}
}

// hintOfErr is the hint of the *errs.Error in err's chain.
func hintOfErr(err error) string { return errs.EnvelopeOf(err).Error.Hint }
