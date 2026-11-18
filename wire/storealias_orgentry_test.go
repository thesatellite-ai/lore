package wire

import "testing"

// aliasOrgConfig is a member home whose org entry names the store by its
// canonical url, the spelling the shared config holds. The store host is a
// made-up dotted host and the alias a made-up single label, so no ssh
// config of the machine running the test names either (the alias resolver
// reads the real ~/.ssh/config; one that renames the store host, such as
// github.com to its port-443 route, would change the answer).
const aliasOrgConfig = `orgs:
  - name: acme
    stores:
      - url: git@git.example.org:acme/notes.git
        org_folder: org
`

// TestCloneOriginThroughAliasFindsOrgEntry (devco D-627): a store clone
// whose origin goes through a host alias the ssh config does not rename
// (git@<alias>:acme/notes.git, routed by url.<base>.insteadOf) is the
// org's store, as lore's clone folder lookup already says (CloneDirFor,
// SameRepoViaAlias). The org lookup keyed it as <alias>/acme/notes and
// found no org: a read in the clone folder lost the org folder layer.
func TestCloneOriginThroughAliasFindsOrgEntry(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, aliasOrgConfig)
	const alias = "git@zz-d627-alias:acme/notes.git"

	if o := cloneOrgEntry(home, alias); o == nil || o.Name != "acme" {
		t.Errorf("cloneOrgEntry(%s) = %v, want the acme entry (the org folder layer of a clone folder)", alias, o)
	}
	if got := orgOfURL(home, alias); got != "acme" {
		t.Errorf("orgOfURL(%s) = %q, want acme (the clone folder's store name)", alias, got)
	}
	if got := orgStoreLabel(home, alias); got != "acme" {
		t.Errorf("orgStoreLabel(%s) = %q, want acme", alias, got)
	}
	if !sameStoreURL("git@git.example.org:acme/notes.git", alias) {
		t.Errorf("sameStoreURL: the alias spelling is not the same store")
	}

	// still other stores: another repository path, another public host
	for _, other := range []string{
		"git@zz-d627-alias:acme/other.git",
		"git@gitlab.com:acme/notes.git",
		"https://gitlab.com/acme/notes",
	} {
		if o := cloneOrgEntry(home, other); o != nil {
			t.Errorf("cloneOrgEntry(%s) = %s, want none (another store)", other, o.Name)
		}
		if got := orgOfURL(home, other); got != "" {
			t.Errorf("orgOfURL(%s) = %q, want none (another store)", other, got)
		}
	}
}

// TestSameStoreAs pins the one store-identity rule the org lookups share.
func TestSameStoreAs(t *testing.T) {
	t.Parallel()
	same := sameStoreAs("git@git.example.org:acme/notes.git")
	if same == nil {
		t.Fatal("sameStoreAs refused a store url")
	}
	for in, want := range map[string]bool{
		"https://git.example.org/acme/notes":       true,
		"git@git.example.org:acme/notes":           true,
		"git@zz-d627-alias:acme/notes.git":         true,
		"git@zz-d627-alias:acme/other.git":         false,
		"git@gitlab.com:acme/notes.git":            false,
		"file:///srv/stores/acme/notes":            false,
		"not a url at all":                         false,
		"https://git.example.org/acme/notes-extra": false,
	} {
		if got := same(in); got != want {
			t.Errorf("same(%q) = %v, want %v", in, got, want)
		}
	}
	if sameStoreAs("") != nil {
		t.Errorf("sameStoreAs(\"\") is a matcher, want nil")
	}
}
