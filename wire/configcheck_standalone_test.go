package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestStandaloneConfigCheck pins cmd/lore's `lore config check` schema
// (P3-CFGCHECK): absent and valid files pass with their orgs and warnings;
// every broken rule is E_CONFIG_INVALID naming the file and the rule.
func TestStandaloneConfigCheck(t *testing.T) {
	cases := []struct {
		name     string
		body     string // "" = no file
		wantErr  []string
		wantWarn []string
		wantOrgs []string
	}{
		{name: "absent"},
		{name: "empty file", body: "\n"},
		{name: "valid with orgs", body: "orgs:\n  - name: acme\n  - name: beta\n", wantOrgs: []string{"acme", "beta"}},
		{name: "unknown key is a warning", body: "org_foldr: x\n", wantWarn: []string{"unknown key, ignored", "org_foldr"}},
		{name: "live kind not a table", body: "live:\n  defaults:\n    taskz: true\n    tasks: true\n", wantWarn: []string{"live.defaults.taskz: not a synced table"}},
		{name: "policies and store are known keys", body: "store:\n  lock_timeout: 30s\npolicies: {}\n"},
		{name: "syntax error", body: "a: [\n", wantErr: []string{"not valid YAML"}},
		{name: "bad duration", body: "lease:\n  ttl: banana\n", wantErr: []string{"banana"}},
		{name: "built-in profile name", body: "context:\n  profiles:\n    - name: claude_code\n      pointer_file: X.md\n      import: at\n", wantErr: []string{"context.profiles", "already taken"}},
		{name: "lock timeout too long", body: "store:\n  lock_timeout: 10h\n", wantErr: []string{"store.lock_timeout 10h"}},
		{name: "every problem at once", body: "lease:\n  ttl: banana\nstore:\n  lock_timeout: 10h\n", wantErr: []string{"banana", "store.lock_timeout 10h"}},
		{name: "duplicate org", body: "orgs:\n  - name: acme\n  - name: acme\n", wantErr: []string{"already used"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, ConfigFile)
			if tc.body != "" {
				if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			out, err := standaloneConfig{home: home}.CheckConfig(context.Background())
			if len(tc.wantErr) > 0 {
				var e *errs.Error
				if !errors.As(err, &e) || e.Code != errs.ConfigInvalid {
					t.Fatalf("err = %v, want E_CONFIG_INVALID", err)
				}
				for _, w := range append([]string{path}, tc.wantErr...) {
					if !strings.Contains(e.Message, w) {
						t.Errorf("message %q lacks %q", e.Message, w)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckConfig: %v", err)
			}
			if out.Path != path || out.Exists != (tc.body != "") {
				t.Fatalf("out = %+v", out)
			}
			if strings.Join(out.Orgs, ",") != strings.Join(tc.wantOrgs, ",") {
				t.Errorf("orgs = %v, want %v", out.Orgs, tc.wantOrgs)
			}
			warn := strings.Join(out.Warnings, "\n")
			for _, w := range tc.wantWarn {
				if !strings.Contains(warn, w) {
					t.Errorf("warnings %q lack %q", warn, w)
				}
			}
			if len(tc.wantWarn) == 0 && len(out.Warnings) > 0 {
				t.Errorf("unexpected warnings %v", out.Warnings)
			}
		})
	}
}

// TestStandaloneConfigValidate pins the verdict `lore config set` reads.
func TestStandaloneConfigValidate(t *testing.T) {
	ctx := context.Background()
	v, err := standaloneConfig{}.ValidateConfig(ctx, []byte("live:\n  autopush: false\n"))
	if err != nil || v.Decode != nil || v.Invalid != nil || len(v.Unknown) != 0 {
		t.Fatalf("valid file: %+v %v", v, err)
	}
	if v, _ = (standaloneConfig{}).ValidateConfig(ctx, []byte("nope: 1\n")); len(v.Unknown) != 1 {
		t.Fatalf("unknown key: %+v", v)
	}
	if v, _ = (standaloneConfig{}).ValidateConfig(ctx, []byte("live:\n  autopush: [1]\n")); v.Decode == nil {
		t.Fatalf("wrong type: %+v", v)
	}
	if v, _ = (standaloneConfig{}).ValidateConfig(ctx, []byte("orgs:\n  - name: a\n  - name: a\n")); v.Invalid == nil {
		t.Fatalf("broken rule: %+v", v)
	}
}

// TestStandaloneOptionsCarryConfigSchema: cmd/lore's options supply the
// schema, so config check never answers E_UNSUPPORTED there.
func TestStandaloneOptionsCarryConfigSchema(t *testing.T) {
	o := StandaloneOptions(t.TempDir(), t.TempDir(), "", "test")
	if o.Local.Config == nil {
		t.Fatal("StandaloneOptions leaves Local.Config nil: config check is E_UNSUPPORTED on cmd/lore")
	}
}
