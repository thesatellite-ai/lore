package orgcfg

import (
	"strings"
	"testing"
)

// TestAppendOrgFileStore: B-4, `lore org store add` appends one store to
// lore-org.yaml and keeps every other key and comment; a store the file
// lists already (any spelling of its url) is reported, not added twice.
func TestAppendOrgFileStore(t *testing.T) {
	raw := []byte("# the agency's org\nname: agency\nstores:\n    - url: git@git.example.test:agency/team-store.git\n      org_folder: org\nown:\n    - git.example.test/agency/*\n")
	want := "git.example.test/agency/client-store"
	out, existed, err := appendOrgFileStore(raw, want, OrgFileStore{URL: "git@git.example.test:agency/client-store.git", Name: "client"})
	if err != nil || existed {
		t.Fatalf("append: existed %v err %v", existed, err)
	}
	got := string(out)
	for _, s := range []string{"# the agency's org", "name: agency", "url: git@git.example.test:agency/team-store.git", "org_folder: org", "url: git@git.example.test:agency/client-store.git", "name: client", "git.example.test/agency/*"} {
		if !strings.Contains(got, s) {
			t.Errorf("result lacks %q:\n%s", s, got)
		}
	}
	doc, err := parseOrgFileDoc(string(out))
	if err != nil || len(doc.Stores) != 2 || doc.Stores[1].Name != "client" {
		t.Fatalf("result does not parse as two stores: %+v %v", doc, err)
	}
	if _, existed, err := appendOrgFileStore(out, want, OrgFileStore{URL: "ssh://git@git.example.test/agency/client-store"}); err != nil || !existed {
		t.Fatalf("second add: existed %v err %v", existed, err)
	}
	if out, _, err := appendOrgFileStore([]byte("name: solo\n"), want, OrgFileStore{URL: "git@git.example.test:agency/client-store.git"}); err != nil || !strings.Contains(string(out), "stores:") {
		t.Fatalf("no stores key: %s %v", out, err)
	}
}
