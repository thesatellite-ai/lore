package lorecfg

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func intp(n int) *int { return &n }

// Duration survives a YAML write and read, and the text form reads the
// same syntax as YAML.
func TestDurationYAMLAndTextRoundTrip(t *testing.T) {
	type wrap struct {
		D Duration `yaml:"d"`
	}
	for _, d := range []time.Duration{0, time.Second, 90 * time.Second, time.Hour + 30*time.Minute, Day, 7 * Day, 36 * time.Hour, 365 * Day} {
		b, err := yaml.Marshal(wrap{D: Duration(d)})
		if err != nil {
			t.Fatal(err)
		}
		var back wrap
		if err := yaml.Unmarshal(b, &back); err != nil {
			t.Fatalf("%v: read back %q: %v", d, b, err)
		}
		if back.D.D() != d {
			t.Errorf("%v → %q → %v", d, b, back.D.D())
		}
		var txt Duration
		if err := txt.UnmarshalText([]byte(Duration(d).String())); err != nil || txt.D() != d {
			t.Errorf("text %q → %v %v", Duration(d).String(), txt.D(), err)
		}
	}
	if b, _ := yaml.Marshal(wrap{D: Duration(7 * Day)}); !strings.Contains(string(b), "d: 7d") {
		t.Errorf("whole days write as Nd: %q", b)
	}
	cases := map[string]time.Duration{"7d": 7 * Day, "0d": 0, "60": time.Minute, " 60s ": time.Minute, "1h30m": 90 * time.Minute}
	for in, want := range cases {
		var d Duration
		if err := d.UnmarshalText([]byte(in)); err != nil || d.D() != want {
			t.Errorf("UnmarshalText(%q) = %v %v, want %v", in, d.D(), err, want)
		}
	}
	for _, bad := range []string{"", "  ", "-1", "-1d", "-5s", "1d12h", "1.5d", "seven", "d", "日"} {
		d := Duration(time.Hour)
		if err := d.UnmarshalText([]byte(bad)); err == nil {
			t.Errorf("UnmarshalText(%q) must fail, got %v", bad, d.D())
		}
		if d.D() != time.Hour {
			t.Errorf("a failed read must not change the value (%q → %v)", bad, d.D())
		}
	}
}

func TestOrgKeys(t *testing.T) {
	c := &Config{Orgs: []Org{
		{Name: "a", Identity: OrgIdentity{Key: "human:alice@agency.example"}},
		{Name: "b"},
		{Name: "c", Identity: OrgIdentity{Key: "   "}},
		{Name: "d", Identity: OrgIdentity{Key: "  human:carol@client.example\t"}},
	}}
	got := c.OrgKeys()
	if strings.Join(got, ",") != "human:alice@agency.example,human:carol@client.example" {
		t.Fatalf("OrgKeys = %q", got)
	}
	if (&Config{}).OrgKeys() != nil {
		t.Fatal("no orgs, no keys")
	}
}

