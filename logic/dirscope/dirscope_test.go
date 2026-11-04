package dirscope

import "testing"

func TestParseAndClean(t *testing.T) {
	ok := map[string]Spec{
		"":                 {},
		"repo":             {Kind: KindRepo},
		"project":          {Kind: KindProject},
		"global":           {Kind: KindGlobal},
		"org:acme":         {Kind: KindOrg, Org: "acme"},
		"dir:src/booking/": {Kind: KindDir, Path: "src/booking"},
		"dir:./src//a":     {Kind: KindDir, Path: "src/a"},
		`dir:src\win`:      {Kind: KindDir, Path: "src/win"},
		"dir:src/a/../b":   {Kind: KindDir, Path: "src/b"},
		"  dir:été/日本  ":   {Kind: KindDir, Path: "été/日本"},
	}
	for in, want := range ok {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"dir:", "dir:.", "dir:/etc", "dir:../x", "dir:a/../..", "dir:C:/x", "folder", "glob"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q): want an error", bad)
		}
	}
}

func TestWider(t *testing.T) {
	prj, repo := Place{}, Place{Repo: "r"}
	a, ab, other := Place{Repo: "r", Path: "a"}, Place{Repo: "r", Path: "a/b"}, Place{Repo: "r", Path: "ax"}
	cases := []struct {
		wide, narrow Place
		want         bool
	}{
		{prj, repo, true}, {prj, ab, true}, {repo, a, true}, {a, ab, true},
		{ab, a, false}, {a, a, false}, {repo, repo, false}, {a, other, false},
		{Place{Repo: "s"}, a, false}, {repo, prj, false},
	}
	for _, c := range cases {
		if got := Wider(c.wide, c.narrow); got != c.want {
			t.Errorf("Wider(%+v, %+v) = %v", c.wide, c.narrow, got)
		}
	}
}

func TestChainParentMoved(t *testing.T) {
	dirs := []string{"src/booking/payments", "src/booking", "src/book", "web"}
	got := Chain(dirs, "src/booking/payments/refund.go")
	if len(got) != 2 || got[0] != "src/booking" || got[1] != "src/booking/payments" {
		t.Fatalf("Chain = %v", got)
	}
	if p := Parent(dirs, "src/booking/payments"); p != "src/booking" {
		t.Fatalf("Parent = %q", p)
	}
	if p := Parent(dirs, "web"); p != "" {
		t.Fatalf("Parent(web) = %q", p)
	}
	if m, ok := Moved("src/booking/payments", "src/booking", "src/res"); !ok || m != "src/res/payments" {
		t.Fatalf("Moved = %q %v", m, ok)
	}
	if _, ok := Moved("src/bookings", "src/booking", "x"); ok {
		t.Fatal("Moved matched a sibling prefix")
	}
}
