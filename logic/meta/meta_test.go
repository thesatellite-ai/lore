package meta

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestMetaGrammar is the 3.6 test table (filters and writes share it).
func TestMetaGrammar(t *testing.T) {
	t.Parallel()
	ok := []struct {
		arg, level, place, key, typ string
		op                          Op
		val, path                   string
	}{
		{"client=acme", "", "", "client", "", OpEq, "acme", ""},
		{"cost_hours:number=3.5", "", "", "cost_hours", "number", OpEq, "3.5", ""},
		{"cost_hours>=2", "", "", "cost_hours", "", OpGe, "2", ""},
		{"formula=a>b", "", "", "formula", "", OpEq, "a>b", ""},
		{"project:jira=HUB-420", "project", "", "jira", "", OpEq, "HUB-420", ""},
		{"repo:acmehub_backend:status_code>400", "repo", "acmehub_backend", "status_code", "", OpGt, "400", ""},
		{"repo:build:text=007", "repo", "", "build", "text", OpEq, "007", ""},
		{"org:agency:billing_rate:number=90", "org", "agency", "billing_rate", "number", OpEq, "90", ""},
		{"jira?", "", "", "jira", "", OpHas, "", ""},
		{"jira!?", "", "", "jira", "", OpMissing, "", ""},
		{"client~=ACME", "", "", "client", "", OpLike, "ACME", ""},
		{"deploy.region!=eu-central", "", "", "deploy.region", "", OpNe, "eu-central", ""},
		{"order_snapshot$.items[*].sku=AB-1", "", "", "order_snapshot", "", OpEq, "AB-1", "$.items[*].sku"},
		{"note=10:30 standup", "", "", "note", "", OpEq, "10:30 standup", ""},
		{"egress_ip<<2001:db8::/32", "", "", "egress_ip", "", OpIn, "2001:db8::/32", ""},
		{"export_timeout:duration>=1h", "", "", "export_timeout", "duration", OpGe, "1h", ""},
		{"pages:list<number>=1,2", "", "", "pages", "list<number>", OpEq, "1,2", ""},
	}
	for _, c := range ok {
		got, err := Parse(c.arg)
		if err != nil {
			t.Fatalf("%s: %v", c.arg, err)
		}
		typ := ""
		if !got.Type.IsZero() {
			typ = got.Type.String()
		}
		if got.Level != c.level || got.Place != c.place || got.Key != c.key || typ != c.typ || got.Op != c.op || got.Value != c.val || got.Path != c.path {
			t.Errorf("%s: got %+v", c.arg, got)
		}
	}
	for _, bad := range []string{"backend:status_code=404", "list:x=1", "cost_hours=>2", "=x", "client", "jira?x"} {
		if _, err := Parse(bad); !errs.Has(err, errs.MetaOp) {
			t.Errorf("%s: want E_META_OP, got %v", bad, err)
		}
	}
	if _, err := ParseWrite("a>2"); !errs.Has(err, errs.MetaOp) {
		t.Errorf("write with > : %v", err)
	}
}

