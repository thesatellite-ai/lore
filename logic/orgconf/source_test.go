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
