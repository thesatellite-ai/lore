package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// TestMetaWriteStoreUnreadable (METADATA 3.11 rule 2, section 6): in a
// client repo on an agency machine whose org store cannot be read, a write
// that carries metadata is refused E_META_STORE_UNREADABLE (a new key
// could shadow a store key, an internal one) and writes nothing; the same
// write without metadata keeps E_LEAK_BLOCKED; in an internal repo the
// metadata write goes through.
func TestMetaWriteStoreUnreadable(t *testing.T) {
	t.Parallel()
	org := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: "northwind", URL: unreachableURL, Folder: "acme"}}, nil
	}
	for _, c := range []struct {
		name               string
		client             bool
		metaCode, noneCode errs.Code
	}{
		{"client", true, errs.MetaStoreUnreadable, errs.LeakBlocked},
		{"internal", false, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			isClient := func(context.Context) (bool, error) { return c.client, nil }
			a := openWith(t, gitRepo(t), Options{OrgStores: org, RepoIsClient: isClient})
			withProject(t, a)
			_, err := run(t, a, opsmemory.OpAdd, `{"body":"tiered","to":"repo","meta":["margin_pct=35"]}`, false)
			if c.metaCode == "" {
				if err != nil {
					t.Fatalf("metadata write in an internal repo: %v", err)
				}
			} else if !errs.Has(err, c.metaCode) {
				t.Fatalf("metadata write: want %s, got %v", c.metaCode, err)
			}
			_, err = run(t, a, opsmemory.OpAdd, `{"body":"plain","to":"repo"}`, false)
			if c.noneCode == "" {
				if err != nil {
					t.Fatalf("plain write in an internal repo: %v", err)
				}
			} else if !errs.Has(err, c.noneCode) {
				t.Fatalf("plain write: want %s, got %v", c.noneCode, err)
			}
			if c.client {
				out, lerr := run(t, a, opsmemory.OpList, `{}`, false)
				if lerr != nil || strings.Contains(out, "tiered") {
					t.Fatalf("a refused metadata write was stored (or list failed %v): %s", lerr, out)
				}
			}
		})
	}
}
