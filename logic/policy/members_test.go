package policy

import (
	"slices"
	"testing"
)

// TestMemberWritableKeys pins the D-681 exemption list: the exact branch
// names lore branch share|isolate write are a member's, and every other
// key (the branch rules for all branches at once among them) stays an
// admin's in a company folder.
func TestMemberWritableKeys(t *testing.T) {
	want := []Key{KeyBranchesShared, KeyBranchesIsolated}
	if got := MemberWritableKeys(); !slices.Equal(got, want) {
		t.Fatalf("member keys = %v, want %v", got, want)
	}
	for _, k := range want {
		if _, ok := Lookup(k); !ok {
			t.Errorf("member key %s is not a policy key", k)
		}
		if !MemberWritable(k) {
			t.Errorf("MemberWritable(%s) = false, want true", k)
		}
	}
	for _, d := range Keys() {
		if slices.Contains(want, d.Key) {
			continue
		}
		if MemberWritable(d.Key) {
			t.Errorf("MemberWritable(%s) = true: only an admin may change it in a company folder", d.Key)
		}
	}
	for _, k := range []Key{KeyBranchesMainLike, KeyBranchesIsolate, KeyBranchesDefaultMode, KeyBranchesLayers, KeyBranchesAutoLand, KeyBranchesOrphanDays} {
		if MemberWritable(k) {
			t.Errorf("MemberWritable(%s) = true: it sets the rule for every branch, an admin's", k)
		}
	}
}
