package config

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// fakePort is ~/.lore/config.yaml in memory; verdict, when set, is what
// the schema says about any candidate.
type fakePort struct {
	file    File
	writes  int
	verdict func(raw []byte) Verdict
	check   CheckOut
}

func (p *fakePort) ReadConfigFile(context.Context) (File, error) { return p.file, nil }

func (p *fakePort) WriteConfigFile(_ context.Context, f File) error {
	p.writes++
	p.file = f
	return nil
}

func (p *fakePort) ValidateConfig(_ context.Context, raw []byte) (Verdict, error) {
	if p.verdict == nil {
		return Verdict{}, nil
	}
	return p.verdict(raw), nil
}

func (p *fakePort) CheckConfig(context.Context) (CheckOut, error) { return p.check, nil }

const sample = `# machine config
live:
  autopush: true # push after each write
  enabled: true
lease:
  ttl: 30m
orgs:
  - name: acme
    store: file:///tmp/acme
  - name: beta
`

func newPort(raw string) *fakePort {
	return &fakePort{file: File{Path: "/home/u/.lore/config.yaml", Raw: []byte(raw), Exists: raw != "", Mode: 0o600}}
}

// run decodes args through the op named name and invokes it.
func run(t *testing.T, p Port, name string, args ...string) (string, error) {
	t.Helper()
	var e op.Entry
	for _, c := range Ops(p) {
		if c.Spec().Name == name {
			e = c
		}
	}
	if e == nil {
		t.Fatalf("no op %s", name)
	}
	in, err := e.Decode(op.ArgsDecoder{Args: args})
	if err != nil {
		return "", err
	}
	res, err := e.Invoke(context.Background(), in)
	if err != nil {
		return "", err
	}
	txt, _ := res.Text()
	return txt, nil
}

func TestOpsAreLocal(t *testing.T) {
	t.Parallel()
	for _, e := range Ops(newPort("")) {
		if !e.Spec().Local {
			t.Errorf("%s is not Local: config must work outside a repo (P3-CFG-1)", e.Spec().Name)
		}
	}
}

func TestGet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		key, want string
		code      errs.Code
	}{
		{key: "live.autopush", want: "true\n"},
		{key: "lease.ttl", want: "30m\n"},
		{key: "orgs.0.name", want: "acme\n"},
		{key: "orgs.1.name", want: "beta\n"},
		{key: "orgs.1.store", code: errs.NotFound},
		{key: "orgs.2.name", code: errs.NotFound},
		{key: "orgs.-1.name", code: errs.NotFound},
		{key: "orgs.x", code: errs.NotFound},
		{key: "live.autopush.deeper", code: errs.NotFound},
		{key: "nope", code: errs.NotFound},
		{key: "live..autopush", code: errs.InvalidInput},
		{key: ".live", code: errs.InvalidInput},
		{key: "live.", code: errs.InvalidInput},
		{key: "lease", want: "ttl: 30m\n"},
	}
	for _, c := range cases {
		got, err := run(t, newPort(sample), OpGet, c.key)
		if c.code != "" {
			if errs.CodeOf(err) != c.code {
				t.Errorf("get %q: err %v, want %s", c.key, err, c.code)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("get %q = %q, %v; want %q", c.key, got, err, c.want)
		}
	}
}

func TestGetOnAbsentAndBadFiles(t *testing.T) {
	t.Parallel()
	if _, err := run(t, newPort(""), OpGet, "live.autopush"); errs.CodeOf(err) != errs.NotFound {
		t.Errorf("absent file: %v", err)
	}
	if _, err := run(t, newPort("live: [unclosed"), OpGet, "live"); errs.CodeOf(err) != errs.ConfigInvalid {
		t.Errorf("unparsable file: %v", err)
	}
	if _, err := run(t, newPort("- a\n- b\n"), OpGet, "a"); errs.CodeOf(err) != errs.ConfigInvalid {
		t.Errorf("top-level list: %v", err)
	}
}

func TestList(t *testing.T) {
	t.Parallel()
	got, err := run(t, newPort(sample), OpList)
	if err != nil {
		t.Fatal(err)
	}
	want := "lease.ttl=30m\nlive.autopush=true\nlive.enabled=true\norgs.0.name=acme\norgs.0.store=file:///tmp/acme\norgs.1.name=beta\n"
	if got != want {
		t.Errorf("list:\n%s\nwant:\n%s", got, want)
	}
	if got, err := run(t, newPort(""), OpList); err != nil || got != "(no config keys)\n" {
		t.Errorf("empty list %q %v", got, err)
	}
}

func TestSetWritesAndKeepsTheFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, key, val string
		// contains are lines the written file must hold.
		contains []string
	}{
		{name: "overwrite keeps the comment", key: "live.autopush", val: "false", contains: []string{"autopush: false # push after each write", "# machine config"}},
		{name: "new key in an existing mapping", key: "live.ttl", val: "45m", contains: []string{"  ttl: 45m", "autopush: true"}},
		{name: "new nested mapping", key: "context.budget.dir", val: "4000", contains: []string{"context:\n  budget:\n    dir: 4000"}},
		{name: "list element field", key: "orgs.1.store", val: "file:///tmp/beta", contains: []string{"store: file:///tmp/beta", "name: beta"}},
		{name: "string stays a string", key: "identity.name", val: "Aman Bansal", contains: []string{"name: Aman Bansal"}},
		{name: "unicode value", key: "identity.name", val: "élan ✓", contains: []string{"name: élan ✓"}},
		{name: "value that is not one scalar is kept as text", key: "identity.name", val: "[a, b", contains: []string{"name: '[a, b'"}},
	}
	for _, c := range cases {
		p := newPort(sample)
		got, err := run(t, p, OpSet, c.key, c.val)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != "✓ "+c.key+" = "+c.val+" (/home/u/.lore/config.yaml)\n" {
			t.Errorf("%s: line %q", c.name, got)
		}
		if p.writes != 1 || p.file.Mode != 0o600 {
			t.Errorf("%s: writes %d mode %o", c.name, p.writes, p.file.Mode)
		}
		for _, w := range c.contains {
			if !strings.Contains(string(p.file.Raw), w) {
				t.Errorf("%s: file lacks %q:\n%s", c.name, w, p.file.Raw)
			}
		}
		// Read back through get: the value round-trips.
		if back, err := run(t, p, OpGet, c.key); err != nil || back != c.val+"\n" {
			t.Errorf("%s: get back %q %v", c.name, back, err)
		}
	}
}

func TestSetOnAbsentFile(t *testing.T) {
	t.Parallel()
	p := newPort("")
	if _, err := run(t, p, OpSet, "live.autopush", "false"); err != nil {
		t.Fatal(err)
	}
	if string(p.file.Raw) != "live:\n  autopush: false\n" {
		t.Errorf("new file %q", p.file.Raw)
	}
}

func TestSetRefusesAndWritesNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, key, val string
		verdict        func([]byte) Verdict
		code           errs.Code
	}{
		{name: "mapping in the way", key: "live", val: "x", code: errs.InvalidInput},
		{name: "list in the way", key: "orgs", val: "x", code: errs.InvalidInput},
		{name: "scalar in the way of a deeper key", key: "lease.ttl.x", val: "1", code: errs.InvalidInput},
		{name: "past the end of a list", key: "orgs.5.name", val: "x", code: errs.InvalidInput},
		{name: "bad dotted key", key: "a..b", val: "x", code: errs.InvalidInput},
		{name: "unknown key", key: "colour", val: "red", code: errs.InvalidInput,
			verdict: func([]byte) Verdict { return Verdict{Unknown: []string{"colour (line 12)"}} }},
		{name: "wrong type", key: "live.autopush", val: "maybe", code: errs.InvalidInput,
			verdict: func([]byte) Verdict { return Verdict{Decode: errors.New("not a bool")} }},
		{name: "rule broken", key: "orgs.0.name", val: "Bad Name", code: errs.ConfigInvalid,
			verdict: func([]byte) Verdict { return Verdict{Invalid: errors.New("org name empty")} }},
	}
	for _, c := range cases {
		p := newPort(sample)
		p.verdict = c.verdict
		_, err := run(t, p, OpSet, c.key, c.val)
		if errs.CodeOf(err) != c.code {
			t.Errorf("%s: err %v, want %s", c.name, err, c.code)
		}
		if p.writes != 0 || string(p.file.Raw) != sample {
			t.Errorf("%s: wrote the file", c.name)
		}
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()
	p := newPort("")
	p.check = CheckOut{Path: "/home/u/.lore/config.yaml"}
	got, err := run(t, p, OpCheck)
	if err != nil || got != "✓ /home/u/.lore/config.yaml does not exist: no orgs configured (every repo resolves to no org)\n" {
		t.Errorf("absent %q %v", got, err)
	}
	p.check = CheckOut{Path: "/c.yaml", Exists: true, Orgs: []string{"acme"}, Warnings: []string{"unknown key x (line 3)"}}
	got, _ = run(t, p, OpCheck)
	if got != "✓ /c.yaml is valid: 1 org entry (acme)\n  warning: unknown key x (line 3)\n" {
		t.Errorf("one org %q", got)
	}
	p.check = CheckOut{Path: "/c.yaml", Exists: true, Orgs: []string{"a", "b"}}
	got, _ = run(t, p, OpCheck)
	if got != "✓ /c.yaml is valid: 2 org entries (a, b)\n" {
		t.Errorf("two orgs %q", got)
	}
}

// TestCheckJSONHasNoNulls: --json is the plain op object (owner question
// on record), and its lists are [] rather than null when empty.
func TestCheckJSONHasNoNulls(t *testing.T) {
	t.Parallel()
	p := newPort("")
	p.check = CheckOut{Path: "/c.yaml"}
	e := Ops(p)[3]
	in, err := e.Decode(op.ArgsDecoder{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Invoke(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := res.JSON()
	if err != nil || string(b) != `{"path":"/c.yaml","exists":false,"orgs":[],"warnings":[]}` {
		t.Errorf("json %s %v", b, err)
	}
}
