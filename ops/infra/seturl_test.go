package infra_test

import (
	"github.com/thesatellite-ai/lore/internal/opstest"

	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/infra"
)

// TestSetURLRefusesNonRepoAddress: `lore repo set-url` with an address the
// normaliser refuses answers E_REPO_URL (bad input, exit 20, HTTP 400;
// PROVENANCE 10.4) naming the address, and the checkout's identity is
// unchanged. Flag misuse stays E_INVALID_INPUT.
func TestSetURLRefusesNonRepoAddress(t *testing.T) {
	h := checkout(t, "git@github.com:agency/acmehub_backend.git")
	if _, err := h.App.CheckRepoAddresses(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := meta(t, h)
	for _, url := range []string{
		"github.com",                        // no path
		"https://github.com/",               // no path
		"https://github.com",                // no path
		"https://bad_host!/agency/acme.git", // not a host name
		"github.com/agency/../acme",         // relative segment
		"github.com/agency//acme",           // empty segment
		"git@:agency/acme.git",              // scp form, no host
	} {
		t.Run(url, func(t *testing.T) {
			msg := h.Fails(infra.OpRepoSetURL, `{"url":"`+url+`"}`, errs.RepoURL)
			if !strings.Contains(msg, "not a repo address") {
				t.Errorf("message %q", msg)
			}
		})
	}
	_, err := h.Run(infra.OpRepoSetURL, `{"url":"github.com"}`)
	var e *errs.Error
	if !errors.As(err, &e) || e.Def().Exit != 20 || e.Def().HTTP != 400 || e.Def().Class != errs.ClassBadInput {
		t.Fatalf("E_REPO_URL def: %v", err)
	}
	after := meta(t, h)
	if after.RepoID != before.RepoID || !slices.Equal(after.URLs, before.URLs) {
		t.Fatalf("a refused set-url changed _meta.json: %+v → %+v", before, after)
	}

	h.Fails(infra.OpRepoSetURL, `{"url":"github.com/agency/x","mirror":true,"remove":true}`, errs.InvalidInput)
	h.Fails(infra.OpRepoSetURL, `{"url":"github.com/agency/x","from":"05/11/2026"}`, errs.InvalidInput)
}

// TestSetURLLocalRemoteRecordsNothing (devco:O-13): a remote that is a
// path on this machine is no address lore records, yet lore's own "the
// repo moved" hint names lore repo set-url for it; the command is accepted
// (exit 0), records nothing (_meta.json unchanged) and says why, naming
// the git step when no remote uses the path yet. Before, it was refused
// E_REPO_URL "not a repo address".
func TestSetURLLocalRemoteRecordsNothing(t *testing.T) {
	const moved = "/srv/git/renamed-kit.git"
	h := checkout(t, moved)
	before := meta(t, h)
	for _, tc := range []struct{ url, want string }{
		{moved, "a git remote already uses it"},
		{"file:///srv/git/other.git", "git remote set-url origin file:///srv/git/other.git"},
		{"../sibling-kit", "git remote set-url origin ../sibling-kit"},
	} {
		t.Run(tc.url, func(t *testing.T) {
			res := h.Must(infra.OpRepoSetURL, `{"url":"`+tc.url+`"}`)
			text := opstest.Text(res)
			if !strings.Contains(text, "not an address lore records") || !strings.Contains(text, tc.want) {
				t.Fatalf("text %q, want %q", text, tc.want)
			}
			if !strings.Contains(h.JSON(res), `"changed":false`) {
				t.Fatalf("changed: %s", h.JSON(res))
			}
		})
	}
	after := meta(t, h)
	if after.RepoID != before.RepoID || !slices.Equal(after.URLs, before.URLs) {
		t.Fatalf("set-url of a path changed _meta.json: %+v → %+v", before, after)
	}
}

// TestSetURLAddressForms: every accepted spelling of one repo confirms the
// same normalised key (host lower-cased, .git and trailing slash dropped).
func TestSetURLAddressForms(t *testing.T) {
	for _, url := range []string{
		"https://github.com/agency/acme-hub-api.git",
		"https://GitHub.com/agency/acme-hub-api/",
		"git@github.com:agency/acme-hub-api.git",
		"ssh://git@github.com:22/agency/acme-hub-api.git",
		"github.com/agency/acme-hub-api",
	} {
		t.Run(url, func(t *testing.T) {
			h := checkout(t, "git@github.com:agency/acmehub_backend.git")
			res := h.Must(infra.OpRepoSetURL, `{"url":"`+url+`"}`)
			if got := h.Field(res, "key"); got != "github.com/agency/acme-hub-api" {
				t.Fatalf("key = %q (%s)", got, h.JSON(res))
			}
		})
	}
}
