package config

import (
	"strings"
	"testing"
)

// TestSetUnderEmptyKey: lore migrate run leaves `identity:` and
// `personal:` with no value (a real migrated config.yaml, 2026-11-07);
// setting a key below one fills it in as a mapping instead of refusing
// "does not exist under a list or a value". A real scalar in the way is
// still refused (TestSetRefusesAndWritesNothing). The one-line setups list
// comes back block style (logic/yamlfmt.Block).
func TestSetUnderEmptyKey(t *testing.T) {
	t.Parallel()
	const migrated = "setups: [{name: main, kind: solo}]\nidentity:\npersonal: ~\nlive: null\n"
	cases := []struct{ key, val, want string }{
		{"identity.key", "human:pat@example.test", "identity:\n  key: human:pat@example.test\n"},
		{"personal.store", "file:///tmp/me", "personal:\n  store: file:///tmp/me\n"},
		{"live.ttl", "30m", "live:\n  ttl: 30m\n"},
		{"identity.name", "Pat Example", "identity:\n  name: Pat Example\n"},
	}
	for _, c := range cases {
		p := newPort(migrated)
		if _, err := run(t, p, OpSet, c.key, c.val); err != nil {
			t.Errorf("%s: %v", c.key, err)
			continue
		}
		if !strings.Contains(string(p.file.Raw), c.want) || !strings.Contains(string(p.file.Raw), "setups:\n  - name: main\n    kind: solo\n") {
			t.Errorf("%s: file\n%s\nlacks\n%s", c.key, p.file.Raw, c.want)
		}
		if back, err := run(t, p, OpGet, c.key); err != nil || back != c.val+"\n" {
			t.Errorf("%s: get back %q %v", c.key, back, err)
		}
	}
}
