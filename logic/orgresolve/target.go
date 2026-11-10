// target.go — F14's write target decision for a create (a core copy of
// v0.1.17's stores.ResolveTarget, so both binaries route a client repo's
// rows the same way, FA-OLDCODE-1). Pure: the caller gathers the inputs
// from the org situation (wire.OrgResolution).

package orgresolve

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Where values of defaults.client_repos (§5.2): "repo", "<org>" or
// "<org>/<folder>".
const (
	// DefaultRepo sends a kind into the client repo itself.
	DefaultRepo = "repo"
)

// ToKind is the shape of a --to value (personalplace.TargetKind, narrowed
// to what F14 decides here).
type ToKind int

// --to shapes.
const (
	ToNone      ToKind = iota // no --to
	ToRepo                    // --to repo
	ToOrg                     // --to <org>
	ToOrgFolder               // --to <org>/<folder>
	ToPersonal                // --to me, me/<org> (placed by F17, not here)
)

// TargetLink is one link of the resolved org for this repo.
type TargetLink struct {
	Folder  string
	Default bool
	Own     bool
}

// TargetInputs are everything F14 reads (pure; gathered by the CLI).
type TargetInputs struct {
	To ToKind
	// Org is the resolved org entry name ("" = none).
	Org string
	// Folder is the --to <org>/<folder> folder.
	Folder string
	// Kind is the row kind as defaults.client_repos spells it (task,
	// memory, rule, decision, comment, …).
	Kind string
	Role Role
	// LinkUnknown: F10 could not tell whether this is a client repo.
	LinkUnknown bool
	// Links are the resolved org's links for this repo.
	Links []TargetLink
	// StoreDefaults is defaults.client_repos from the store's
	// lore-org.yaml; PersonalDefaults from ~/.lore/config.yaml. The store's
	// value wins unless the personal one is stricter (§5.2, F14).
	StoreDefaults    map[string]string
	PersonalDefaults map[string]string
	// Agent: the writer is an agent actor. TTY: a person can answer.
	Agent bool
	TTY   bool
	// CommittedProject is .lore/org.yaml's project (an own repo's internal
	// folder), "" when none.
	CommittedProject string
}

// TargetDecision is where the row goes, and why (printed by --dry-run).
type TargetDecision struct {
	// Repo: write into this repo's own project.
	Repo bool
	// Folder: the store project folder (when !Repo and not personal).
	Folder string
	Rule   string
}

// Errors of ResolveTarget; the CLI maps them to codes.
var (
	// ErrLinkUnknown: fail closed (E_LINK_UNKNOWN).
	ErrLinkUnknown = errors.New("stores: cannot tell whether this repo is a client repo")
	// ErrTwoLinksNoDefault: TEAM-032.
	ErrTwoLinksNoDefault = errors.New("stores: this repo is linked to several projects and none is the default")
	// ErrNoLinkedProject: --to <org> with no link and no committed project.
	ErrNoLinkedProject = errors.New("stores: no internal project is linked to this repo")
	// ErrNeedsExplicitTo: rules and decisions by agents or without a TTY in
	// a client repo (F14, TEAM-035).
	ErrNeedsExplicitTo = errors.New("stores: this write needs an explicit --to")
)

// kinds that need an explicit --to in a client repo when the writer is an
// agent or has no terminal (TEAM-035): their default is "repo", so a
// careless write would reach the client.
var explicitKinds = map[string]bool{"rule": true, "decision": true}

