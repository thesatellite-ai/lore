package orgcfg

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestSharedForLeavesAdminsStoreOutForMembers (devco D-442): lore-org.yaml
// lists the team store and an ops store with audience: admins. The admin's
// copy holds both; a member's copy (and a copy with no identity) leaves
// the ops store out and names it as skipped, so a member's lore never
// clones or reads it. The org folder's store stays for everyone.
func TestSharedForLeavesAdminsStoreOutForMembers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir, _ := authOrg(t)
	authCommit(t, dir, authAdmin, authAdminE, map[string]string{
		"org/lore-org.yaml": "name: acme\nstores:\n    - url: git@example.invalid:acme/team.git\n      org_folder: org\n" +
			"    - url: git@example.invalid:acme/ops.git\n      audience: admins\n",
	})
	sg, found, err := readSuggestionFile(ctx, dir, "HEAD", "org/lore-org.yaml", "org", "example.invalid/acme/team", "main", "sto_x")
	if err != nil || !found {
		t.Fatalf("readSuggestionFile: %v, found %v", err, found)
	}
	if st := sg.Shared().Stores; len(st) != 2 || st[1].Audience != orgconf.AudienceAdmins {
		t.Fatalf("Shared() = %+v; want both stores, the second for admins", st)
	}
	admin, skipped := sg.SharedFor(ctx, "human:"+authAdminE)
	if len(admin.Stores) != 2 || len(skipped) != 0 {
		t.Errorf("the admin's copy = %+v (skipped %+v); want both stores", admin.Stores, skipped)
	}
	for _, key := range []string{"human:" + authMembE, ""} {
		mine, skipped := sg.SharedFor(ctx, key)
		if len(mine.Stores) != 1 || !strings.Contains(mine.Stores[0].URL, "acme/team") {
			t.Errorf("key %q: copy = %+v; want only the team store", key, mine.Stores)
		}
		if len(skipped) != 1 || !strings.Contains(skipped[0].URL, "acme/ops") {
			t.Errorf("key %q: skipped = %+v; want the ops store", key, skipped)
		}
	}
}

// TestAppendOrgFileStoreSetsAudience: `lore org store audience` sets the
// key on a listed store and removes it for everyone; the org folder's
// store is refused; without SetAudience a listed store is left alone.
func TestAppendOrgFileStoreSetsAudience(t *testing.T) {
	t.Parallel()
	raw := []byte("name: acme\nstores:\n    - url: git@example.invalid:acme/team.git\n      org_folder: org\n    - url: git@example.invalid:acme/ops.git\n")
	want := "example.invalid/acme/ops"
	out, existed, err := appendOrgFileStore(raw, want, OrgFileStore{URL: "git@example.invalid:acme/ops.git", Audience: "admins"}, true)
	if err != nil || !existed || !strings.Contains(string(out), "audience: admins") {
		t.Fatalf("set admins: %v, existed %v:\n%s", err, existed, out)
	}
	if out2, existed, err := appendOrgFileStore(out, want, OrgFileStore{URL: "git@example.invalid:acme/ops.git", Audience: "admins"}, true); err != nil || !existed || out2 != nil {
		t.Errorf("same audience again: %v, existed %v, %q; want nothing to commit", err, existed, out2)
	}
	if out2, _, err := appendOrgFileStore(out, want, OrgFileStore{URL: "git@example.invalid:acme/ops.git"}, false); err != nil || out2 != nil {
		t.Errorf("a plain add of a listed store changed the file: %v %q", err, out2)
	}
	back, _, err := appendOrgFileStore(out, want, OrgFileStore{URL: "git@example.invalid:acme/ops.git"}, true)
	if err != nil || strings.Contains(string(back), "audience") {
		t.Errorf("set everyone: %v:\n%s", err, back)
	}
	if _, _, err := appendOrgFileStore(raw, "example.invalid/acme/team", OrgFileStore{URL: "git@example.invalid:acme/team.git", Audience: "admins"}, true); !errors.Is(err, ErrOrgFolderAudience) {
		t.Errorf("admins-only org folder store: %v; want ErrOrgFolderAudience", err)
	}
}

// TestSharedForTeamStore (devco D-727): lore-org.yaml lists a store with
// audience team:@secret. A member on that team (the roster's teams row at
// the commit read) gets it in their copy; once the team row drops them,
// their copy leaves it out and names nothing as skipped; the admin always
// has it. A v0.1.17 teams row (member ids as a JSON string) counts too.
func TestSharedForTeamStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir, _ := authOrg(t)
	authCommit(t, dir, authAdmin, authAdminE, map[string]string{
		"org/lore-org.yaml": "name: acme\nstores:\n    - url: git@example.invalid:acme/team.git\n      org_folder: org\n" +
			"    - url: git@example.invalid:acme/lore-secret.git\n      audience: team:@secret\n",
		"org/teams/tea_s.json": `{"id":"tea_s","handle":"secret","member_actor_ids":["act_m"],"active":true}`,
		"org/teams/tea_y.json": `{"id":"tea_y","handle":"sync","member_actor_ids":"[\"act_m\"]"}`,
	})
	read := func() Suggestion {
		t.Helper()
		sg, found, err := readSuggestionFile(ctx, dir, "HEAD", "org/lore-org.yaml", "org", "example.invalid/acme/team", "main", "sto_x")
		if err != nil || !found {
			t.Fatalf("readSuggestionFile: %v, found %v", err, found)
		}
		return sg
	}
	sg := read()
	if v := sg.ViewerOf(ctx, "human:"+authMembE); v.Admin || !slices.Equal(v.Teams, []string{"secret", "sync"}) {
		t.Errorf("member viewer = %+v; want teams secret and sync", v)
	}
	if mine, skipped := sg.SharedFor(ctx, "human:"+authMembE); len(mine.Stores) != 2 || len(skipped) != 0 {
		t.Errorf("team member's copy = %+v (skipped %+v); want both stores", mine.Stores, skipped)
	}
	authCommit(t, dir, authAdmin, authAdminE, map[string]string{
		"org/teams/tea_s.json": `{"id":"tea_s","handle":"secret","member_actor_ids":[],"active":true}`,
	})
	sg = read()
	for _, key := range []string{"human:" + authMembE, ""} {
		mine, skipped := sg.SharedFor(ctx, key)
		if len(mine.Stores) != 1 || !strings.Contains(mine.Stores[0].URL, "acme/team") || len(skipped) != 0 {
			t.Errorf("key %q off the team: copy = %+v (skipped %+v); want only the org store, nothing named", key, mine.Stores, skipped)
		}
	}
	if admin, _ := sg.SharedFor(ctx, "human:"+authAdminE); len(admin.Stores) != 2 {
		t.Errorf("the admin's copy = %+v; want both stores", admin.Stores)
	}
	if got := teamHandlesIn(ctx, dir, "org"); !slices.Equal(got, []string{"secret", "sync"}) {
		t.Errorf("team handles = %v; want secret, sync", got)
	}
}
