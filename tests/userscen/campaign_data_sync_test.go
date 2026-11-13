//go:build userscen

package userscen

// campaign_data_sync_test.go — the data nouns across two clones of one
// bare remote (the sync area's newSyncTeam fixture): what one person
// writes (a variable, a tag, a comment, a resource, a metadata value, a
// commit link) reaches the other after push and pull; writes made with the
// remote unreachable succeed locally and travel once it is back; two
// processes writing at once in one clone leave every write (or a clean
// refusal) and a readable tree.

import (
	"strings"
	"sync"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// cdWriteAll writes one row of every data noun in c and returns the ids
// that must reach a teammate.
func cdWriteAll(t *testing.T, c *th.Clone, suffix string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	c.MustLore("vars", "set", "host_"+suffix, "h-"+suffix+".acme.example")
	c.MustLore("tag", "add", "--name=tag_"+suffix)
	m := cdIDOf(t, c.Lore("memory", "add", "--body", "memory "+suffix, "--json"))
	ids["memory"] = m
	ids["comment"] = cdIDOf(t, c.Lore("comment", "add", "--on-table=memories", "--on-id="+m, "--body=comment "+suffix, "--json"))
	c.MustLore("meta", "set", m, "owner_"+suffix+"=alice")
	c.MustLore("resource", "kind", "add", "box_"+suffix)
	ids["resource"] = cdIDOf(t, c.Lore("resource", "add", "box_"+suffix, "one", "--json"))
	return ids
}

// cdSeesAll checks that x sees everything cdWriteAll wrote with suffix.
func cdSeesAll(t *testing.T, x *th.Clone, suffix string, ids map[string]string) {
	t.Helper()
	if v := cdVarValue(t, x, "host_"+suffix); v != "h-"+suffix+".acme.example" {
		t.Errorf("var host_%s: %q", suffix, v)
	}
	x.MustLore("tag", "list").Says(t, "tag_"+suffix)
	x.MustLore("comment", "list", "--on-id="+ids["memory"]).Says(t, "comment "+suffix)
	if got := cdMetaOf(t, x, ids["memory"]); got["owner_"+suffix] != "alice" {
		t.Errorf("meta of %s: %v", ids["memory"], got)
	}
	x.MustLore("resource", "show", ids["resource"]).Says(t, "one")
}

// cdPush commits and pushes c's main (pulling first: a land may have moved
// origin/main while the person worked). Rows that finished in live land
// with this push (U-2): the pre-push hook adds them and asks once to push
// again.
func cdPush(c *th.Clone, msg string) {
	c.CommitAll(msg)
	c.Git("pull", "-q", "--no-rebase", "origin", "main")
	c.AdminPushLanding("origin", "main")
}

// TestCampaignData_SyncTwoClones: alice's data rows reach bob after push
// and pull; bob's edit of the variable reaches alice back.
func TestCampaignData_SyncTwoClones(t *testing.T) {
	st := newSyncTeam(t, 2)
	a, b := st.clones[0], st.clones[1]
	ids := cdWriteAll(t, a, "a")
	cdPush(a, "data")
	b.Git("pull", "-q", "--no-rebase", "origin", "main")
	cdSeesAll(t, b, "a", ids)
	b.MustLore("vars", "set", "host_a", "moved.acme.example")
	cdPush(b, "move host")
	a.Git("pull", "-q", "--no-rebase", "origin", "main")
	if v := cdVarValue(t, a, "host_a"); v != "moved.acme.example" {
		t.Errorf("alice after pull: host_a = %q", v)
	}
	if w := a.MustLore("vars", "where", "host_a").Stdout; strings.Count(w, "var_") > 1 {
		t.Errorf("one variable is defined twice after the round trip:\n%s", w)
	}
}

// TestCampaignData_SyncOffline: with the remote unreachable every data
// write still succeeds locally; once it is reachable again a push and a
// pull carry them to the teammate.
func TestCampaignData_SyncOffline(t *testing.T) {
	st := newSyncTeam(t, 2)
	a, b := st.clones[0], st.clones[1]
	st.people[0].Offline()
	ids := cdWriteAll(t, a, "off")
	cdSeesAll(t, a, "off", ids)
	st.people[0].Online()
	cdPush(a, "offline data")
	b.Git("pull", "-q", "--no-rebase", "origin", "main")
	cdSeesAll(t, b, "off", ids)
}

// TestCampaignData_ConcurrentWrites: two processes in one clone at once —
// distinct variables both land; the same tag name twice gives one tag and
// one clean E_NAME_TAKEN; metadata on one row from both keeps both keys.
func TestCampaignData_ConcurrentWrites(t *testing.T) {
	c := resRepo(t)
	m := cdMemory(t, c, "shared row")
	run := func(args ...[]string) []th.Result {
		out := make([]th.Result, len(args))
		var wg sync.WaitGroup
		for i, a := range args {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out[i] = c.Lore(a...)
			}()
		}
		wg.Wait()
		return out
	}
	for _, r := range run([]string{"vars", "set", "left", "1"}, []string{"vars", "set", "right", "2"}) {
		r.OK(t)
	}
	if cdVarValue(t, c, "left") != "1" || cdVarValue(t, c, "right") != "2" {
		t.Error("a concurrent vars set was lost")
	}
	rs := run([]string{"tag", "add", "--name=race", "--json"}, []string{"tag", "add", "--name=race", "--json"})
	ok := 0
	for _, r := range rs {
		if r.Code == 0 {
			ok++
			continue
		}
		if e := restCLIErr(r); e == nil || (e.Code != "E_NAME_TAKEN" && !e.Retryable) {
			t.Errorf("the losing tag add: %s", r)
		}
	}
	if ok == 0 {
		t.Fatal("neither concurrent tag add succeeded")
	}
	if n := strings.Count(c.MustLore("tag", "list").Stdout, "race"); n != 1 {
		t.Errorf("tag race listed %d times", n)
	}
	for _, r := range run([]string{"meta", "set", m, "left_key=1"}, []string{"meta", "set", m, "right_key=2"}) {
		r.OK(t)
	}
	if got := cdMetaOf(t, c, m); got["left_key"] != "1" || got["right_key"] != "2" {
		t.Errorf("concurrent meta set on one row kept %v", got)
	}
}
