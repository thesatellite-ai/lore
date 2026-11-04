package formatgate

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

func TestCheckMatrix(t *testing.T) {
	binaries := []string{"0.1.19", "0.1.19-dev", "v0.1.19", "garbage", "0.1.17-34-gabc", ""}
	cases := []struct {
		name  string
		d     Declared
		write bool
	}{
		{"empty", Declared{}, true},
		{"known", Declared{Features: []string{Live, HLC, Meta}, MinLoreVersion: "0.1.18"}, true},
		{"unknown feature", Declared{Features: []string{Live, "zz_future"}}, false},
		{"floor above", Declared{Features: []string{Live}, MinLoreVersion: "0.2.0"}, false},
		{"bad floor + unknown", Declared{Features: []string{"zz_future"}, MinLoreVersion: "garbage"}, false},
		{"bad floor only", Declared{MinLoreVersion: "garbage"}, true},
		{"plugin only", Declared{Features: []string{"plugin/infra"}}, true},
	}
	for _, b := range binaries {
		for _, c := range cases {
			v := Check(b, c.d)
			if v.Writable != c.write {
				t.Errorf("%s/%s: writable %v, want %v", b, c.name, v.Writable, c.write)
			}
			if !v.Writable && !errs.Has(v.Err(), errs.OldLore) {
				t.Errorf("%s/%s: err %v", b, c.name, v.Err())
			}
		}
	}
}

func TestVersionBelow(t *testing.T) {
	for _, c := range []struct {
		b, f string
		want bool
	}{
		{"0.1.9", "0.1.19", true}, {"0.1.19", "0.1.9", false}, {"v0.1.19", "0.1.19", false},
		{"0.1.19-dev", "0.1.19", false}, {"garbage", "0.1.0", true}, {"0.1.0", "garbage", false},
		{"0.1.18", "", false},
	} {
		if got := VersionBelow(c.b, c.f); got != c.want {
			t.Errorf("VersionBelow(%q,%q)=%v", c.b, c.f, got)
		}
	}
	if MaxVersion("0.1.9", "0.1.19") != "0.1.19" || MaxVersion("garbage", "0.1.2") != "0.1.2" || MaxVersion("0.1.2", "") != "0.1.2" {
		t.Fatal("MaxVersion")
	}
}

func TestRaiseAndMerge(t *testing.T) {
	d, added := Raise(Declared{Features: []string{Live}}, []string{HLC, Meta, Live})
	if !slices.Equal(added, []string{HLC, Meta}) || d.MinLoreVersion != "0.1.18" || !slices.Equal(d.Features, []string{HLC, Live, Meta}) {
		t.Fatalf("Raise: %+v %v", d, added)
	}
	d2, added2 := Raise(d, []string{HLC})
	if len(added2) != 0 || d2.MinLoreVersion != "0.1.18" {
		t.Fatalf("Raise twice: %+v %v", d2, added2)
	}
	if hi, _ := Raise(Declared{MinLoreVersion: "0.2.0"}, []string{HLC}); hi.MinLoreVersion != "0.2.0" {
		t.Fatalf("Raise lowered the floor: %+v", hi)
	}
	m := Merge(Declared{Features: []string{Meta, Live}, MinLoreVersion: "0.1.9"}, Declared{Features: []string{HLC, Live}, MinLoreVersion: "0.1.19"})
	if !slices.Equal(m.Features, []string{HLC, Live, Meta}) || m.MinLoreVersion != "0.1.19" {
		t.Fatalf("Merge: %+v", m)
	}
}

func TestLockedTable(t *testing.T) {
	v := Check("0.1.18", Declared{Features: []string{"plugin/infra"}})
	owner := func(tb string) string {
		if tb == "infra_hosts" {
			return "infra"
		}
		return ""
	}
	if !v.LockedTable("infra_hosts", owner) || v.LockedTable("tasks", owner) || v.LockedTable("infra_hosts", nil) {
		t.Fatal("LockedTable")
	}
}
