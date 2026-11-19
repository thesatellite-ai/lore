package orgcfg

import (
	"strings"
	"testing"
)

// devco T-22: retiring a store drops only its entry; every other key and
// store stays, and a store not listed is reported, not invented.
func TestDropOrgFileStore(t *testing.T) {
	raw := []byte("name: initech\nstores:\n  - url: git@git.example.test:initech/team.git\n    org_folder: org\n  - url: git@git.example.test:initech/clientco.git\n    name: clientco\nown:\n  - git.example.test/initech/*\n")
	out, listed, err := dropOrgFileStore(raw, "git.example.test/initech/clientco")
	if err != nil || !listed {
		t.Fatalf("drop: listed=%v err=%v", listed, err)
	}
	s := string(out)
	if strings.Contains(s, "clientco") || !strings.Contains(s, "initech/team.git") || !strings.Contains(s, "own:") {
		t.Fatalf("wrong file after retire:\n%s", s)
	}
	if _, listed, _ := dropOrgFileStore(out, "git.example.test/initech/clientco"); listed {
		t.Fatal("a store no longer listed was found again")
	}
}
