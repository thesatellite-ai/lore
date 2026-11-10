package onboard

// lifecycle.go — a setup's life after it is made (owner decision
// 2026-11-06, replaces the plain remove):
//
//	enabled ──disable──▶ disabled (kept in setups[], disabled: true, matches nothing)
//	        ◀──enable───
//	enabled|disabled ──remove──▶ trash.setups (removed_at, its repo pins kept)
//	trash ──restore──▶ setups[] (pins back)     trash ──purge──▶ gone for good
//
// Pure rules only: which setups take part in matching, which trash entry a
// name means, and what a purge leaves in place. wire edits config.yaml.

import (
	neturl "net/url"
	"path"
	"strings"
)

// NoteMaxLen caps a setup's note (one or two lines on Home).
const NoteMaxLen = 500

// TrashedSetup is one trash.setups entry: the setup as it was, when it was
// removed and the repo_setups pins it had (restored with it). In YAML the
// setup's own keys sit beside removed_at and repo_pins, so a trashed entry
// reads like the setups[] entry it was.
type TrashedSetup struct {
	Setup SetupEntry `yaml:",inline" json:"setup"`
	// RemovedAt is when lore setup remove moved it here, RFC 3339 UTC.
	RemovedAt string `yaml:"removed_at" json:"removed_at"`
	// RepoPins are the repo ids that were pinned to it (lore setup use --here).
	RepoPins []string `yaml:"repo_pins,omitempty" json:"repo_pins,omitempty"`
}

// Enabled is the setups that take part in matching (every setup that is
// not disabled), in config order.
func (c Config) Enabled() []SetupEntry {
	out := make([]SetupEntry, 0, len(c.Setups))
	for _, s := range c.Setups {
		if !s.Disabled {
			out = append(out, s)
		}
	}
	return out
}

// NewestTrashed is the index in trash of the most recently removed entry
// named name, -1 when none. Two entries may share a name (a setup removed,
// made again and removed again); a name always means the newest one.
func NewestTrashed(trash []TrashedSetup, name string) int {
	at := -1
	for i, t := range trash {
		if t.Setup.Name != name {
			continue
		}
		if at < 0 || t.RemovedAt >= trash[at].RemovedAt {
			at = i
		}
	}
	return at
}

// PurgeKeeps lists what a permanent delete of t leaves in place, one line
// each, for the confirm's "what stays" list: the machine identity, the
// personal store, the org, and the GitHub repos (lore never deletes a
// repo). identity and shared are the top-level identity name and
// personal.store of config.yaml ("" when unset).
func PurgeKeeps(t TrashedSetup, identity, shared string) []string {
	var out []string
	if identity != "" {
		out = append(out, "your lore identity ("+identity+") in ~/.lore/config.yaml")
	}
	if shared != "" {
		out = append(out, "the personal store "+shared+" in ~/.lore/config.yaml")
	}
	own := strings.TrimSpace(t.Setup.PersonalStore)
	if own != "" && own != PersonalStoreInherit && own != PersonalStoreNone && own != shared {
		if isLocalStoreFolder(own) {
			// A local folder is the person's one personal store `me`,
			// shared by all their setups (one `me` per person, decided
			// 2026-11-09): leaving a job never touches it; keeping a job's
			// notes apart is what that job's org stores are for (journey
			// joiner/month3/leave-job-a).
			out = append(out, "your personal store me ("+own+"): one per person, shared by all your setups; purging a setup never touches it")
		} else {
			out = append(out, "the store repo "+own+" (on GitHub or wherever it lives)")
		}
	}
	if t.Setup.Org != "" {
		out = append(out, "the org "+t.Setup.Org+"'s stores (on GitHub or wherever they live)")
	}
	return append(out, "every GitHub repo: lore never deletes a repository")
}

// OrgFreedByPurge reports whether purging trash entry at frees its org:
// it is a member or admin setup and no other setup, live or trashed, names
// that org, so the purge removes the orgs[] entry too and the team name is
// free again (owner decision 2026-11-08). wire.PurgeSetup and the purge
// preview share this rule.
func OrgFreedByPurge(cfg Config, at int) bool {
	if at < 0 || at >= len(cfg.Trash) {
		return false
	}
	org := cfg.Trash[at].Setup.Org
	if org == "" {
		return false
	}
	for _, e := range cfg.Setups {
		if e.Org == org {
			return false
		}
	}
	for i, t := range cfg.Trash {
		if i != at && t.Setup.Org == org {
			return false
		}
	}
	return true
}

// isLocalStoreFolder reports whether url is a local folder store lore made
// under the lore home (a file url under .lore/stores/).
func isLocalStoreFolder(url string) bool {
	u, err := neturl.Parse(strings.TrimSpace(url))
	return err == nil && u.Scheme == "file" && strings.Contains(path.Clean(u.Path), "/.lore/stores/")
}
