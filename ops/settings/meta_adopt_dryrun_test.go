package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestMetaAdoptDryRun (later 522, CLI_REFERENCE 3.1): meta adopt
// --dry-run names the keys that would move and writes nothing (needs no
// --yes); the real run then moves them.
func TestMetaAdoptDryRun(t *testing.T) {
	h := multiStore(t, ownRepo)
	h.MustYes(settings.OpMetaAdd, `{"name":"env","type":"text","values":"prod,dev","to":"kb/web"}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"tier","type":"text","to":"kb/web"}`)
	before := h.JSON(h.Must(settings.OpMetaKeys, `{}`))

	out := h.JSON(h.Must(settings.OpMetaAdopt, `{"from":"kb/web","to":"kb/org","dry_run":true}`))
	if !strings.Contains(out, `"env"`) || !strings.Contains(out, `"tier"`) || !strings.Contains(out, `"dry_run":true`) {
		t.Fatalf("dry run output %s", out)
	}
	if after := h.JSON(h.Must(settings.OpMetaKeys, `{}`)); after != before {
		t.Fatalf("dry run wrote:\nbefore %s\nafter  %s", before, after)
	}
	h.MustYes(settings.OpMetaAdopt, `{"from":"kb/web","to":"kb/org"}`)
	if moved := h.JSON(h.Must(settings.OpMetaKeys, `{"keys":["env"]}`)); !strings.Contains(moved, `"store":"kb/org"`) {
		t.Fatalf("real run did not move env: %s", moved)
	}
}
