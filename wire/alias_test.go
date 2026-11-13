package wire

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestSetTargetEmbedded (later 122): the --to team / company rewrite
// reaches a `to` field inside an embedded struct (by value and by
// pointer), replaces only a value equal to the shortcut, and leaves a
// named (non-embedded) struct field and other json names alone.
func TestSetTargetEmbedded(t *testing.T) {
	t.Parallel()
	type target struct {
		To string `json:"to,omitempty"`
	}
	type other struct {
		To string `json:"to"`
	}
	type input struct {
		target
		*other
		Named other  `json:"named"`
		From  string `json:"from"`
	}
	cases := []struct {
		name            string
		in              input
		top, ptr, named string
	}{
		{"embedded value and pointer", input{target: target{To: "team"}, other: &other{To: "team"}, Named: other{To: "team"}, From: "team"}, "agency", "agency", "team"},
		{"another value kept", input{target: target{To: "repo"}, other: &other{To: "company"}, Named: other{To: "team"}, From: "team"}, "repo", "company", "team"},
		{"nil embedded pointer", input{target: target{To: "team"}, From: "team"}, "agency", "", ""},
	}
	for _, c := range cases {
		in := c.in
		setTarget(reflect.ValueOf(&in), "team", "agency")
		ptr := ""
		if in.other != nil {
			ptr = in.other.To
		}
		if in.target.To != c.top || ptr != c.ptr || in.Named.To != c.named || in.From != "team" {
			t.Errorf("%s: %+v (other %q)", c.name, in, ptr)
		}
	}
}

// TestPolicySetOrgShortcuts (later 122): `lore policy set --key … --to team`
// and `--to company` write the policy row into the org folders the
// shortcuts name; with no org both are refused E_NOT_FOUND naming
// `lore org link`, and nothing is written.
func TestPolicySetOrgShortcuts(t *testing.T) {
	t.Parallel()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: "agency", URL: orgURL, Folder: "acme"}, {Name: "agency/org", URL: orgURL, Folder: "org"}}, nil
	}
	a := openWith(t, gitRepo(t), Options{OrgStores: hook, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, a)
	for to, dst := range map[string]store.Source{"team": "agency", "company": "agency/org"} {
		if _, err := run(t, a, settings.OpPolicySet, `{"key":"trash.retention_days","value":"45","to":"`+to+`"}`, true); err != nil {
			t.Fatalf("policy set --to %s: %v", to, err)
		}
		rows, err := a.List(background(), dst, schema.TablePolicies)
		if err != nil || len(rows) != 1 {
			t.Fatalf("--to %s: %s holds %d policies (%v), want 1", to, dst, len(rows), err)
		}
	}

	b := openWith(t, gitRepo(t), Options{})
	withProject(t, b)
	for _, to := range []string{"team", "company"} {
		_, err := run(t, b, settings.OpPolicySet, `{"key":"trash.retention_days","value":"45","to":"`+to+`"}`, true)
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.NotFound || !strings.Contains(e.Message, "--to "+to+": this repository has no org store") ||
			!strings.Contains(e.Hint, "lore org link") {
			t.Fatalf("policy set --to %s with no org: %v", to, err)
		}
	}
	rows, err := b.List(background(), "repo", schema.TablePolicies)
	if err != nil || len(rows) != 0 {
		t.Fatalf("a refused shortcut wrote %d policies (%v)", len(rows), err)
	}
}
