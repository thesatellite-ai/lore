package config

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// TestUnset covers config.unset over the input space: a scalar, a nested
// key whose parent empties, a whole mapping, a list item, an absent key,
// a key under a scalar; comments and order of the rest kept.
func TestUnset(t *testing.T) {
	t.Parallel()
	const withPersonal = "# header\npersonal:\n  store: file:///tmp/me # mine\nlive:\n  autopush: true # push\n  enabled: true\n"
	cases := []struct {
		name, raw, key string
		want           []string // substrings the file must keep
		gone           []string // substrings the file must lose
		code           errs.Code
	}{
		{name: "scalar keeps siblings and comments", raw: sample, key: "live.autopush",
			want: []string{"# machine config", "enabled: true", "ttl: 30m"}, gone: []string{"autopush", "# push after each write"}},
		{name: "nested key empties its parent", raw: withPersonal, key: "personal.store",
			want: []string{"# header", "autopush: true # push"}, gone: []string{"personal", "file:///tmp/me"}},
		{name: "first key keeps the file header", raw: withPersonal, key: "personal",
			want: []string{"# header", "live:"}, gone: []string{"personal"}},
		{name: "whole mapping", raw: sample, key: "lease", want: []string{"live:", "orgs:"}, gone: []string{"lease", "ttl"}},
		{name: "list item", raw: sample, key: "orgs.1", want: []string{"acme"}, gone: []string{"beta"}},
		{name: "last list item drops the list", raw: "tools:\n  - claude\nlive:\n  enabled: true\n", key: "tools.0",
			want: []string{"live:"}, gone: []string{"tools", "[]"}},
		{name: "absent key", raw: sample, key: "personal.store", code: errs.NotFound},
		{name: "absent file", raw: "", key: "identity", code: errs.NotFound},
		{name: "under a scalar", raw: sample, key: "lease.ttl.x", code: errs.NotFound},
		{name: "not a dotted path", raw: sample, key: "live..autopush", code: errs.InvalidInput},
		{name: "inside policies", raw: sample, key: "policies.secrets.on_detect", code: errs.InvalidInput},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := newPort(c.raw)
			_, err := run(t, p, OpUnset, c.key)
			if c.code != "" {
				if errs.CodeOf(err) != c.code {
					t.Fatalf("err = %v, want %s", err, c.code)
				}
				if p.writes != 0 {
					t.Fatalf("refused unset wrote the file")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := string(p.file.Raw)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("file lost %q:\n%s", w, got)
				}
			}
			for _, g := range c.gone {
				if strings.Contains(got, g) {
					t.Errorf("file still holds %q:\n%s", g, got)
				}
			}
		})
	}
}

// TestUnsetDryRunAndTwice: --dry-run names the key and writes nothing; a
// second unset of the same key is E_NOT_FOUND and writes nothing.
func TestUnsetDryRunAndTwice(t *testing.T) {
	t.Parallel()
	p := newPort(sample)
	txt, err := runDry(t, p, "lease.ttl")
	if err != nil {
		t.Fatal(err)
	}
	if p.writes != 0 || !strings.Contains(txt, "would remove lease.ttl (30m)") {
		t.Fatalf("dry run wrote %d times, said %q", p.writes, txt)
	}
	if _, err := run(t, p, OpUnset, "lease.ttl"); err != nil {
		t.Fatal(err)
	}
	if p.writes != 1 {
		t.Fatalf("writes = %d, want 1", p.writes)
	}
	if _, err := run(t, p, OpUnset, "lease.ttl"); errs.CodeOf(err) != errs.NotFound {
		t.Fatalf("second unset: %v, want E_NOT_FOUND", err)
	}
	if p.writes != 1 {
		t.Fatalf("second unset wrote")
	}
}

// TestUnsetRefusesBreakingAValidFile: a removal the schema rejects is
// refused when the file was valid, and allowed when the file was already
// invalid (the removal may be the fix).
func TestUnsetRefusesBreakingAValidFile(t *testing.T) {
	t.Parallel()
	broken := errors.New("orgs[0]: name is required")
	// The schema rejects any file without "name: acme".
	verdict := func(raw []byte) Verdict {
		if strings.Contains(string(raw), "name: acme") {
			return Verdict{}
		}
		return Verdict{Invalid: broken}
	}
	p := newPort(sample)
	p.verdict = verdict
	if _, err := run(t, p, OpUnset, "orgs.0.name"); errs.CodeOf(err) != errs.ConfigInvalid {
		t.Fatalf("err = %v, want E_CONFIG_INVALID", err)
	}
	if p.writes != 0 {
		t.Fatalf("refused unset wrote")
	}
	// Already invalid: no acme anywhere, and the removal goes through.
	q := newPort("orgs:\n  - name: beta\nlive:\n  autopush: true\n")
	q.verdict = verdict
	if _, err := run(t, q, OpUnset, "live.autopush"); err != nil {
		t.Fatalf("unset on an invalid file: %v", err)
	}
}

// runDry runs config.unset key --dry-run.
func runDry(t *testing.T, p Port, key string) (string, error) {
	t.Helper()
	var e op.Entry
	for _, c := range Ops(p) {
		if c.Spec().Name == OpUnset {
			e = c
		}
	}
	in, err := e.Decode(op.ArgsDecoder{Args: []string{key}, Flags: map[string][]string{"dry-run": {"true"}}})
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
