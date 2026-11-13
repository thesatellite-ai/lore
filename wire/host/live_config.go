package host

// live_config.go — the `live:`, `lease.push_retries` and `default_branch`
// keys of ~/.lore/config.yaml that live rows read (LORE_TEAM_SPEC §1
// "Config"; v0.1.17 lorecfg.LiveFor): the top-level blocks, then the
// repo's org entry's `live:` block per key (liveSettings.override,
// P3-MODEB-2).

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// Built-in live defaults (v0.1.17 lorecfg: TEAM-025, TEAM-026, F8).
// ds:def id=team-config-defaults-amp24fsn owner=@khanakia stability=stable desc="built-in live defaults of ~/.lore/config.yaml"
const (
	defaultLiveFetchTTL     = 60 * time.Second
	defaultLiveMaxAge       = 7 * 24 * time.Hour // TEAM-026: an open row is copied to main after a week
	defaultLiveAutopush     = false              // owner rule: nothing pushes without the person saying so (lore sync, git push)
	defaultLiveCompactAfter = 1000
	defaultLiveRemote       = "origin"
	defaultPushRetries      = 5
)

// defaultLiveKinds are the tables whose new rows go live (v0.1.17
// lorecfg.DefaultLiveKinds); false entries are kinds config may turn on.
// ds:def id=team-live-kinds-88j26q7s owner=@khanakia stability=stable desc="kinds that are live by default"
func defaultLiveKinds() map[string]bool {
	return map[string]bool{
		"tasks": true, "missions": true, "handoffs": true, "comments": true,
		"rules": false, "decisions": false, "memories": false,
	}
}

// liveSettings is the effective live configuration of one command.
type liveSettings struct {
	Defaults      map[string]bool
	FetchTTL      time.Duration
	MaxAge        time.Duration
	Autopush      bool
	CompactAfter  int
	Remote        string
	PushRetries   int
	DefaultBranch string
}

// liveFile is the part of config.yaml read here.
type liveFile struct {
	DefaultBranch string `yaml:"default_branch"`
	Live          struct {
		Defaults     map[string]bool   `yaml:"defaults"`
		FetchTTL     *orgconf.Duration `yaml:"fetch_ttl"`
		MaxAge       *orgconf.Duration `yaml:"max_age"`
		Autopush     *bool             `yaml:"autopush"`
		CompactAfter *int              `yaml:"compact_after"`
		Remote       string            `yaml:"remote"`
	} `yaml:"live"`
	Lease struct {
		PushRetries *int `yaml:"push_retries"`
	} `yaml:"lease"`
}

// loadLiveSettings merges the built-in defaults with home's config.yaml.
// An absent or unreadable file gives the defaults: a broken config is
// reported by `lore config check`, and live rows keep working meanwhile
// (v0.1.17 liveConfig).
func loadLiveSettings(home string) liveSettings {
	s := liveSettings{
		Defaults: defaultLiveKinds(), FetchTTL: defaultLiveFetchTTL, MaxAge: defaultLiveMaxAge, Autopush: defaultLiveAutopush,
		CompactAfter: defaultLiveCompactAfter, Remote: defaultLiveRemote, PushRetries: defaultPushRetries,
	}
	b, err := os.ReadFile(filepath.Clean(orgcfg.ConfigPath(home)))
	if err != nil {
		return s // absent (fs.ErrNotExist) or unreadable: the defaults
	}
	var f liveFile
	if yaml.Unmarshal(b, &f) != nil {
		return s
	}
	for k, v := range f.Live.Defaults {
		s.Defaults[k] = v
	}
	if f.Live.FetchTTL != nil {
		s.FetchTTL = f.Live.FetchTTL.D()
	}
	if f.Live.MaxAge != nil {
		s.MaxAge = f.Live.MaxAge.D()
	}
	if f.Live.Autopush != nil {
		s.Autopush = *f.Live.Autopush
	}
	if f.Live.CompactAfter != nil {
		s.CompactAfter = *f.Live.CompactAfter
	}
	if f.Live.Remote != "" {
		s.Remote = f.Live.Remote
	}
	if f.Lease.PushRetries != nil && *f.Lease.PushRetries >= 0 {
		s.PushRetries = *f.Lease.PushRetries
	}
	s.DefaultBranch = f.DefaultBranch
	return s
}

// override applies an org entry's live: block over s, key by key (a key
// the entry leaves unset keeps the top level's value; defaults merge per
// kind), as v0.1.17 lorecfg.LiveFor(org) did. nil changes nothing.
func (s *liveSettings) override(l *orgconf.Live) {
	if l == nil {
		return
	}
	for k, v := range l.Defaults {
		s.Defaults[k] = v
	}
	if l.FetchTTL != nil {
		s.FetchTTL = l.FetchTTL.D()
	}
	if l.MaxAge != nil {
		s.MaxAge = l.MaxAge.D()
	}
	if l.Autopush != nil {
		s.Autopush = *l.Autopush
	}
	if l.CompactAfter != nil {
		s.CompactAfter = *l.CompactAfter
	}
	if l.Remote != "" {
		s.Remote = l.Remote
	}
}
