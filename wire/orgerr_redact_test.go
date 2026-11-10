package wire

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/model/errs"
)

// orgRedactHome is a lore home (the ~/.lore folder, Options.Home) whose
// config.yaml lists the agency's
// store (scp spelling) and a second org's store (https, .git).
func orgRedactHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	cfg := `orgs:
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - url: git@github.com:agency/notes
        org_folder: org
    repos: [github.com/client/*]
  - name: other
    identity: {key: "human:alice@other.example", name: "Alice Doe"}
    stores:
      - url: https://github.com/other/vault.git
`
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestRedactOrgErr (B-FLOW4-4, security): an org-resolution error printed
// without a terminal (an agent's transcript, a client's CI log) never
// names a configured org store url, in any spelling, in its message, hint
// or cause; its code and the rest of its text stay. With a terminal, for
// a non-lore error and for nil the error is returned as it is. A url
// nobody configured (the client's own repo) is not touched.
func TestRedactOrgErr(t *testing.T) {
	t.Parallel()
	home := orgRedactHome(t)
	urls := []string{
		"git@github.com:agency/notes",
		"git@github.com:agency/notes.git",
		"https://github.com/agency/notes",
		"ssh://git@github.com/agency/notes.git",
		"github.com/agency/notes",
		"https://github.com/other/vault.git",
		"git@github.com:other/vault",
		"github.com/other/vault",
	}
	for _, u := range urls {
		t.Run(u, func(t *testing.T) {
			in := errs.Wrap(errs.NotAStore, u+" (branch main) is listed under stores: but is not a lore store", errors.New("fetch "+u+": refused")).
				WithHint("remove " + u + " from ~/.lore/config.yaml")
			got := redactOrgErr(home, nil, in, true)
			if !errs.Has(got, errs.NotAStore) {
				t.Fatalf("code lost: %v", got)
			}
			body := errs.EnvelopeOf(got).Error
			all := got.Error() + "\n" + body.Message + "\n" + body.Hint
			for _, leak := range []string{"agency/notes", "other/vault"} {
				if strings.Contains(all, leak) {
					t.Fatalf("store url left in %q", all)
				}
			}
			if !strings.Contains(body.Message, redactedText) || !strings.Contains(body.Message, "(branch main) is listed under stores") || !strings.Contains(body.Hint, "~/.lore/config.yaml") {
				t.Fatalf("message/hint not kept around the redaction: %q / %q", body.Message, body.Hint)
			}
			// a terminal shows it
			if same := redactOrgErr(home, nil, in, false); !errors.Is(same, in) || same.Error() != in.Error() {
				t.Fatalf("terminal: %v, want the error unchanged", same)
			}
		})
	}

	// a client remote nobody configured stays readable
	own := errs.New(errs.OrgAmbiguous, "github.com/client/website matches two orgs")
	if got := redactOrgErr(home, nil, own, true); !strings.Contains(errs.EnvelopeOf(got).Error.Message, "github.com/client/website") {
		t.Fatalf("own remote redacted: %v", got)
	}
	// a store known only from a link of the situation
	sit := &orgcfg.Situation{Result: orgresolve.Result{Links: []orgresolve.Link{{StoreURL: "github.com/linked/store", Folder: "client"}}}}
	linked := errs.New(errs.LinkUnknown, "github.com/linked/store client/ cannot be read")
	if got := redactOrgErr(home, sit, linked, true); strings.Contains(got.Error(), "linked/store") {
		t.Fatalf("linked store url left: %v", got)
	}
	// non-lore errors and nil pass through; an unreadable config changes nothing
	plain := errors.New("git@github.com:agency/notes broke")
	if got := redactOrgErr(home, nil, plain, true); !errors.Is(got, plain) || got.Error() != plain.Error() {
		t.Fatalf("non-lore error rewritten: %v", got)
	}
	if got := redactOrgErr(home, nil, nil, true); got != nil {
		t.Fatalf("nil became %v", got)
	}
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "config.yaml"), []byte("orgs: [::"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := errs.New(errs.NotAStore, "git@github.com:agency/notes is not a store")
	if got := redactOrgErr(bad, nil, in, true); !errors.Is(got, in) || got.Error() != in.Error() {
		t.Fatalf("unreadable config: %v", got)
	}
}
