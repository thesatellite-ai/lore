package wire

// storeaccess.go — canonical store urls in shared config, this machine's
// own access in config.yaml (brief 3.7, logic/orgconf/access.go).
//
// lore-org.yaml and a repo's .lore/stores.yaml are read by every teammate,
// so they name a store as everyone reaches it (git@github.com:acme/x.git).
// A person who typed an ssh alias of theirs (git@work-gh:acme/x.git, where
// ~/.ssh/config says `Host work-gh` / `HostName github.com`) gets:
//
//	typed url ─▶ ~/.ssh/config: alias? ─no─▶ stored as typed
//	            └─yes─▶ canonical url into the shared file
//	                    + access: {url: git@github.com:acme/, via: git@work-gh:acme/}
//	                      in this machine's config.yaml (once per owner:
//	                      never the whole host, D-ah)
//	                    + one output line saying both
//
// Every git network call to that store then runs with
// `-c url.git@work-gh:acme/.insteadOf=git@github.com:acme/` (storeroute.go), never
// a write to the person's git config. Reads never rewrite shared config:
// an alias already in a shared file is doctor's finding (DoctorStoreAccess).

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/errs"
)

// sshAliasResolver answers from <user home>/.ssh/config (Includes
// followed; probe.ReadSSHConfig) what ssh connects to for a host: ok only
// when a Host block renames it to another HostName. A config that does not
// read resolves nothing (the url is then stored as typed).
func sshAliasResolver(ctx context.Context) orgconf.AliasResolver {
	home := userHome(SetupQuery{})
	var cfg *probe.SSHConfig
	return func(host string) (orgconf.SSHAlias, bool) {
		if cfg == nil {
			c, err := probe.New(probe.Options{Home: home}).ReadSSHConfig(ctx)
			if err != nil {
				return orgconf.SSHAlias{}, false
			}
			cfg = &c
		}
		eff := cfg.Effective(host)
		if eff.HostName == "" || strings.EqualFold(eff.HostName, host) {
			return orgconf.SSHAlias{}, false
		}
		return orgconf.SSHAlias{HostName: eff.HostName, User: eff.User}, true
	}
}

// init gives the git backend's same-store test (gitbackend.SameRepoViaAlias:
// is a clone whose origin uses another host name this store's?) the
// person's ssh config, so an alias is recognised by what ssh connects to,
// the no-dot guess only for a host the config does not name. The config
// is read on each question: it is asked only when a clone folder holds an
// origin with the same path on another host.
func init() {
	gitbackend.SetAliasHosts(func(host string) (string, bool) {
		al, ok := sshAliasResolver(context.Background())(host)
		return al.HostName, ok
	})
}

// typedStoreURL is a store url a person typed for a shared file, after
// canonicalTyped: URL is what the shared file gets; Alias is set when the
// typed url used an ssh alias (then Rule maps URL back for this machine).
type typedStoreURL struct {
	Typed, URL string
	Alias      *orgconf.Canonicalized
}

// canonicalTyped resolves an ssh-alias url to its canonical form (no
// write); any other url is returned as typed.
func canonicalTyped(ctx context.Context, raw string) typedStoreURL {
	c, err := orgconf.CanonicalSSH(raw, sshAliasResolver(ctx))
	if err != nil {
		return typedStoreURL{Typed: raw, URL: raw}
	}
	return typedStoreURL{Typed: raw, URL: c.URL, Alias: &c}
}

// Note is the output line for an alias url ("" for any other): what the
// shared file gets and how this machine still reaches it.
func (t typedStoreURL) Note() string {
	if t.Alias == nil {
		return ""
	}
	c := t.Alias
	return fmt.Sprintf("%s uses your ssh alias %s (HostName %s): the shared config gets the canonical url %s, and ~/.lore/config.yaml maps it back for this machine (access: url %s via %s)",
		t.Typed, c.Alias, c.HostName, c.URL, c.Rule.URL, c.Rule.Via)
}

// withRule is r plus t's access rule for the calls of this command, before
// the rule is recorded (a dry run reaches the store through the alias
// without writing config.yaml).
func (t typedStoreURL) withRule(r orgcfg.Router) orgcfg.Router {
	if t.Alias == nil {
		return r
	}
	rule := []orgconf.Access{t.Alias.Rule}
	return func(ctx context.Context, u string) ([]string, []string) {
		var args, env []string
		if r != nil {
			args, env = r(ctx, u)
		}
		return append(orgconf.AccessArgs(rule, u), args...), env
	}
}

// recordAccess writes t's access rule into config.yaml (nothing for a url
// that used no alias, or when a rule already maps the same prefix the
// same way). A rule mapping the prefix elsewhere is kept: the person
// chose it, and the error says which one wins and how to change it.
func recordAccess(home string, t typedStoreURL) error {
	if t.Alias == nil {
		return nil
	}
	want := t.Alias.Rule
	for _, r := range accessRules(home) {
		if strings.TrimSpace(r.URL) != want.URL {
			continue
		}
		if strings.TrimSpace(r.Via) == want.Via {
			return nil
		}
		return errs.New(errs.InvalidInput, fmt.Sprintf("~/.lore/config.yaml access: already maps %s via %s, and the url typed reaches it via %s", want.URL, r.Via, want.Via)).
			WithHint("type the canonical url (" + t.URL + "), or change that access: rule's via in ~/.lore/config.yaml to the alias you use")
	}
	return writeOrgConfig(home, func(root *yaml.Node) error { return appendAccessNode(root, want) })
}

// appendAccessNode adds rule to root's access: list (made when absent).
func appendAccessNode(root *yaml.Node, rule orgconf.Access) error {
	var list *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == orgconf.AccessKey {
			list = root.Content[i+1]
			break
		}
	}
	if list == nil {
		list = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: orgconf.AccessKey}, list)
	}
	if list.Kind != yaml.SequenceNode {
		return errs.New(errs.ConfigInvalid, "~/.lore/config.yaml: access: is not a list").
			WithHint("make it a list of {url, via} entries (docs: store access)")
	}
	n := &yaml.Node{}
	if err := n.Encode(rule); err != nil {
		return errs.Wrap(errs.Internal, "wire: encode the access rule", err)
	}
	list.Content = append(list.Content, n)
	return nil
}

// accessNoteFor is the git backend's WithAccessNote over home's
// config.yaml: a failed network call to a remote an access rule maps names
// that rule (url → via, where it is set) and the next step.
func accessNoteFor(home string) func(remote string) string {
	return func(remote string) string {
		if r, ok := orgconf.AccessFor(accessRules(home), strings.TrimSpace(remote)); ok {
			return orgconf.AccessFailureNote(r)
		}
		return ""
	}
}

// canonicalShared is the url a store command writes into a shared file
// for raw: raw itself, or for an ssh-alias url its canonical url, with
// the access rule recorded in config.yaml and the person told (notice).
func (a *App) canonicalShared(ctx context.Context, raw string) (string, error) {
	t := canonicalTyped(ctx, raw)
	if t.Alias == nil {
		return raw, nil
	}
	if err := recordAccess(a.opts.Home, t); err != nil {
		return "", err
	}
	a.notice("lore: " + t.Note())
	return t.URL, nil
}
