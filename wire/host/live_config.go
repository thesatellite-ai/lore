package host

// live_config.go — the `live:`, `lease.push_retries` and `default_branch`
// keys of ~/.lore/config.yaml that live rows read (LORE_TEAM_SPEC §1
// "Config"; v0.1.17 lorecfg.LiveFor). Only the top-level blocks are read:
// the per-org `live:` override is not wired for cmd/lore yet (P3-MODEB-2).

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// Built-in live defaults (v0.1.17 lorecfg: TEAM-025, TEAM-026, F8).
const (
	defaultLiveFetchTTL     = 60 * time.Second
	defaultLiveAutopush     = true
	defaultLiveCompactAfter = 1000
	defaultLiveRemote       = "origin"
	defaultPushRetries      = 5
)

// defaultLiveKinds are the tables whose new rows go live (v0.1.17
// lorecfg.DefaultLiveKinds); false entries are kinds config may turn on.
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
		Defaults: defaultLiveKinds(), FetchTTL: defaultLiveFetchTTL, Autopush: defaultLiveAutopush,
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
