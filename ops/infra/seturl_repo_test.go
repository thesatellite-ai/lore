package infra_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/infra"
)

// repoURLs is the urls field of the repos row id, as stored.
func repoURLs(t *testing.T, h *opstest.H, id string) string {
	t.Helper()
	x, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := h.App.Get(context.Background(), engine.Ref{ID: x})
	if err != nil {
		t.Fatal(err)
	}
	v, ok := r.Fields["urls"]
	if !ok || v == nil {
		return ""
	}
	return v.String()
}

// TestSetURLOtherRepo (OV31, CLI_REFERENCE 3.14): `lore repo set-url
// --repo <mount | rep_ id>` updates that repo row of the project only:
// this checkout's _meta.json is untouched and the output says so; the
// mount name and the rep_ id reach the same row; --repo naming this
// checkout's own row writes _meta.json as without --repo; an unknown name
// is E_NOT_FOUND with nothing changed.
func TestSetURLOtherRepo(t *testing.T) {
	h := checkout(t, "git@github.com:agency/acmehub_backend.git")
	own, err := h.App.CheckRepoAddresses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	web := h.ID(h.Must(infra.OpRepoAdd, `{"mount_name":"web"}`))
	before := meta(t, h)

	res := h.MustYes(infra.OpRepoSetURL, `{"url":"https://github.com/agency/acme-web.git","repo":"web"}`)
	if got := h.Field(res, "repo_id"); got != web {
		t.Fatalf("--repo web updated %s, want %s", got, web)
	}
	if !strings.Contains(opstest.Text(res), "_meta.json is not changed from here") {
		t.Fatalf("output does not say the other checkout is untouched: %s", opstest.Text(res))
	}
	if u := repoURLs(t, h, web); !strings.Contains(u, "github.com/agency/acme-web") {
		t.Fatalf("web row urls = %s", u)
	}
	if after := meta(t, h); after.RepoID != before.RepoID || !slices.Equal(after.URLs, before.URLs) {
		t.Fatalf("--repo web changed this checkout's _meta.json: %+v -> %+v", before, after)
	}
	if u := repoURLs(t, h, own.RepoID); strings.Contains(u, "acme-web") {
		t.Fatalf("this checkout's row took the other repo's address: %s", u)
	}

	res = h.MustYes(infra.OpRepoSetURL, `{"url":"github.com/agency/acme-web-mirror","mirror":true,"repo":"`+web+`"}`)
	if h.Field(res, "repo_id") != web || h.Field(res, "mode") != "mirror" {
		t.Fatalf("--repo <rep_ id>: %s", h.JSON(res))
	}
	if u := repoURLs(t, h, web); !strings.Contains(u, "acme-web-mirror") {
		t.Fatalf("web row urls after mirror = %s", u)
	}

	res = h.MustYes(infra.OpRepoSetURL, `{"url":"github.com/agency/acmehub-api","repo":"`+own.RepoID+`"}`)
	if h.Field(res, "repo_id") != own.RepoID || strings.Contains(opstest.Text(res), "not changed from here") {
		t.Fatalf("--repo <own id>: %s", opstest.Text(res))
	}
	if m := meta(t, h); len(m.URLs) == 0 || m.URLs[0] != "github.com/agency/acmehub-api" {
		t.Fatalf("--repo naming this checkout did not write _meta.json: %+v", m)
	}

	mid := meta(t, h)
	h.Fails(infra.OpRepoSetURL, `{"url":"github.com/agency/x","repo":"nope"}`, errs.NotFound)
	if after := meta(t, h); !slices.Equal(after.URLs, mid.URLs) || strings.Contains(repoURLs(t, h, web), "agency/x") {
		t.Fatal("a refused --repo changed something")
	}
}
