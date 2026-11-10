package orgconf

import (
	"strings"
	"testing"
)

// TestParseRefusesBrokenSettings pins P3-CFG-5: a config.yaml whose
// non-org settings are broken fails Parse (so every core command fails
// closed), at the top level and inside an org entry, naming the key.
func TestParseRefusesBrokenSettings(t *testing.T) {
	const org = "orgs:\n  - name: acme\n"
	cases := []struct {
		name, yaml, want string
	}{
		{"live max_age day mix", "live:\n  max_age: 1d12h\n", `invalid duration "1d12h"`},
		{"live max_age zero", "live:\n  max_age: 0s\n", "live: max_age: must be greater than zero"},
		{"live compact_after", "live:\n  compact_after: 0\n", "live: compact_after: must be at least 1"},
		{"live remote", "live:\n  remote: a/b\n", `live: remote "a/b"`},
		{"live defaults kind", "live:\n  defaults:\n    Tasks: true\n", "live: defaults.Tasks"},
		{"live autopush type", "live:\n  autopush: maybe\n", "line 2: live.autopush: expected true or false, got text"},
		{"lease renew vs ttl", "lease:\n  ttl: 1m\n  renew_every: 2m\n", "lease: renew_every: must be shorter than ttl"},
		{"lease push_retries", "lease:\n  push_retries: -1\n", "lease: push_retries: may not be negative"},
		{"agents max_attempts", "agents:\n  max_attempts: 0\n", "agents: max_attempts: must be at least 1"},
		{"merge_check adapter", "merge_check:\n  adapter: gitlab\n", `merge_check: adapter "gitlab"`},
		{"default_branch", "default_branch: 'a..b'\n", `default_branch "a..b"`},
		{"link_index zero", "link_index:\n  max_age: 0s\n", "link_index.max_age"},
		{"untrusted_hosts empty", "untrusted_hosts: ['  ']\n", "untrusted_hosts[0]: empty pattern"},
		{"upgrade_notice type", "upgrade_notice: sometimes\n", "line 1: upgrade_notice: expected true or false, got text"},
		{"context profile", "context:\n  profiles:\n    - name: claude_code\n      pointer_file: X.md\n      import: at\n", "context.profiles"},
		{"org live", org + "    live:\n      max_age: 0s\n", `orgs[0] "acme": live: max_age`},
		{"org lease", org + "    lease:\n      ttl: 0s\n", `orgs[0] "acme": lease: ttl`},
		{"org agents", org + "    agents:\n      max_claims_per_agent: 0\n", `orgs[0] "acme": agents: max_claims_per_agent`},
		{"org merge_check", org + "    merge_check:\n      adapter: x\n", `orgs[0] "acme": merge_check: adapter`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("Parse accepted %q", tc.yaml)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error %q does not name %q", err, tc.want)
			}
		})
	}
}

// TestParseAcceptsValidSettings: every setting at a valid value (and
// policies:/store:, read elsewhere) parses.
func TestParseAcceptsValidSettings(t *testing.T) {
	const y = `default_branch: release/2026
live:
  defaults: {tasks: true}
  max_age: 7d
  fetch_ttl: 60
  autopush: false
  compact_after: 3
  remote: origin
lease: {ttl: 30m, renew_every: 5m, grace: 1m, push_retries: 0}
agents: {max_claims_per_agent: 2, max_attempts: 3}
merge_check: {adapter: github}
link_index: {max_age: 2h}
untrusted_hosts: ['*.example.com']
upgrade_notice: false
auto_upgrade: true
policies: {meta.max_bytes: 4096}
store: {lock_timeout: 30s}
orgs:
  - name: acme
    live: {max_age: 1d}
    lease: {ttl: 10m}
    agents: {max_attempts: 1}
    merge_check: {adapter: none}
`
	c, err := Parse([]byte(y))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.DefaultBranch != "release/2026" || c.MergeCheck.Adapter != MergeCheckGitHub || c.Orgs[0].Lease == nil {
		t.Fatalf("settings not decoded: %+v", c.Settings)
	}
}
