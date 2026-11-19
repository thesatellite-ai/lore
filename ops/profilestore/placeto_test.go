package profilestore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/store"
)

// labeledEnv is a groundEnv whose project folder web of the store agency
// is attached under the bare name agency (a repo linked to agency/web),
// as wire.App labels it.
type labeledEnv struct{ groundEnv }

func (labeledEnv) StoreLabel(name store.Source) string {
	if name == "agency" {
		return "agency/web"
	}
	return string(name)
}

// TestPlaceToNamesTheFullFolder (devco D-697, owner use pass 5 weak 3): a
// printed --to names the full <store>/<folder> of a linked folder, never
// the bare name it is attached under (an admin running `--to agency` from
// their HOME changed the store-wide policy), and a member's refusal made
// inside another command (branch share runs policy set) prints that
// command's own line (WithAsk), so one place decides the wording.
func TestPlaceToNamesTheFullFolder(t *testing.T) {
	ctx := context.Background()
	member, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	admin, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	env := labeledEnv{newGroundEnv(t, member, admin)}
	for src, want := range map[store.Source]string{
		"agency":     "agency/web", // the linked folder, attached under the bare org name
		"agency/org": "agency/org",
		"agency/web": "agency/web",
		"repo":       "repo",
	} {
		if got := profilestore.PlaceTo(ctx, env, src); got != want {
			t.Errorf("PlaceTo(%s) = %q, want %q", src, got, want)
		}
	}
	line := "lore branch share --branch feat/a --yes in a checkout linked to agency/web, or from any folder: lore policy set --key branches.shared --value feat/a --to agency/web --yes"
	err = profilestore.RequireAdminAt(profilestore.WithAsk(ctx, "make a code branch write to the shared notes for everyone in the company folder agency/web", line),
		env, "agency", "change a policy of a company folder", "lore policy set --key branches.shared --value feat/a --to agency --yes")
	if !errs.Has(err, errs.Refused) || !strings.HasSuffix(hintOf(err), line) || !strings.Contains(err.Error(), "company folder agency/web") {
		t.Errorf("member refusal = %v (hint %q), want the branch line with --to agency/web", err, hintOf(err))
	}
	if err := profilestore.RequireAdminAt(profilestore.WithAsk(ctx, "x", line), labeledEnv{newGroundEnv(t, admin, admin)}, "agency", "x", "y"); err != nil {
		t.Errorf("admin: %v, want ok", err)
	}
}
