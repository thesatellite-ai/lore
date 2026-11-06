package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestMetaKeysFiltersAndAll: meta keys --table keeps keys used on that
// table, --place narrows by level, set --where --dry-run --all lists rows,
// --all without --dry-run is refused, delete --reason is accepted.
func TestMetaKeysFiltersAndAll(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"jira","type":"text"}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"cost","type":"number"}`)
	h.MustYes(memAdd, `{"body":"one","meta":["jira=HUB-1"]}`)
	got := opstest.Text(h.Must(settings.OpMetaKeys, `{"table":"memories"}`))
	if !strings.Contains(got, "jira") || strings.Contains(got, "cost") {
		t.Fatalf("--table %q", got)
	}
	if org := opstest.Text(h.Must(settings.OpMetaKeys, `{"place":"org"}`)); strings.Contains(org, "jira") {
		t.Fatalf("--place org %q", org)
	}
	got = opstest.Text(h.Must(settings.OpMetaSet, `{"where":["jira?"],"id":"cost=3","dry_run":true,"all":true}`))
	if !strings.Contains(got, "would set cost on 1 rows") {
		t.Fatalf("--all %q", got)
	}
	h.Fails(settings.OpMetaSet, `{"where":["jira?"],"id":"cost=3","all":true}`, errs.InvalidInput)
	h.MustYes(settings.OpMetaDelete, `{"id":"cost","reason":"unused"}`)
}