func TestAgentsForLayers(t *testing.T) {
	cases := []struct {
		name string
		top  Agents
		org  *Org
		want EffectiveAgents
	}{
		{"defaults", Agents{}, nil, EffectiveAgents{DefaultMaxClaimsPerAgent, DefaultMaxAttempts}},
		{"top level", Agents{MaxClaimsPerAgent: intp(3)}, nil, EffectiveAgents{3, DefaultMaxAttempts}},
		{"entry beats top", Agents{MaxClaimsPerAgent: intp(3), MaxAttempts: intp(5)}, &Org{Agents: &Agents{MaxAttempts: intp(1)}}, EffectiveAgents{3, 1}},
		{"entry without agents block", Agents{MaxAttempts: intp(9)}, &Org{}, EffectiveAgents{DefaultMaxClaimsPerAgent, 9}},
		{"explicit zero is kept", Agents{}, &Org{Agents: &Agents{MaxClaimsPerAgent: intp(0)}}, EffectiveAgents{0, DefaultMaxAttempts}},
	}
	for _, tc := range cases {
		c := &Config{Agents: tc.top}
		if got := c.AgentsFor(tc.org); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestMergeCheckForLayers(t *testing.T) {
	cases := []struct {
		name string
		top  MergeCheckAdapter
		org  *Org
		want MergeCheckAdapter
	}{
		{"default", "", nil, DefaultMergeCheck},
		{"top", MergeCheckGitHub, nil, MergeCheckGitHub},
		{"entry beats top", MergeCheckGitHub, &Org{MergeCheck: &MergeCheck{Adapter: MergeCheckNone}}, MergeCheckNone},
		{"empty entry adapter falls through", MergeCheckGitHub, &Org{MergeCheck: &MergeCheck{}}, MergeCheckGitHub},
		{"entry only", "", &Org{MergeCheck: &MergeCheck{Adapter: MergeCheckGitHub}}, MergeCheckGitHub},
	}
	for _, tc := range cases {
		c := &Config{MergeCheck: MergeCheck{Adapter: tc.top}}
		if got := c.MergeCheckFor(tc.org); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestLinkIndexMaxAge(t *testing.T) {
	if (&Config{}).LinkIndexMaxAge() != DefaultLinkIndexMaxAge {
		t.Fatal("default")
	}
	d := Duration(2 * Day)
	if (&Config{LinkIndex: LinkIndex{MaxAge: &d}}).LinkIndexMaxAge() != 2*Day {
		t.Fatal("configured")
	}
	z := Duration(0)
	if (&Config{LinkIndex: LinkIndex{MaxAge: &z}}).LinkIndexMaxAge() != 0 {
		t.Fatal("an explicit 0 is the configured value")
	}
	// Through the real loader.
	dir := writeTeam(t, "link_index:\n  max_age: 3d\n")
	cfg, _, err := LoadTeam(context.Background(), dir, Options{Notices: &bytes.Buffer{}})
	if err != nil || cfg.LinkIndexMaxAge() != 3*Day {
		t.Fatalf("loaded: %v %v", cfg, err)
	}
}

// Remove deletes the YAML and any legacy TOML so an unset cannot come back
// through a later migration; backups stay; a second Remove reports false.
func TestRemove(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := Remove(dir, "identity"); err != nil || ok {
		t.Fatalf("nothing to remove: %v %v", ok, err)
	}
	write("identity.yaml")
	write("identity.toml")
	write("identity.toml" + MigratedSuffix)
	write("other.yaml")
	if ok, err := Remove(dir, "identity"); err != nil || !ok {
		t.Fatalf("remove: %v %v", ok, err)
	}
	for name, want := range map[string]bool{"identity.yaml": false, "identity.toml": false, "identity.toml" + MigratedSuffix: true, "other.yaml": true} {
		_, err := os.Stat(filepath.Join(dir, name))
		if (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", name, err == nil, want)
		}
	}
	if ok, _ := Remove(dir, "identity"); ok {
		t.Fatal("a second Remove removes nothing")
	}
	write("only.toml")
	if ok, err := Remove(dir, "only"); err != nil || !ok {
		t.Fatalf("a lone TOML is removed too: %v %v", ok, err)
	}
	if loc, _ := Locate(context.Background(), dir, "only", Options{Notices: &bytes.Buffer{}}); loc.Exists {
		t.Fatal("after Remove nothing is found")
	}
}

// Migration keeps every key (unknown ones too), TOML local dates as their
// TOML text, the file mode, and never overwrites an earlier backup.
func TestMigrationKeepsValuesAndBackups(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	toml := "display_name = \"Alice 日本\"\nfuture_key = 42\nborn = 2026-11-02\nat = 10:30:00\nwhen = 2026-11-02T10:30:00\ntags = [\"a\", \"b\"]\n[nested]\nlist = [1979-05-27, 1980-01-01]\n[[items]]\nname = \"x\"\nday = 2026-01-01\n"
	tomlPath := filepath.Join(dir, "cfg.toml")
	if err := os.WriteFile(tomlPath, []byte(toml), 0o640); err != nil {
		t.Fatal(err)
	}
	var notes bytes.Buffer
	loc, err := Locate(ctx, dir, "cfg", Options{Notices: &notes})
	if err != nil || loc.Format != FormatYAML || !loc.Exists {
		t.Fatalf("Locate: %+v %v (%s)", loc, err, notes.String())
	}
	info, err := os.Stat(loc.Path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode kept: %v %v", info, err)
	}
	b, _ := os.ReadFile(loc.Path)
	var got map[string]any
	if err := yaml.Unmarshal(b, &got); err != nil {
		t.Fatalf("migrated YAML parses: %v\n%s", err, b)
	}
	checks := map[string]any{"display_name": "Alice 日本", "future_key": 42, "born": "2026-11-02", "at": "10:30:00", "when": "2026-11-02T10:30:00"}
	for k, want := range checks {
		if got[k] != want {
			t.Errorf("%s = %#v, want %#v\n%s", k, got[k], want, b)
		}
	}
	nested, _ := got["nested"].(map[string]any)
	if l, _ := nested["list"].([]any); len(l) != 2 || l[0] != "1979-05-27" {
		t.Errorf("dates inside arrays of tables: %#v", nested)
	}
	items, _ := got["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["day"] != "2026-01-01" {
		t.Errorf("array of tables: %#v", got["items"])
	}
	if _, err := os.Stat(tomlPath + MigratedSuffix); err != nil {
		t.Fatalf("backup kept: %v", err)
	}

	// A TOML re-created later migrates again into a NEW backup; the first
	// backup is never overwritten.
	if err := os.Remove(loc.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tomlPath, []byte("display_name = \"Bob\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Locate(ctx, dir, "cfg", Options{Notices: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(tomlPath + MigratedSuffix)
	if !strings.Contains(string(first), "Alice") {
		t.Fatalf("first backup overwritten: %q", first)
	}
	matches, _ := filepath.Glob(tomlPath + MigratedSuffix + ".*")
	if len(matches) != 1 {
		t.Fatalf("one stamped second backup: %v", matches)
	}
	second, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(second), "Bob") {
		t.Fatalf("second backup holds the second TOML: %q", second)
	}
}
