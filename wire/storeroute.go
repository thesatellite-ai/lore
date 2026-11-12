package wire

// storeroute.go — the one helper that decides which setup's ssh route
// (ONBOARDING_DESIGN 3.4, Q5) a git network call to a store uses (issue
// 15). Every store call site asks it: the store clone backend (clone,
// fetch, push), the org link reads and writes (orgcfg: fetch, ls-remote,
// push) and the signals remote of a linked store clone (gitremote).
//
//	storeRoute(url) ─▶ the setups that own url:
//	                     personal_store: <url>          (solo, member, admin)
//	                     personal_store: inherit (or unset) and url is the
//	                       shared personal.store         (every such setup)
//	                     a store of orgs[<setup.org>]    (member, admin)
//	                ─▶ one owner, or the active setup among several,
//	                   or several that agree on the route ─▶ routeFor(its github block)
//	                ─▶ no owner, or owners that disagree ─▶ nothing (git as configured)
//
// Identity rule: a url no setup owns never gets an alias. A store url
// matches a setup's store when the paths are equal (case-insensitive,
// without .git) and the hosts are equal, or both are github.com or that
// setup's own host alias (the spelling lore writes for it). Another
// host with the same owner/repo path is a different store.

import (
	"context"
	"net/url"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// storeRoute is the git args and env that reach store url as the setup
// that owns it (see the file comment); ok is false when no setup owns it
// or the owners disagree, and then args and env are empty.
func storeRoute(ctx context.Context, home, dir, storeURL string) (args, env []string, ok bool) {
	args, env, ok = ownerRoute(ctx, home, dir, storeURL)
	// brief 3.7: this machine's access rules (config.yaml access:) map a
	// canonical url to the person's own spelling, whoever owns the store
	if acc := orgconf.AccessArgs(accessRules(home), strings.TrimSpace(storeURL)); len(acc) > 0 {
		return append(acc, args...), env, true
	}
	return args, env, ok
}

// accessRules is config.yaml's access: rules (none when the file does not
// read: every command refuses a broken config.yaml anyway).
func accessRules(home string) []orgconf.Access {
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil {
		return nil
	}
	return cfg.Access
}

// ownerRoute is the route of the setup that owns storeURL (see the file
// comment).
func ownerRoute(ctx context.Context, home, dir, storeURL string) (args, env []string, ok bool) {
	owners := storeOwners(home, storeURL)
	if len(owners) == 0 {
		return nil, nil, false
	}
	pick := owners[0]
	if len(owners) > 1 {
		res, err := ResolveSetup(ctx, setupQueryFor(ctx, home, dir, nil))
		i := -1
		if err == nil && res.Active.Name != "" {
			i = slices.IndexFunc(owners, func(e onboard.SetupEntry) bool { return e.Name == res.Active.Name })
		}
		if i >= 0 {
			pick = owners[i]
		} else if !sameRoutes(owners) {
			return nil, nil, false // several accounts own it and none is active here: no guess
		}
	}
	args, env = routeFor(pick.GitHub, userHome(SetupQuery{}))
	return args, env, true
}

// sameRoutes reports whether every owner reaches GitHub the same way.
func sameRoutes(owners []onboard.SetupEntry) bool {
	h := userHome(SetupQuery{})
	a0, e0 := routeFor(owners[0].GitHub, h)
	for _, o := range owners[1:] {
		a, e := routeFor(o.GitHub, h)
		if !slices.Equal(a, a0) || !slices.Equal(e, e0) {
			return false
		}
	}
	return true
}

// storeOwners is every enabled setup that owns storeURL, in config order.
func storeOwners(home, storeURL string) []onboard.SetupEntry {
	cfg, err := ReadSetupsConfig(home)
	if err != nil || len(cfg.Setups) == 0 {
		return nil
	}
	oc, _, _ := orgcfg.LoadConfig(home) // a broken org config: personal stores still match
	// personal.store is owned by every setup that inherits it; an unreadable
	// machine config leaves it unowned (git as configured, never a guess).
	shared := ""
	if mc, err := readMachineConfig(home); err == nil {
		shared = mc.personalURL()
	}
	var out []onboard.SetupEntry
	for _, s := range cfg.Setups {
		if s.Disabled {
			continue
		}
		alias := strings.TrimSpace(s.GitHub.HostAlias)
		owned := false
		switch p := strings.TrimSpace(s.PersonalStore); p {
		case onboard.PersonalStoreNone:
		case "", onboard.PersonalStoreInherit:
			owned = shared != "" && sameStore(storeURL, shared, alias)
		default:
			owned = sameStore(storeURL, p, alias)
		}
		if !owned && s.Org != "" && oc != nil {
			if o := oc.FindOrg(s.Org); o != nil {
				for _, st := range o.Stores {
					if sameStore(storeURL, st.URL, alias) {
						owned = true
						break
					}
				}
			}
		}
		if owned {
			out = append(out, s)
		}
	}
	return out
}

// sameStore reports whether a and b name one store for a setup whose host
// alias is alias ("" = none).
func sameStore(a, b, alias string) bool {
	ha, pa, oka := storeHostPath(a)
	hb, pb, okb := storeHostPath(b)
	if !oka || !okb || pa != pb {
		return false
	}
	if ha == hb {
		return true
	}
	gh := func(h string) bool {
		return h == probe.GitHubHost || (alias != "" && h == strings.ToLower(alias))
	}
	return gh(ha) && gh(hb)
}

// storeHostPath splits a remote url into its lower-case host and
// owner/repo path without .git: scp-like (git@host:owner/repo.git),
// ssh:// and https:// forms. ok is false for a local path or file:// url.
func storeHostPath(raw string) (host, path string, ok bool) {
	u := strings.TrimSpace(raw)
	switch {
	case strings.Contains(u, "://"):
		p, err := url.Parse(u)
		if err != nil || p.Scheme == "file" || p.Hostname() == "" {
			return "", "", false
		}
		host, path = p.Hostname(), p.Path
	case strings.Contains(u, ":") && !strings.HasPrefix(u, "/"):
		h, p, _ := strings.Cut(u, ":")
		if _, after, found := strings.Cut(h, "@"); found {
			h = after
		}
		host, path = h, p
	default:
		return "", "", false
	}
	path = strings.ToLower(strings.Trim(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/"))
	host = strings.ToLower(host)
	if host == "" || !strings.Contains(path, "/") {
		return "", "", false
	}
	return host, path, true
}

// storeRouter is orgcfg's Router over storeRoute: the org link reads and
// writes reach each store as the setup that owns it, and nothing else.
func storeRouter(home, dir string) orgcfg.Router {
	return func(ctx context.Context, u string) ([]string, []string) {
		args, env, _ := storeRoute(ctx, home, dir, u)
		return args, env
	}
}

// cloneRoute is the store clone backend's route (git.WithRoute): the
// owning setup's route only. A store no setup owns (a public store, a
// third account's store attached through stores.yaml) is reached as git
// is configured, never through the active setup's alias: that alias
// rewrites every github.com url and would reach the store as the wrong
// account.
func cloneRoute(ctx context.Context, home, dir, remote string) (args, env []string) {
	args, env, _ = storeRoute(ctx, home, dir, remote)
	return args, env
}
