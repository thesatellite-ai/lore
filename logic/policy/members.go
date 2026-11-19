package policy

import "slices"

// memberKeys is the one list of policy keys a member of the org may write
// in a company folder, where every other key is an admin's (devco D-681:
// company rules and shared settings such as the context budget apply to
// every repo that reads the folder, so only an admin changes them).
//
// A key belongs here only when it is a teammate's own workflow choice that
// the team docs promise to members ("a teammate shares a branch, the team
// sees it after a sync"):
//
//   - branches.shared: the exact branch names `lore branch share` chose.
//   - branches.isolated: the exact branch names `lore branch isolate` chose.
//
// Both name one branch at a time and only move that branch between its own
// layer and the shared notes; `lore branch share|isolate` write nothing else.
// The other branches.* keys stay admin-only because they set the rule for
// every branch at once: branches.main_like and branches.isolate are glob
// patterns over all branches, branches.default_mode decides every unnamed
// branch, branches.layers turns layers off for the whole folder, and
// branches.auto_land / branches.orphan_days change how every layer lands or
// expires.
var memberKeys = []Key{KeyBranchesShared, KeyBranchesIsolated}

// MemberWritable reports whether a member of the org may write key in a
// company folder (memberKeys). The role check of policy set/unset and the
// place a printed hint names ask this one function, so `lore policy set
// --key branches.shared` by a member behaves the same as `lore branch share`.
func MemberWritable(k Key) bool { return slices.Contains(memberKeys, k) }

// MemberWritableKeys is a copy of memberKeys, for docs and tests.
func MemberWritableKeys() []Key { return slices.Clone(memberKeys) }
