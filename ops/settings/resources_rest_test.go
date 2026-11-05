package settings_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/surface/rest"
)

// TestResourceRESTBulkAndOps (P2-RES-1, OV32, OV49): over the real engine,
// DELETE /v1/resources?kind=&where=&dry_run=true previews and writes
// nothing, then deletes only the matches as one batch; PATCH
// /v1/resources edits every match; the by-id routes and the newer resource
// ops (check with a note, history, report) answer.
func TestResourceRESTBulkAndOps(t *testing.T) {
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"host"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"provider","type":"text"}`},
	)
	for _, n := range []string{"a", "b", "c"} {
		h.MustYes(settings.OpResAdd, `{"kind":"host","name":"`+n+`","set":["provider=linode"]}`)
	}
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"keep","set":["provider=hetzner"]}`)
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := rest.New(h.App.Registry(), h.App, rest.Options{Token: restToken, Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	auth := "Bearer " + restToken

	code, body := call(t, ts, http.MethodPatch, "/v1/resources", auth, `{"kind":"host","where":["provider=linode"],"set":["provider=aws"],"yes":true}`)
	if code != http.StatusOK || strings.Count(body, `"res_`) != 3 || !strings.Contains(body, `"batch":"bat_`) {
		t.Fatalf("bulk PATCH: %d %s", code, body)
	}
	where := url.QueryEscape("provider=aws")
	code, body = call(t, ts, http.MethodDelete, "/v1/resources?kind=host&where="+where+"&dry_run=true", auth, ``)
	if code != http.StatusOK || !strings.Contains(body, `"dry_run":true`) || !strings.Contains(body, `"count":3`) {
		t.Fatalf("bulk DELETE dry run: %d %s", code, body)
	}
	if n := countHosts(t, h); n != 4 {
		t.Fatalf("dry run deleted: %d hosts", n)
	}
	code, body = call(t, ts, http.MethodPost, "/v1/resources/a/check", auth, `{"note":"via rest"}`)
	if code != http.StatusOK {
		t.Fatalf("check over REST: %d %s", code, body)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"a"}`)); !strings.Contains(s, "note: via rest") {
		t.Errorf("REST check note: %q", s)
	}
	if code, body = call(t, ts, http.MethodPost, "/v1/resources/a/history", auth, `{}`); code != http.StatusOK || !strings.Contains(body, "aws") {
		t.Errorf("history over REST: %d %s", code, body)
	}
	if code, body = call(t, ts, http.MethodPost, "/v1/resources/report", auth, `{"kind":"host"}`); code/100 != 2 {
		t.Errorf("report over REST: %d %s", code, body)
	}
	code, body = call(t, ts, http.MethodDelete, "/v1/resources?kind=host&where="+where+"&yes=true", auth, ``)
	if code != http.StatusOK || !strings.Contains(body, `"count":3`) || !strings.Contains(body, `"batch":"bat_`) {
		t.Fatalf("bulk DELETE: %d %s", code, body)
	}
	if n := countHosts(t, h); n != 1 {
		t.Fatalf("%d hosts left, want only keep", n)
	}
	if code, body = call(t, ts, http.MethodDelete, "/v1/resources/keep?yes=true", auth, ``); code != http.StatusOK {
		t.Errorf("by-id DELETE: %d %s", code, body)
	}
}
