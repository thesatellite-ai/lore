//go:build teame2e

package teame2e

import (
	"strings"
	"testing"
)

// compact=on on the personal store: the first push of a normal command
// compacts the history older than compact_after into one snapshot (every
// row kept), and says so; compaction never runs with compact=off.
func TestE53_StorePushAutoCompactsPersonal(t *testing.T) {
	p := newSPPersonal(t, spOld)
	old := []string{"auto old one", "auto old two", "auto old three"}
	for _, b := range old {
		p.add(t, p.ca, "me", b)
	}
	p.a.SetNow(spNow)
	p.b.SetNow(spNow)
	// compact off (the default): a write never rewrites history.
	p.add(t, p.ca, "me", "while off")
	if log := p.log(); strings.HasPrefix(log[0], spCompactSubject) {
		t.Fatalf("compact off rewrote the history: %q", log)
	}
	p.ca.MustLore("store", "set", "me", "compact=on", "compact_after=30d").Says(t, "compact on after 30d")
	r := p.ca.MustLore("memory", "add", "triggers the compaction", "--to", "me")
	r.Says(t, "compacted its history")
	p.spCompacted(t, 1, 2) // the two writes at spNow are kept
	spSees(t, p.ca, append(old, "while off", "triggers the compaction")...)
	// Within the day, another push compacts nothing more.
	before := p.log()
	p.add(t, p.ca, "me", "same day")
	if after := p.log(); len(after) != len(before)+1 {
		t.Fatalf("a second push within the day: want one more commit, got %q after %q", after, before)
	}
	// The other machine re-fetches the compacted history and keeps going.
	p.cb.MustLore("sync")
	spSees(t, p.cb, append(old, "while off", "triggers the compaction", "same day")...)
}

// compact=on on an org store compacts only from an admin's machine: the
// member's pushes never rewrite the shared history, the admin's first push
// does, and the member then re-fetches with its rows intact.
func TestE54_StorePushAutoCompactsOrgFromAdminOnly(t *testing.T) {
	p := newSPOrg(t, spOld)
	for _, b := range []string{"org old one", "org old two"} {
		p.add(t, p.ca, spOrgStore, b)
	}
	p.cb.MustLore("sync")
	p.a.SetNow(spNow)
	p.b.SetNow(spNow)
	spMakeAdmin(t, p.a)
	p.ca.MustLore("store", "set", spOrgStore, "compact=on", "compact_after=30d")
	p.cb.MustLore("sync")
	// The member writes: pushed, never compacted.
	p.add(t, p.cb, spOrgStore, "member write")
	if log := p.log(); strings.HasPrefix(log[0], spCompactSubject) {
		t.Fatalf("a member's push compacted the org store: %q", log)
	}
	// The admin writes: its push compacts.
	p.ca.MustLore("memory", "add", "admin write", "--to", spOrgStore).Says(t, "compacted its history")
	if log := p.log(); !strings.HasPrefix(log[0], spCompactSubject) {
		t.Fatalf("the admin's push did not compact: %q", log)
	}
	p.cb.MustLore("sync")
	spSees(t, p.cb, "org old one", "org old two", "member write", "admin write")
}
