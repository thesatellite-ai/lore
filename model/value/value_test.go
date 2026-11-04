package value

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
)

// mustFor returns a helper that fails t on a constructor error.
func mustFor(t *testing.T) func(Value, error) Value {
	return func(v Value, err error) Value {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

// TestRoundTripEveryKind: for one valid value of every kind, marshal then
// Parse(kind) gives an Equal value with identical bytes.
func TestRoundTripEveryKind(t *testing.T) {
	t.Parallel()
	must := mustFor(t)
	ref, err := ids.ParseAny("mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00")
	if err != nil {
		t.Fatal(err)
	}
	ip := must(IPOf("203.0.113.10"))
	list, err := ListOf(KindIP, ip, must(IPOf("2001:db8::1")))
	if err != nil {
		t.Fatal(err)
	}
	f, ferr := Float(1.5)
	cases := map[Kind]Value{
		KindText:      must(TextOf("Retry <twice> & \"log\"")),
		KindNumber:    must(f, ferr),
		KindBool:      BoolOf(true),
		KindDate:      must(DateOf("2026-12-01")),
		KindDateTime:  must(DateTimeOf("2026-11-04T11:42:07.123+01:00")),
		KindDuration:  must(DurationOf("1.5h")),
		KindIP:        ip,
		KindCIDR:      must(CIDROf("10.0.0.0/8")),
		KindDNS:       must(DNSOf("DB.Hub.acme.example.")),
		KindURL:       must(URLOf("https://hub.acme.example/x?y=1")),
		KindEmail:     must(EmailOf("dev@acme.example")),
		KindJSON:      must(JSONOf([]byte(`{"b":[1,2],"a":null}`))),
		KindList:      list,
		KindRef:       must(RefOf(ref)),
		KindSecretRef: must(SecretRefOf("gopass://acme/hub_prod/ssh_deploy")),
		KindNull:      Null{},
	}
	for _, k := range Kinds() {
		v, ok := cases[k]
		if !ok {
			t.Errorf("no case for kind %s", k)
			continue
		}
		if v.Kind() != k {
			t.Errorf("%s: Kind() = %s", k, v.Kind())
		}
		b, err := v.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		var back Value
		if k == KindList {
			back, err = ParseList(KindIP, b)
		} else {
			back, err = Parse(k, b)
		}
		if err != nil {
			t.Errorf("%s: Parse(%s): %v", k, b, err)
			continue
		}
		if !Equal(v, back) {
			t.Errorf("%s: round trip %s != %v", k, b, back)
		}
	}
	if cases[KindDNS].String() != "db.hub.acme.example" {
		t.Error("dns not normalised")
	}
	if cases[KindDateTime].String() != "2026-11-04T10:42:07.123000000Z" {
		t.Errorf("datetime = %s", cases[KindDateTime])
	}
	if cases[KindJSON].String() != `{"a":null,"b":[1,2]}` {
		t.Error("json not canonical")
	}
}

func TestConstructorsReject(t *testing.T) {
	t.Parallel()
	bad := map[string]func() error{
		"ip":           func() error { _, err := IPOf("203.0.113"); return err },
		"cidr":         func() error { _, err := CIDROf("10.0.0.0/33"); return err },
		"dns ip":       func() error { _, err := DNSOf("203.0.113.10"); return err },
		"dns char":     func() error { _, err := DNSOf("a_b.example"); return err },
		"dns hyphen":   func() error { _, err := DNSOf("-a.example"); return err },
		"url relative": func() error { _, err := URLOf("/path"); return err },
		"email name":   func() error { _, err := EmailOf("Dev <dev@acme.example>"); return err },
		"date":         func() error { _, err := DateOf("2026-13-01"); return err },
		"datetime":     func() error { _, err := DateTimeOf("yesterday"); return err },
		"duration":     func() error { _, err := DurationOf("2d"); return err },
		"number":       func() error { _, err := NumberOf("01"); return err },
		"number space": func() error { _, err := NumberOf(" 1"); return err },
		"nan":          func() error { _, err := Float(nanValue()); return err },
		"json":         func() error { _, err := JSONOf([]byte(`{"a":1,"a":2}`)); return err },
		"text utf8":    func() error { _, err := TextOf("\xff"); return err },
		"ref zero":     func() error { _, err := RefOf(ids.AnyID{}); return err },
		"list mixed":   func() error { _, err := ListOf(KindText, BoolOf(true)); return err },
		"list nil":     func() error { _, err := ListOf(KindText, nil); return err },
		"parse kind":   func() error { _, err := Parse(KindIP, []byte(`5`)); return err },
		"parse bool":   func() error { _, err := Parse(KindBool, []byte(`"true"`)); return err },
		"kind word":    func() error { _, err := ParseKind("string"); return err },
	}
	for name, f := range bad {
		if f() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// URL CREDENTIALS FIX (D24/SQ13): credentials are stripped, not
	// refused. Built from two parts so no secret scanner flags the fixture.
	urlCases := []struct {
		in, want string
		stripped bool
	}{
		{"https://u:" + "p@host.example/", "https://host.example/", true},
		{"https://u@host.example/", "https://host.example/", true},
		{"ssh://git:" + "p@host.example/a", "ssh://git@host.example/a", true},
		{"ssh://git@host.example/a", "ssh://git@host.example/a", false},
		{"https://host.example/a?q=1", "https://host.example/a?q=1", false},
	}
	for _, c := range urlCases {
		u, err := URLOf(c.in)
		if err != nil || u.String() != c.want || u.Stripped() != c.stripped {
			t.Errorf("URLOf -> %q %v %v; want %q %v", u.String(), u.Stripped(), err, c.want, c.stripped)
		}
	}
}

func nanValue() float64 {
	zero := 0.0
	return zero / zero
}

func TestNumberKeepsLiteral(t *testing.T) {
	t.Parallel()
	for _, lit := range []string{"1.50", "12345678901234567890", "-0.0e5", "3"} {
		v, err := Parse(KindNumber, []byte(lit))
		if err != nil || v.String() != lit {
			t.Errorf("%s -> %v %v", lit, v, err)
		}
	}
	n := Int(42)
	if i, err := n.Int64(); err != nil || i != 42 {
		t.Fatal("Int64")
	}
}

func TestInfer(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]Kind{
		`"x"`: KindText, `1`: KindNumber, `false`: KindBool, `null`: KindNull,
		`{"a":1}`: KindJSON, `["a","b"]`: KindList, `[]`: KindList, `[1,"a"]`: KindJSON,
	} {
		v, err := Infer([]byte(in))
		if err != nil || v.Kind() != want {
			t.Errorf("Infer(%s) = %v %v, want %s", in, v, err, want)
			continue
		}
		b, err := v.MarshalJSON()
		if err != nil || string(b) != in {
			t.Errorf("Infer(%s) re-marshals to %s", in, b)
		}
	}
	if _, err := Infer([]byte(`{`)); err == nil {
		t.Error("invalid json inferred")
	}
}

func TestDateTimeDropsMonotonic(t *testing.T) {
	t.Parallel()
	now := time.Now()
	a, b := DateTimeFrom(now), DateTimeFrom(now)
	if !Equal(a, b) || a.Time().Location() != time.UTC {
		t.Fatal("datetime not normalised")
	}
}

func FuzzInfer(f *testing.F) {
	f.Add([]byte(`["a","b"]`))
	f.Add([]byte(`{"x":[1,2,{"y":null}]}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		v, err := Infer(b)
		if err != nil {
			return
		}
		out, err := v.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		v2, err := Infer(out)
		if err != nil || !Equal(v, v2) {
			t.Fatalf("infer not stable: %q -> %q", b, out)
		}
	})
}
