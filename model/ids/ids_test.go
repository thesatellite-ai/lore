package ids

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

func TestNewParseRoundTrip(t *testing.T) {
	t.Parallel()
	before := time.Now().Add(-time.Second)
	id, err := New[Memory]()
	if err != nil {
		t.Fatal(err)
	}
	s := id.String()
	if !strings.HasPrefix(s, "mem_") || len(s) != idLen {
		t.Fatalf("bad id %q", s)
	}
	back, err := Parse[Memory](s)
	if err != nil || back != id {
		t.Fatalf("round trip %v %v", back, err)
	}
	if id.Time().Before(before) || id.Time().After(time.Now().Add(time.Second)) {
		t.Fatalf("embedded time %v", id.Time())
	}
	if _, err := Parse[Actor](s); !errs.Has(err, errs.InvalidID) {
		t.Fatalf("wrong-table parse err = %v", err)
	}
	a := id.Any()
	if a.Prefix() != PrefixMemory {
		t.Fatal("prefix")
	}
	if n, err := As[Memory](a); err != nil || n != id {
		t.Fatal("As")
	}
}

func TestParseRejects(t *testing.T) {
	t.Parallel()
	v4 := "mem_" + "0193" + "6f2a" + "7c4b" + "4e1d" + "8a3f5b6c7d8e9f00" // version nibble 4
	cases := map[string]string{
		"empty":         "",
		"short":         "mem_01",
		"no sep":        "mem-01936f2a7c4b7e1d8a3f5b6c7d8e9f00",
		"upper prefix":  "MEM_01936f2a7c4b7e1d8a3f5b6c7d8e9f00",
		"upper hex":     "mem_01936F2A7C4B7E1D8A3F5B6C7D8E9F00",
		"unregistered":  "zzz_01936f2a7c4b7e1d8a3f5b6c7d8e9f00",
		"not v7":        v4,
		"non hex":       "mem_01936f2a7c4b7e1d8a3f5b6c7d8e9fzz",
		"long":          "mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f000",
		"unicode tail":  "mem_01936f2a7c4b7e1d8a3f5b6c7d8e9fé",
		"digits prefix": "m3m_01936f2a7c4b7e1d8a3f5b6c7d8e9f00",
	}
	for name, s := range cases {
		if _, err := ParseAny(s); !errs.Has(err, errs.InvalidID) {
			t.Errorf("%s: %q accepted (err %v)", name, s, err)
		}
	}
	if _, err := ParseAny("mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00"); err != nil {
		t.Fatalf("spec example rejected: %v", err)
	}
}

func TestJSON(t *testing.T) {
	t.Parallel()
	type doc struct {
		ID    ID[Memory] `json:"id"`
		Opt   ID[Actor]  `json:"opt"`
		Other AnyID      `json:"other"`
	}
	id, err := New[Memory]()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(doc{ID: id, Other: id.Any()})
	if err != nil {
		t.Fatal(err)
	}
	var back doc
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.ID != id || !back.Opt.IsZero() || back.Other != id.Any() {
		t.Fatalf("json round trip %+v", back)
	}
	bad := strings.Replace(string(b), `"id":"mem_`, `"id":"act_`, 1)
	if err := json.Unmarshal([]byte(bad), &back); err == nil {
		t.Fatal("wrong prefix accepted by JSON")
	}
}

func TestPrefixesUniqueAndValid(t *testing.T) {
	t.Parallel()
	seen := map[Prefix]bool{}
	for _, p := range Prefixes() {
		if !p.Valid() || seen[p] {
			t.Errorf("prefix %q invalid or duplicate", p)
		}
		seen[p] = true
	}
	for _, tag := range []Tag{Memory{}, Actor{}, Store{}, Repo{}, Project{}, Machine{}, Policy{}, MetaKey{}, MetaValue{}, Kind{}, KindField{}, Var{}, SecretRule{}, SecretAllow{}, Resource{}} {
		if !tag.Prefix().Registered() {
			t.Errorf("tag prefix %q unregistered", tag.Prefix())
		}
	}
}

func FuzzParseAny(f *testing.F) {
	f.Add("mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00")
	f.Add("")
	f.Add("act_01936f0000007a1b8c2d3e4f5a6b7c8d")
	f.Fuzz(func(t *testing.T, s string) {
		id, err := ParseAny(s)
		if err != nil {
			if !errs.Has(err, errs.InvalidID) {
				t.Fatalf("non E_INVALID_ID error %v", err)
			}
			return
		}
		if id.String() != s {
			t.Fatalf("round trip changed %q to %q", s, id.String())
		}
		var back AnyID
		if err := back.UnmarshalText([]byte(s)); err != nil || back != id {
			t.Fatalf("text round trip %v", err)
		}
	})
}
