package onboard

import (
	"errors"
	"strings"
	"testing"
)

// TestMatchFolderOnADrive: a setup glob on a Windows drive matches a folder
// under it when the resolver (EvalSymlinks) turns the short home name into
// the long one. Before, the glob's literal prefix was resolved as
// "/C:/Users/RUNNER~1/w", which Windows cannot resolve, so only the folder
// side became C:/Users/runneradmin/... and no setup matched.
func TestMatchFolderOnADrive(t *testing.T) {
	t.Parallel()
	resolve := func(p string) (string, error) {
		if !strings.HasPrefix(p, "C:/") {
			return "", errors.New("not a Windows path") // "/C:/..." included
		}
		return strings.Replace(p, "C:/Users/RUNNER~1", "C:/Users/runneradmin", 1), nil
	}
	setups := []SetupEntry{
		{Name: "work", Folders: []string{"C:/Users/RUNNER~1/w/**"}},
		{Name: "acme", Folders: []string{"C:/Users/RUNNER~1/w/acme/**"}},
	}
	if r := MatchFolder(setups, "C:/Users/RUNNER~1/w/x", "", resolve); !r.Found || r.Match.Setup != "work" {
		t.Fatalf("w/x: %+v, want setup work", r)
	}
	if r := MatchFolder(setups, "C:/Users/RUNNER~1/w/acme/y", "", resolve); !r.Found || r.Match.Setup != "acme" {
		t.Fatalf("w/acme/y: %+v, want setup acme", r)
	}
	if got := LiteralPrefix("C:/Users/a/w/**", ""); got != "C:/Users/a/w" {
		t.Fatalf("LiteralPrefix = %s, want C:/Users/a/w", got)
	}
}
