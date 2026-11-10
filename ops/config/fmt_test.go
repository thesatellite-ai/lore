package config

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// flowSample is a config.yaml saved before the block-style writer: the
// setups list on one line between commented, ordered keys.
const flowSample = `# machine config
identity:
  key: human:sam@example.test # who I am
setups: [{name: personal, kind: solo, identity: {key: 'human:sam@example.test'}, tools: [claude]}]
# timing
lease:
  ttl: 30m
tools: [claude, codex]
`

// TestFmtRewritesFlowToBlock: config fmt spreads the one-line setups list
// over lines, keeps every comment and the key order, leaves a flat scalar
// list as written, names the key, and a second run writes nothing.
func TestFmtRewritesFlowToBlock(t *testing.T) {
	t.Parallel()
	p := newPort(flowSample)
	got, err := run(t, p, OpFmt)
	if err != nil {
		t.Fatal(err)
	}
	if got != "✓ rewrote /home/u/.lore/config.yaml in block style (setups); comments and key order kept\n" {
		t.Fatalf("line %q", got)
	}
	if p.writes != 1 || p.file.Mode != 0o600 {
		t.Fatalf("writes %d mode %o", p.writes, p.file.Mode)
	}
	want := `# machine config
identity:
  key: human:sam@example.test # who I am
setups:
  - name: personal
    kind: solo
    identity:
      key: 'human:sam@example.test'
    tools:
      - claude
# timing
lease:
  ttl: 30m
tools: [claude, codex]
`
	if string(p.file.Raw) != want {
		t.Fatalf("file:\n%s\nwant:\n%s", p.file.Raw, want)
	}
	// Idempotent: nothing left to rewrite, nothing written.
	p.file.Read = p.file.Raw
	got, err = run(t, p, OpFmt)
	if err != nil || p.writes != 1 || !strings.Contains(got, "already in block style; nothing written") {
		t.Fatalf("second run: %q %v writes %d", got, err, p.writes)
	}
}

// TestFmtWritesNothing: a block file, an absent file, --dry-run and a
// schema that refuses the rewrite all leave the file as it was.
func TestFmtWritesNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, raw string
		args      []string
		verdict   func(raw []byte) Verdict
		wantLine  string
		wantCode  errs.Code
	}{
		{name: "block file", raw: sample, wantLine: "is already in block style; nothing written"},
		{name: "absent file", raw: "", wantLine: "is already in block style; nothing written"},
		{name: "flat scalar flow lists only", raw: "tools: [claude, codex]\nidentity: {key: k}\n", wantLine: "is already in block style"},
		{name: "dry run", raw: flowSample, args: []string{"dry-run"}, wantLine: "would rewrite /home/u/.lore/config.yaml in block style (setups on one line); nothing written (--dry-run)"},
		{name: "the rewrite does not validate", raw: flowSample, wantCode: errs.ConfigInvalid, verdict: func(raw []byte) Verdict {
			if strings.Contains(string(raw), "setups: [") {
				return Verdict{}
			}
			return Verdict{Invalid: errors.New("placeholder rule")}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := newPort(c.raw)
			p.verdict = c.verdict
			got, err := runFlags(t, p, OpFmt, c.args)
			if c.wantCode != "" {
				if !errs.Has(err, c.wantCode) {
					t.Fatalf("err %v, want %s", err, c.wantCode)
				}
			} else if err != nil || !strings.Contains(got, c.wantLine) {
				t.Fatalf("got %q %v, want %q", got, err, c.wantLine)
			}
			if p.writes != 0 || string(p.file.Raw) != c.raw {
				t.Fatalf("written: %d\n%s", p.writes, p.file.Raw)
			}
		})
	}
}

// TestReadsNeverReformat: get, list, check and FlowKeys read a flow-style
// file and never write it (only config fmt, a set or an unset does).
func TestReadsNeverReformat(t *testing.T) {
	t.Parallel()
	p := newPort(flowSample)
	for _, c := range [][]string{{OpGet, "lease.ttl"}, {OpList}, {OpCheck}} {
		if _, err := run(t, p, c[0], c[1:]...); err != nil {
			t.Fatalf("%s: %v", c[0], err)
		}
	}
	keys, err := FlowKeys(p.file)
	if err != nil || len(keys) != 1 || keys[0] != "setups" {
		t.Fatalf("FlowKeys = %q %v", keys, err)
	}
	if p.writes != 0 || string(p.file.Raw) != flowSample {
		t.Fatalf("a read wrote the file (%d writes)", p.writes)
	}
}

// runFlags runs op name with each named bool flag set.
func runFlags(t *testing.T, p Port, name string, flags []string) (string, error) {
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
	fl := map[string][]string{}
	for _, f := range flags {
		fl[f] = []string{"true"}
	}
	in, err := e.Decode(op.ArgsDecoder{Flags: fl})
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