// ResolveTarget is F14 for the store side: given the --to shape, the kind
// and the repo's role, it decides repo vs store folder. Personal targets
// are placed by personalplace (F17) and never reach here.
func ResolveTarget(in TargetInputs) (TargetDecision, error) {
	switch in.To {
	case ToRepo:
		return TargetDecision{Repo: true, Rule: "--to repo"}, nil
	case ToOrgFolder:
		return TargetDecision{Folder: in.Folder, Rule: "--to " + in.Org + "/" + in.Folder}, nil
	case ToOrg:
		f, why, err := defaultLink(in)
		if err != nil {
			return TargetDecision{}, err
		}
		return TargetDecision{Folder: f, Rule: "--to " + in.Org + " → " + why}, nil
	case ToPersonal:
		return TargetDecision{}, errors.New("stores: personal targets are placed by F17")
	case ToNone:
		// no --to: the defaults below decide
	}
	// No --to.
	if in.LinkUnknown {
		return TargetDecision{}, ErrLinkUnknown
	}
	if in.Role != RoleClient || in.Org == "" {
		return TargetDecision{Repo: true, Rule: "own repo or no org: this repo"}, nil
	}
	where, src := effectiveDefault(in.Kind, in.StoreDefaults, in.PersonalDefaults)
	if where == "" {
		where, src = in.Org, "a kind not listed goes to the linked project"
	}
	if where == DefaultRepo {
		if explicitKinds[in.Kind] && (in.Agent || !in.TTY) {
			who := "without a terminal"
			if in.Agent {
				who = "by an agent"
			}
			return TargetDecision{}, fmt.Errorf("%w: a %s written %s in a client repo must pass --to (repo or %s); nothing written", ErrNeedsExplicitTo, in.Kind, who, in.Org)
		}
		return TargetDecision{Repo: true, Rule: "defaults.client_repos." + in.Kind + " = repo (" + src + ")"}, nil
	}
	org, folder, hasFolder := strings.Cut(where, "/")
	if org != in.Org {
		// A default naming another org can never be followed (orgs never
		// mix); fall back to the safe place, the linked project.
		hasFolder = false
	}
	if hasFolder {
		return TargetDecision{Folder: folder, Rule: "defaults.client_repos." + in.Kind + " = " + where + " (" + src + ")"}, nil
	}
	f, why, err := defaultLink(in)
	if err != nil {
		return TargetDecision{}, err
	}
	return TargetDecision{Folder: f, Rule: "defaults.client_repos." + in.Kind + " = " + where + " (" + src + ") → " + why}, nil
}

// defaultLink picks the linked project for --to <org> (TEAM-032).
func defaultLink(in TargetInputs) (string, string, error) {
	switch len(in.Links) {
	case 0:
		if in.CommittedProject != "" {
			return in.CommittedProject, ".lore/org.yaml project", nil
		}
		if in.LinkUnknown {
			return "", "", ErrLinkUnknown
		}
		return "", "", fmt.Errorf("%w (use --to %s/<folder>, or `lore org link %s --project <folder>`)", ErrNoLinkedProject, in.Org, in.Org)
	case 1:
		return in.Links[0].Folder, "the linked project " + in.Links[0].Folder + "/", nil
	}
	var defaults, all []string
	for _, l := range in.Links {
		all = append(all, l.Folder)
		if l.Default {
			defaults = append(defaults, l.Folder)
		}
	}
	sort.Strings(all)
	if len(defaults) == 1 {
		return defaults[0], "the default link " + defaults[0] + "/", nil
	}
	return "", "", fmt.Errorf("%w: candidates %s; pass --to %s/<folder> or mark one with `lore org link %s --project <folder> --default`",
		ErrTwoLinksNoDefault, strings.Join(all, ", "), in.Org, in.Org)
}

// effectiveDefault merges defaults.client_repos for one kind: the store's
// value wins, except that a personal value may make it stricter — any
// store target is stricter than "repo" (§5.2: "personal config may only
// make a default stricter").
func effectiveDefault(kind string, store, personal map[string]string) (string, string) {
	s, sok := lookupKind(store, kind)
	p, pok := lookupKind(personal, kind)
	switch {
	case sok && pok && s == DefaultRepo && p != DefaultRepo:
		return p, "personal config, stricter than the store's"
	case sok:
		return s, "the store's lore-org.yaml"
	case pok:
		return p, "personal config"
	}
	return "", ""
}

// lookupKind reads a kind with singular/plural tolerance ("memory" and
// "memories" are the same kind in hand-written config).
func lookupKind(m map[string]string, kind string) (string, bool) {
	want := singular(kind)
	for k, v := range m {
		if singular(k) == want {
			return v, true
		}
	}
	return "", false
}

// singular turns a table name into its kind (memories → memory, tasks → task).
func singular(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	switch {
	case strings.HasSuffix(k, "ies"):
		return strings.TrimSuffix(k, "ies") + "y"
	case strings.HasSuffix(k, "s"):
		return strings.TrimSuffix(k, "s")
	}
	return k
}
