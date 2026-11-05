package wire

// migrate_personal_test.go — later 59 (FA-FMT-1): migratePersonalSettings,
// step 12's personal half (v0.1.17 lore.db `config` rows and the
// project_configs keys the team half left) into ~/.lore/config.yaml, and
// rollbackConfigKeys, its undo.

import (
	"os"
	"slices"
	"strings"
	"testing"
)

const (
	fromDB  = v017LoreDB
	fromPCs = "project_configs"
)

func TestMigratePersonalSettings(t *testing.T) {
	ps := func(from, k, v string) personalSetting { return personalSetting{Key: k, Value: v, From: from} }
	cases := []struct {
		name    string
		config  string // config.yaml before ("" = no file)
		cands   []personalSetting
		lines   []string // each must appear in the step lines
		written map[string]string
		absent  []string // keys config.yaml must not hold afterwards
	}{
		{
			name:    "valid keys of every kind",
			cands:   []personalSetting{ps(fromDB, "live.autopush", "true"), ps(fromPCs, "lease.ttl", "20m"), ps(fromDB, "agents.max_attempts", "3"), ps(fromDB, "merge_check.adapter", "github"), ps(fromDB, "live.remote", "upstream")},
			lines:   []string{"live.autopush = true", "lease.ttl = 20m", "agents.max_attempts = 3", "merge_check.adapter = github", "live.remote = upstream"},
			written: map[string]string{"live.autopush": "true", "lease.ttl": "20m", "agents.max_attempts": "3", "merge_check.adapter": "github", "live.remote": "upstream"},
		},
		{
			name:    "already set is kept",
			config:  "live:\n  autopush: false\n",
			cands:   []personalSetting{ps(fromDB, "live.autopush", "true")},
			lines:   []string{"(config.yaml already sets it; kept)"},
			written: map[string]string{"live.autopush": "false"},
		},
		{
			name:   "invalid values",
			cands:  []personalSetting{ps(fromDB, "live.compact_after", "0"), ps(fromDB, "live.autopush", "maybe"), ps(fromDB, "merge_check.adapter", "gitlab"), ps(fromDB, "live.remote", "a/b"), ps(fromDB, "lease.ttl", "soon")},
			lines:  []string{"key live.compact_after holds a value", "key live.autopush holds a value", "key merge_check.adapter holds a value", "key live.remote holds a value", "key lease.ttl holds a value"},
			absent: []string{"live.compact_after", "live.autopush", "merge_check.adapter", "live.remote", "lease.ttl"},
		},
		{
			name:   "unknown key",
			cands:  []personalSetting{ps(fromDB, "theme.color", "blue")},
			lines:  []string{fromDB + " key theme.color has no 0.1.18 home; left in place"},
			absent: []string{"theme.color"},
		},
		{
			name:    "disagreeing sources",
			cands:   []personalSetting{ps(fromDB, "lease.grace", "1m"), ps(fromPCs, "lease.grace", "2m"), ps(fromDB, "upgrade_notice", "false"), ps(fromPCs, "upgrade_notice", " false ")},
			lines:   []string{"key lease.grace has different values", "upgrade_notice = false"},
			written: map[string]string{"upgrade_notice": "false"},
			absent:  []string{"lease.grace"},
		},
		{
			name:   "lease renew_every not shorter than ttl",
			cands:  []personalSetting{ps(fromDB, "lease.ttl", "5m"), ps(fromDB, "lease.renew_every", "5m")},
			lines:  []string{"key lease.ttl would make lease.renew_every not shorter", "key lease.renew_every would make"},
			absent: []string{"lease.ttl", "lease.renew_every"},
		},
		{
			name:   "renew_every against a ttl config.yaml already holds",
			config: "lease:\n  ttl: 2m\n",
			cands:  []personalSetting{ps(fromDB, "lease.renew_every", "3m")},
			lines:  []string{"key lease.renew_every would make"},
			absent: []string{"lease.renew_every"},
		},
		{
			name:    "a valid lease pair",
			cands:   []personalSetting{ps(fromDB, "lease.ttl", "10m"), ps(fromDB, "lease.renew_every", "2m")},
			lines:   []string{"lease.ttl = 10m", "lease.renew_every = 2m"},
			written: map[string]string{"lease.ttl": "10m", "lease.renew_every": "2m"},
		},
	}
	for _, c := range cases {
		for _, dry := range []bool{true, false} {
			t.Run(c.name+map[bool]string{true: " (dry run)", false: ""}[dry], func(t *testing.T) {
				a := newTestApp(t, gitRepo(t))
				if c.config != "" {
					if err := os.WriteFile(configPath(a.opts.Home), []byte(c.config), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				lines, err := a.migratePersonalSettings(background(), c.cands, dry)
				if err != nil {
					t.Fatal(err)
				}
				all := strings.Join(lines, "\n")
				for _, w := range c.lines {
					if !strings.Contains(all, w) {
						t.Fatalf("lines lack %q:\n%s", w, all)
					}
				}
				root, err := readConfigRoot(a.opts.Home)
				if err != nil {
					t.Fatal(err)
				}
				for k, v := range c.written {
					got, ok := configLookup(root, k)
					if dry && !strings.Contains(c.config, strings.Split(k, ".")[0]) {
						if ok {
							t.Fatalf("a dry run wrote %s", k)
						}
						continue
					}
					if !ok || got != v {
						t.Fatalf("config.yaml %s = %q (%v), want %q", k, got, ok, v)
					}
				}
				for _, k := range c.absent {
					if _, ok := configLookup(root, k); ok && !strings.Contains(c.config, strings.Split(k, ".")[1]) {
						t.Fatalf("config.yaml holds %s", k)
					}
				}
			})
		}
	}
	if lines, err := newTestApp(t, gitRepo(t)).migratePersonalSettings(background(), nil, false); err != nil || lines != nil {
		t.Fatalf("no candidates: %v %v", lines, err)
	}
}

// TestRollbackPersonalConfigKeys: rollback removes the keys that still hold
// the value the run wrote (and the parent mapping it leaves empty), keeps
// and lists a key changed since, and ignores a key already gone.
func TestRollbackPersonalConfigKeys(t *testing.T) {
	a := newTestApp(t, gitRepo(t))
	cfg := "live:\n  autopush: true\nlease:\n  ttl: 30m\n  grace: 1m\n# mine\nother: kept\n"
	if err := os.WriteFile(configPath(a.opts.Home), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	line, kept, err := a.rollbackConfigKeys([]journalConfigKey{
		{Key: "live.autopush", Value: "true"}, // unchanged: removed, live: goes too
		{Key: "lease.ttl", Value: "20m"},      // changed after the run: kept
		{Key: "lease.grace", Value: "1m"},     // unchanged: removed, lease: stays (ttl)
		{Key: "agents.max_attempts", Value: "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "2 personal settings removed (live.autopush, lease.grace)") {
		t.Fatalf("line = %q", line)
	}
	if !slices.Equal(kept, []string{"~/.lore/" + ConfigFile + " lease.ttl (changed after the migration)"}) {
		t.Fatalf("kept = %v", kept)
	}
	got := string(mustRead(t, configPath(a.opts.Home)))
	for _, w := range []string{"# mine", "ttl: 30m", "other: kept"} {
		if !strings.Contains(got, w) {
			t.Fatalf("config.yaml lost %q:\n%s", w, got)
		}
	}
	for _, w := range []string{"autopush", "grace", "live:"} {
		if strings.Contains(got, w) {
			t.Fatalf("config.yaml still holds %q:\n%s", w, got)
		}
	}
	if line, kept, err = a.rollbackConfigKeys(nil); err != nil || line != "" || kept != nil {
		t.Fatalf("no keys: %q %v %v", line, kept, err)
	}
}
