package infra_test

import (
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
		"file:///srv/git/acme.git",          // local remote, no host
		"/srv/git/acme.git",                 // absolute path
		"./acme",                            // relative path
		"../acme",                           // relative path
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
