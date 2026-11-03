package identity

// noorg.go — the fallback identity when no org resolves (LORE_TEAM_SPEC F10
// rule 6, F18; coordinator Q7, fail closed, no cross-org identity bleed):
//
//   - config.yaml has no orgs      → the legacy chain, unchanged (Resolve).
//   - orgs exist, none matched     → the top-level `identity:` of config.yaml.
//   - …and it is absent            → the legacy chain, but every candidate
//     equal to an org identity key is skipped, with ONE note naming the
//     skipped sources and orgs (never the key) and how to set a top-level
//     identity.
//
// Why: the typical laptop has `git config user.email` = the work email, which
// is also the org identity key. Without the skip, a personal or third-party
// repo would be stamped with the company identity.

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"saas/pkg/aicoder/lorecfg"
)

// StepTopConfig: the identity came from the top-level `identity:` of
// ~/.lore/config.yaml (no org matched). Appended after StepOrgConfig so no
// existing step number changes.
const StepTopConfig Step = StepOrgConfig + 1

// stepTopConfigName is StepTopConfig's display name.
const stepTopConfigName = "top-level identity (config.yaml)"

// TopIdentityHint is the one-line note printed when orgs exist, none matched
// and no top-level identity is set. Exported so tests and docs agree.
const TopIdentityHint = "no org matched and ~/.lore/config.yaml has no top-level identity; " + topIdentityFix

// topIdentityFix is the shared "how to fix it" tail of the no-org notes.
const topIdentityFix = "set a top-level identity: {key: \"human:<your personal email>\", name: \"Your Name\"} in ~/.lore/config.yaml"

// NoOrgInputs is everything ResolveNoOrg reads.
type NoOrgInputs struct {
	// Config is ~/.lore/config.yaml; nil is treated as a config without orgs.
	Config *lorecfg.Config
	// FlagActor is --actor, when a command offers it.
	FlagActor string
}

// ResolveNoOrg returns the identity for a repo where no org resolved. It
// never errors: like Resolve, the chain always ends in some identity.
func ResolveNoOrg(in NoOrgInputs) Resolved {
	if in.Config == nil || len(in.Config.Orgs) == 0 {
		return Resolve(Inputs{FlagActor: in.FlagActor})
	}
	// key → the org entries using it; names, never keys, go into notes so a
	// no-org repo's output never shows a company identity (E08).
	orgKeys := map[string][]string{}
	for i := range in.Config.Orgs {
		if k := strings.TrimSpace(in.Config.Orgs[i].Identity.Key); k != "" {
			orgKeys[k] = append(orgKeys[k], in.Config.Orgs[i].Name)
		}
	}
	skip := func(key string) bool { return len(orgKeys[strings.TrimSpace(key)]) > 0 }

	if top := in.Config.Identity; strings.TrimSpace(top.Key) != "" {
		// --actor / LORE_ACTOR keep their place above the identity file
		// (steps 1–2), still never as an org key. Only those two steps are
		// tried here: the later ones may persist a salt file.
		for _, c := range []struct {
			v    string
			step Step
		}{{in.FlagActor, StepFlag}, {os.Getenv(EnvActor), StepEnv}} {
			if v := strings.TrimSpace(c.v); v != "" && !skip(v) {
				return Resolved{StableKey: v, DisplayName: extractDisplayName(v), Kind: kindFromKey(v), Step: c.step}
			}
		}
		key := strings.TrimSpace(top.Key)
		display := strings.TrimSpace(top.Name)
		if display == "" {
			display = extractDisplayName(key)
		}
		return Resolved{StableKey: key, DisplayName: display, Kind: kindFromKey(key), Step: StepTopConfig, Handle: top.Handle}
	}

	r, skipped := resolveChain(Inputs{FlagActor: in.FlagActor}, skip)
	r.Notes = append(r.Notes, skippedNote(skipped, orgKeys))
	return r
}

// skippedNote is the ONE line printed on the no-top-level path: which
// sources were skipped and for which orgs (never the keys themselves), plus
// how to fix it; TopIdentityHint alone when nothing was skipped.
func skippedNote(skipped []Resolved, orgKeys map[string][]string) string {
	if len(skipped) == 0 {
		return TopIdentityHint
	}
	var sources, orgs []string
	seenOrg := map[string]bool{}
	for _, sk := range skipped {
		sources = append(sources, sourceLabel(sk.Step))
		for _, o := range orgKeys[strings.TrimSpace(sk.StableKey)] {
			if !seenOrg[o] {
				seenOrg[o] = true
				orgs = append(orgs, o)
			}
		}
	}
	sort.Strings(orgs)
	verb, was := "is an org identity", "was"
	if len(sources) > 1 {
		verb, was = "are org identities", "were"
	}
	return fmt.Sprintf("your %s %s (%s) and %s skipped; %s",
		strings.Join(sources, " and "), verb, strings.Join(orgs, ", "), was, topIdentityFix)
}

// sourceLabel names a chain step the way a user knows it.
func sourceLabel(s Step) string {
	switch s {
	case StepFlag:
		return "--actor"
	case StepEnv:
		return EnvActor
	case StepIdentityFile:
		return "~/.lore/identity.yaml"
	case StepGitConfig:
		return "git email"
	default:
		return s.String()
	}
}
