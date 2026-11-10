//go:build teame2e

package teame2e

// teame2e_storepush_attached_test.go — store push modes and compaction on
// a lore-only store attached in .lore/stores.yaml (decided 2026-11-10,
// land7 A): its settings live in its lore-store.yaml as an org store's
// do, and whoever can push to it may change them (no org, so no admin);
// an org's store attached by url alone stays that org's (admin only).

import "testing"

// Remote and name of the attached store.
const (
	spAttachedSlug  = "github.com/acme/kb"
	spAttachedStore = "kb"
)

// newSPAttached: store kb attached in stores.yaml by a (store init) and b
// (store add), no org anywhere.
func newSPAttached(t *testing.T) *spPair {
	t.Helper()
	w := newTeamWorld(t)
	p := &spPair{w: w, store: w.Remote(spAttachedSlug)}
	p.a = w.Human("alice", "alice@example.test")
	p.b = w.Human("bob", "bob@example.test")
	p.a.SetNow(spOld)
	p.b.SetNow(spOld)
	p.ca, p.cb = spCodeClone(w, p.a), spCodeClone(w, p.b)
	p.ca.MustLore("store", "init", spAttachedStore, p.store.URL())
	p.cb.MustLore("store", "add", spAttachedStore, p.store.URL())
	return p
}

// A stores.yaml store takes push=batch from `lore store set` (refused
// before: "not the personal store nor a store of a configured org"), the
// other clone reads the same shared setting after a sync, batches its own
// writes into one pushed commit, and the store compacts like an org's.
// Break-verified: with placeOf refusing stores outside an org again, the
// first set fails E_REFUSED.
func TestE55_StorePushAttachedStoreSetBatchCompact(t *testing.T) {
	p := newSPAttached(t)
	for _, b := range []string{"kb old one", "kb old two", "kb old three"} {
		p.add(t, p.ca, spAttachedStore, b)
	}
	p.a.SetNow(spNow)
	p.b.SetNow(spNow)
	if v := spShow(t, p.ca, spAttachedStore); v.Push.Place != "store" || v.Push.Push != "now" {
		t.Fatalf("show: want place store, push now, got %+v", v.Push)
	}
	p.ca.MustLore("store", "set", spAttachedStore, "push=batch", "push_every=10m").
		Says(t, "kb: push batch every 10m", "anyone who can push to the store may change it")
	if tip := p.log()[0]; tip != "lore: store set "+spAttachedStore {
		t.Fatalf("the setting is one pushed commit to the store, tip %q", tip)
	}
	// bob reads the shared setting after his sync and batches his writes.
	p.cb.MustLore("sync")
	if v := spShow(t, p.cb, spAttachedStore); v.Push.Push != "batch" || v.Push.PushEvery != "10m" {
		t.Fatalf("the other clone must see the store's batch setting, got %+v", v.Push)
	}
	before := p.log()
	p.add(t, p.cb, spAttachedStore, "kb bob one")
	p.add(t, p.cb, spAttachedStore, "kb bob two")
	spSameLog(t, "batched writes inside push_every", p.log(), before)
	p.cb.MustLore("sync").Says(t, "store kb: sent")
	spOneBatchOnTop(t, before, p.log(), 2)
	// The store's history compacts (asks first, then folds the old writes).
	p.ca.MustLore("sync")
	p.ca.Lore("store", "compact", spAttachedStore).Fails(t, spExitNeedsConfirm, spCodeConfirm, "--yes")
	p.ca.MustLore("store", "compact", spAttachedStore, "--yes").Says(t, "compacted kb", "epoch 0 → 1")
	p.cb.MustLore("sync")
	if v := spShow(t, p.cb, spAttachedStore); v.Push.Epoch != 1 {
		t.Fatalf("the other clone moves to epoch 1 after its sync, got %d", v.Push.Epoch)
	}
	spSees(t, p.cb, "kb old one", "kb old two", "kb old three", "kb bob one", "kb bob two")
	// The code repo is still refused.
	p.ca.Lore("store", "compact", "repo", "--yes").Fails(t, spExitRefused, spCodeRefused, "code repository")
}

// An org's store attached only in stores.yaml (bob never imported the org)
// is still the org's: its lore-org.yaml names the org, so bob, who has no
// admin setup of it, is refused set and compact and nothing moves.
// Break-verified by dropping refinePlace (bob's set lands on the remote).
func TestE56_StorePushAttachedOrgStoreStaysAdminOnly(t *testing.T) {
	w := newTeamWorld(t)
	p := &spPair{w: w, store: w.Remote(spOrgSlug)}
	p.a = w.Human("alice", "alice@example.test")
	p.b = w.Human("bob", "bob@example.test")
	p.a.SetNow(spNow)
	p.b.SetNow(spNow)
	p.ca, p.cb = spCodeClone(w, p.a), spCodeClone(w, p.b)
	p.ca.MustLore("store", "init", spOrgStore, p.store.URL())
	p.ca.MustLore("org", "create", spOrg, "--store", spOrgStore, "--key", "human:alice@example.test", "--name", "Alice", "--yes")
	p.cb.MustLore("store", "add", spOrgStore, p.store.URL()) // no `org import`
	before := p.log()
	p.cb.Lore("store", "set", spOrgStore, "push=batch").Fails(t, spExitRefused, spCodeRefused, "only an admin of "+spOrg)
	p.cb.Lore("store", "compact", spOrgStore, "--yes").Fails(t, spExitRefused, spCodeRefused, "only an admin of "+spOrg)
	spSameLog(t, "refused set and compact by a non-member", p.log(), before)
	if v := spShow(t, p.cb, spOrgStore); v.Push.Place != "org" || v.Push.Push != "now" {
		t.Fatalf("show: want the org's store, push now, got %+v", v.Push)
	}
}
