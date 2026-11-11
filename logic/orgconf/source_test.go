package orgconf

import "testing"

func TestStateOfAndRefresh(t *testing.T) {
	t.Parallel()
	const sto, v1, v2 = "sto_01a1bf3249ed7319a7f26569ae4e79f6", "aaaa111", "bbbb222"
	o := &Org{Name: "acme", Stores: []Store{{URL: "git@h:acme/store.git", OrgFolder: "org"}}, Own: []string{"h/acme/*"}}
	if got := StateOf(o, sto, v1); got != SourceUnstamped {
		t.Fatalf("no stamp: %s", got)
	}
	moved := Shared{Stores: []Store{{URL: "git@h:acme/store2.git", OrgFolder: "org"}}, Own: []string{"h/acme/*"}}
	if !Refresh(o, moved, OrgSource{Store: sto, Version: v1}) {
		t.Fatal("a moved store url is a change")
	}
	if o.Stores[0].URL != "git@h:acme/store2.git" || o.Source == nil || o.Source.Version != v1 {
		t.Fatalf("refresh did not copy: %+v", o)
	}
	if got := StateOf(o, sto, v1); got != SourceCurrent {
		t.Fatalf("same version: %s", got)
	}
	if got := StateOf(o, sto, v2); got != SourceStale {
		t.Fatalf("newer file: %s", got)
	}
	if got := StateOf(o, "sto_01a1bf3249ed7319a7f26569ae4e7000", v1); got != SourceOtherStore {
		t.Fatalf("another store's file: %s", got)
	}
	if Refresh(o, moved, OrgSource{Store: sto, Version: v2}) {
		t.Fatal("restamping the same fields is no change")
	}
	// An empty branch is the default branch; nil and empty lists are equal.
	a := Shared{Stores: []Store{{URL: "u", Branch: ""}}}
	b := Shared{Stores: []Store{{URL: "u", Branch: DefaultStoreBranch}}, Own: []string{}}
	if !a.Equal(b) {
		t.Fatal("default branch spelled two ways differs")
	}
}

// TestRefreshCopiesDefaults (owner report, 0.1.18-dev): defaults is one of
// the copied fields, like stores, own and repos: a refresh adds it,
// replaces it when the store's file changed it, and removes it when the
// file has none; a different defaults map is a change.
func TestRefreshCopiesDefaults(t *testing.T) {
	t.Parallel()
	const sto = "sto_01a1bf3249ed7319a7f26569ae4e79f6"
	stores := []Store{{URL: "git@h:acme/store.git", OrgFolder: "org"}}
	o := &Org{Name: "acme", Stores: stores}
	with := func(m map[string]string) Shared {
		return Shared{Stores: stores, Defaults: OrgDefaults{ClientRepos: m}}
	}
	if !Refresh(o, with(map[string]string{"task": "acme"}), OrgSource{Store: sto, Version: "v1"}) {
		t.Fatal("new defaults are a change")
	}
	if got := o.Defaults.ClientRepos["task"]; got != "acme" {
		t.Fatalf("defaults not copied: %+v", o.Defaults)
	}
	src := map[string]string{"task": "repo", "memory": "acme"}
	if !Refresh(o, with(src), OrgSource{Store: sto, Version: "v2"}) {
		t.Fatal("changed defaults are a change")
	}
	src["task"] = "acme" // the entry holds its own copy, not the caller's map
	if o.Defaults.ClientRepos["task"] != "repo" || o.Defaults.ClientRepos["memory"] != "acme" {
		t.Fatalf("defaults not replaced: %+v", o.Defaults)
	}
	if Refresh(o, with(map[string]string{"task": "repo", "memory": "acme"}), OrgSource{Store: sto, Version: "v3"}) {
		t.Fatal("the same defaults are no change")
	}
	if !Refresh(o, with(nil), OrgSource{Store: sto, Version: "v4"}) {
		t.Fatal("removed defaults are a change")
	}
	if len(o.Defaults.ClientRepos) != 0 {
		t.Fatalf("defaults not removed: %+v", o.Defaults)
	}
	if !with(nil).Equal(with(map[string]string{})) {
		t.Fatal("no defaults and empty defaults differ")
	}
}
