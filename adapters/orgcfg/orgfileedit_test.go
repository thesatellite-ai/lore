package orgcfg

import (
	"strings"
	"testing"
)

// TestSetOrgFileList: org config set changes one list key and keeps every
// other key in its place; the same list is no change; an empty list
// removes the key (devco D-453).
func TestSetOrgFileList(t *testing.T) {
	raw := []byte("name: acme\nstores:\n  - url: git@example.test:acme/lore.git\n    org_folder: org\nown:\n  - example.test/acme/*\n")
	out, changed, err := setOrgFileList(raw, "repos", []string{"example.test/acme/client-*"})
	if err != nil || !changed {
		t.Fatalf("set repos: changed=%v err=%v", changed, err)
	}
	s := string(out)
	for _, want := range []string{"name: acme", "org_folder: org", "own:\n    - example.test/acme/*", "repos:\n    - example.test/acme/client-*"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Index(s, "own:") > strings.Index(s, "repos:") {
		t.Fatalf("own moved after repos:\n%s", s)
	}
	if _, changed, err = setOrgFileList(out, "repos", []string{"example.test/acme/client-*"}); err != nil || changed {
		t.Fatalf("same list: changed=%v err=%v", changed, err)
	}
	out, changed, err = setOrgFileList(out, "own", nil)
	if err != nil || !changed || strings.Contains(string(out), "own:") {
		t.Fatalf("empty own: changed=%v err=%v\n%s", changed, err, out)
	}
	if _, _, err = setOrgFileList([]byte("own: one\n"), "own", []string{"x/y/z"}); err == nil {
		t.Fatal("a scalar own was read as a list")
	}
}