func TestNameConvert(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Story Points": "story_points", "In Progress": "in_progress", "jiraTicket": "jira_ticket", "HTTPStatus": "http_status",
		"Deploy-Region": "deploy_region", "v1.2": "v1_2", "Café au lait": "cafe_au_lait", "QA/Review": "qareview",
		"2FA Method": "n_2fa_method", "  Won't Fix  ": "wont_fix", "---": "",
	} {
		if got := ToSnake(in); got != want {
			t.Errorf("ToSnake(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckName(t *testing.T) {
	t.Parallel()
	for name, code := range map[string]errs.Code{
		"client": "", "deploy.region": "", "a.b.c.d": "", "a.b.c.d.e": errs.MetaDepth, "jiraTicket": errs.MetaKey,
		"2fa": errs.MetaKey, "a__b": errs.MetaKey, "a_": errs.MetaKey, "deploy..x": errs.MetaKey, "сlient": errs.MetaKey,
	} {
		err := CheckName(name, 0)
		if (code == "" && err != nil) || (code != "" && !errs.Has(err, code)) {
			t.Errorf("CheckName(%q) = %v, want %q", name, err, code)
		}
	}
	cols := map[string]bool{"status": true}
	for _, name := range []string{"status", "id", "meta", "_w", "mky_x", "val", "text", "list", "repo", "org"} {
		if err := Reserved(name, cols); !errs.Has(err, errs.MetaReserved) {
			t.Errorf("Reserved(%q) = %v", name, err)
		}
	}
	if err := Reserved("deploy.date", cols); err != nil {
		t.Errorf("deploy.date: %v", err)
	}
	if !TagLike("tags") || TagLike("client") {
		t.Error("TagLike")
	}
}

func TestCoerce(t *testing.T) {
	t.Parallel()
	num := Type{Kind: value.KindNumber}
	cases := []struct {
		t        Type
		in, want string
		code     errs.Code
	}{
		{Type{}, "  acme ", `"acme"`, ""},
		{Type{}, "007", `"007"`, ""},
		{Type{}, "a,b", `"a,b"`, ""},
		{Type{}, "   ", "", errs.MetaValue},
		{num, "3.50", "3.50", ""},
		{num, "-0", "0", ""},
		{num, "1,000", "", errs.MetaType},
		{num, "NaN", "", errs.MetaType},
		{num, "99999999999999999999", "", errs.MetaType},
		{Type{Kind: value.KindBool}, "True", "", errs.MetaType},
		{Type{Kind: value.KindDate}, "2026-12-01", `"2026-12-01"`, ""},
		{Type{Kind: value.KindDate}, "2026-13-01", "", errs.MetaType},
		{Type{Kind: value.KindList, Elem: value.KindNumber}, "1,2", "[1,2]", ""},
	}
	for _, c := range cases {
		got, err := Coerce(c.t, c.in)
		if c.code != "" {
			if !errs.Has(err, c.code) {
				t.Errorf("Coerce(%v,%q) = %v, want %s", c.t, c.in, err, c.code)
			}
			continue
		}
		if err != nil || string(got.Raw) != c.want {
			t.Errorf("Coerce(%v,%q) = %s, %v; want %s", c.t, c.in, got.Raw, err, c.want)
		}
	}
	for raw, code := range map[string]errs.Code{"null": errs.MetaValue, `""`: errs.MetaValue, "[]": errs.MetaValue, "{}": errs.MetaValue,
		`[[1]]`: errs.MetaShape, `[1,"a"]`: errs.MetaShape, `[{"a":1}]`: errs.MetaShape} {
		if _, err := InferType([]byte(raw)); !errs.Has(err, code) {
			t.Errorf("InferType(%s) = %v, want %s", raw, err, code)
		}
	}
}

func TestEntryRoundTripAndPaths(t *testing.T) {
	t.Parallel()
	e := Entry{Name: "deploy", Scope: "repo", Strict: true, V: []byte(`{"env":"prod"}`)}
	j, err := e.Encode()
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeEntry(j)
	if err != nil || back.Name != "deploy" || !back.Strict || string(back.V) != `{"env":"prod"}` {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	v, err := SetPath(back.V, []string{"region"}, []byte(`"eu"`))
	if err != nil || string(v) != `{"env":"prod","region":"eu"}` {
		t.Fatalf("SetPath: %s %v", v, err)
	}
	v, empty, found, err := UnsetPath(v, []string{"env"})
	if err != nil || empty || !found || string(v) != `{"region":"eu"}` {
		t.Fatalf("UnsetPath: %s %v %v %v", v, empty, found, err)
	}
	if _, empty, _, _ = UnsetPath(v, []string{"region"}); !empty {
		t.Fatal("last field should empty the object")
	}
	if _, err := CheckSize([]Entry{{Name: "x", V: []byte(`"` + string(make([]byte, 0)) + `abcdefghij"`)}}, 5); !errs.Has(err, errs.MetaSize) {
		t.Fatalf("CheckSize: %v", err)
	}
	if Show(Entry{Sensitive: true, V: []byte(`"s"`)}) != "•••" {
		t.Fatal("sensitive not masked")
	}
}
