package host

import (
	"reflect"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestLiveSettingsOrgOverride (P3-MODEB-2): the repo's org entry `live:`
// block wins over the top level key by key, as v0.1.17 lorecfg.LiveFor:
// an unset key keeps the top level's value, defaults merge per kind
// (a kind the entry names wins, the others stay), and no block (nil, or
// an empty one) changes nothing.
func TestLiveSettingsOrgOverride(t *testing.T) {
	base := func() liveSettings {
		return liveSettings{Defaults: map[string]bool{"tasks": true, "memories": false}, FetchTTL: time.Minute,
			MaxAge: time.Hour, Autopush: true, CompactAfter: 100, Remote: "origin", PushRetries: 3, DefaultBranch: "main"}
	}
	cases := []struct {
		name  string
		block string // "" = nil block
		edit  func(*liveSettings)
	}{
		{name: "no block"},
		{name: "empty block", block: "{}"},
		{name: "one kind flips, the other stays", block: "defaults: {memories: true}", edit: func(s *liveSettings) { s.Defaults["memories"] = true }},
		{name: "kind switched off", block: "defaults: {tasks: false}", edit: func(s *liveSettings) { s.Defaults["tasks"] = false }},
		{name: "new kind", block: "defaults: {decisions: true}", edit: func(s *liveSettings) { s.Defaults["decisions"] = true }},
		{name: "fetch_ttl", block: "fetch_ttl: 5m", edit: func(s *liveSettings) { s.FetchTTL = 5 * time.Minute }},
		{name: "max_age", block: "max_age: 48h", edit: func(s *liveSettings) { s.MaxAge = 48 * time.Hour }},
		{name: "autopush off", block: "autopush: false", edit: func(s *liveSettings) { s.Autopush = false }},
		{name: "compact_after", block: "compact_after: 7", edit: func(s *liveSettings) { s.CompactAfter = 7 }},
		{name: "remote", block: "remote: upstream", edit: func(s *liveSettings) { s.Remote = "upstream" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, want := base(), base()
			var l *orgconf.Live
			if c.block != "" {
				l = &orgconf.Live{}
				if err := yaml.Unmarshal([]byte(c.block), l); err != nil {
					t.Fatal(err)
				}
			}
			got.override(l)
			if c.edit != nil {
				c.edit(&want)
			}
			if len(got.Defaults) != len(want.Defaults) {
				t.Fatalf("defaults %v, want %v", got.Defaults, want.Defaults)
			}
			for k, v := range want.Defaults {
				if got.Defaults[k] != v {
					t.Fatalf("defaults %v, want %v", got.Defaults, want.Defaults)
				}
			}
			got.Defaults, want.Defaults = nil, nil
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}
