package wire

// doctor_storeaccess.go — the store_access check of `lore doctor` (brief
// 3.7): shared config names stores by their canonical url, and each
// machine says in its own config.yaml how it reaches them (access:,
// storeaccess.go). Two findings, each with the exact fix:
//
//	org store / stores.yaml url ─▶ an ssh alias of this machine? ─▶ warning:
//	    teammates cannot use it; the canonical url for the shared file and
//	    the access: lines that keep this machine reaching it (never rewritten
//	    by doctor: shared config is changed by a person, on purpose)
//	                            ─▶ cloned here and the remote does not answer
//	    (ls-remote through the store's route) ─▶ warning naming the
//	    canonical url, the access: lines for the ssh alias ~/.ssh/config has
//	    for that host (or a placeholder alias), and the git config command
//	    that does the same for every program
//
//	config.yaml access: rule for a whole host (url git@github.com:) ─▶
//	    warning: it maps every repo of that host, so a store of another
//	    account (a personal store) goes through the alias too; names the
//	    stores under another owner and gives the owner-scoped lines
//	    (D-ah); `lore doctor --fix` replaces the rule with them
//
// It only reads (bar --fix on a host-wide rule); stores with no clone on
// this machine are not probed (the first command that reads one says when
// it cannot be reached).

import (
	"context"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/logic/gitfault"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
)

// DoctorCheckStoreAccess is the check's JSON name.
const DoctorCheckStoreAccess DoctorCheck = "store_access"

// aliasPlaceholder stands for the person's own ssh alias in a fix when
// ~/.ssh/config has none for the host.
const aliasPlaceholder = "<your-ssh-alias>"

// accessTarget is one store url the check looks at, with how output names
// it and where it is configured.
type accessTarget struct {
	Label, URL, Branch, Where string
	// Name is what `lore store set-url --name` takes for this store (the
	// org's name for its org-folder store); "" falls back to Label.
	Name string
}

// DoctorStoreAccess runs the check (see the file comment) without fixing.
func (a *App) DoctorStoreAccess(ctx context.Context) ([]DoctorFinding, error) {
	return a.DoctorStoreAccessFix(ctx, false)
}

// storeAccess is the check as doctor's runner, fixing with fix.
func (a *App) storeAccess(fix bool) func(context.Context) ([]DoctorFinding, error) {
	return func(c context.Context) ([]DoctorFinding, error) { return a.DoctorStoreAccessFix(c, fix) }
}

// DoctorStoreAccessFix runs the check; with fix (`lore doctor --fix`) a
// host-wide access rule is replaced by its owner-scoped rules.
func (a *App) DoctorStoreAccessFix(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	resolve := sshAliasResolver(ctx)
	out := a.hostWideFindings(ctx, fix)
	seen := map[string]bool{}
	for _, t := range a.accessTargets(ctx) {
		k, ok := storeKey(t.URL)
		if !ok || seen[k+"#"+t.Where] {
			continue
		}
		seen[k+"#"+t.Where] = true
		if c, err := orgconf.CanonicalSSH(t.URL, resolve); err == nil {
			out = append(out, aliasInSharedFinding(t, c))
			continue
		}
		if f, bad := a.unreachableFinding(ctx, t); bad {
			out = append(out, f)
		}
	}
	return out, nil
}

// accessTargets are the configured org stores (config.yaml orgs, copied
// from lore-org.yaml) and the git stores of this repo's stores.yaml.
func (a *App) accessTargets(ctx context.Context) []accessTarget {
	var out []accessTarget
	if cfg, _, err := orgcfg.LoadConfig(a.opts.Home); err == nil && cfg != nil {
		for _, o := range cfg.Orgs {
			main := o.OrgFolderStore()
			for _, sc := range o.Stores {
				name := sc.ShortName()
				if main != nil && main.URL == sc.URL {
					name = o.Name
				}
				out = append(out, accessTarget{Label: sc.ShortName(), URL: sc.URL, Branch: sc.Branch,
					Where: "org " + o.Name + "'s " + orgconf.OrgFileName, Name: name})
			}
		}
	}
	// repoTop, not storeRepoTop: outside a repo there is simply no
	// stores.yaml to read, and the extra work-tree probe would cost doctor
	// --all one more git call (land7 B budget)
	if top, err := a.repoTop(ctx); err == nil && top != "" {
		if c, rerr := readStoresFile(top); rerr == nil {
			for _, s := range c.Stores {
				if strings.Contains(string(s.Name), targetSep) {
					continue // a folder entry follows its store
				}
				if _, ssh := orgconf.HostOf(s.URL); ssh || strings.HasPrefix(s.URL, "https://") {
					out = append(out, accessTarget{Label: string(s.Name), URL: s.URL, Branch: s.Branch, Where: ".lore/stores.yaml"})
				}
			}
		}
	}
	return out
}

// accessPersonalTargets are the personal stores config.yaml names (each
// setup's personal_store url, and the shared personal.store): stores that
// usually belong to another account than an org's alias.
func (a *App) accessPersonalTargets() []accessTarget {
	var out []accessTarget
	if mc, err := readMachineConfig(a.opts.Home); err == nil {
		if u := mc.personalURL(); u != "" {
			out = append(out, accessTarget{Label: "personal", URL: u, Where: "~/.lore/config.yaml personal.store"})
		}
	}
	if sc, err := ReadSetupsConfig(a.opts.Home); err == nil {
		for _, st := range sc.Setups {
			switch p := strings.TrimSpace(st.PersonalStore); p {
			case "", onboard.PersonalStoreNone, onboard.PersonalStoreInherit:
			default:
				out = append(out, accessTarget{Label: "personal (setup " + st.Name + ")", URL: p, Where: "~/.lore/config.yaml setup " + st.Name})
			}
		}
	}
	return out
}

// hostWideFindings warns about each config.yaml access rule that maps a
// whole host (see the file comment). The owners the rule is meant for are
// the owners of the org stores it maps (of every store it maps when no org
// store matches); a store under any other owner is named as reached
// through the alias by mistake. With fix the rule is replaced by one rule
// per meant owner (nothing is changed when no owner is known).
func (a *App) hostWideFindings(ctx context.Context, fix bool) []DoctorFinding {
	rules := accessRules(a.opts.Home)
	if !slices.ContainsFunc(rules, orgconf.HostWide) {
		return nil
	}
	all := append(a.accessTargets(ctx), a.accessPersonalTargets()...)
	var out []DoctorFinding
	for _, r := range rules {
		r = orgconf.Access{URL: strings.TrimSpace(r.URL), Via: strings.TrimSpace(r.Via)}
		if !orgconf.HostWide(r) {
			continue
		}
		f, narrow := hostWideFinding(r, all)
		if fix && len(narrow) > 0 {
			if err := writeOrgConfig(a.opts.Home, func(root *yaml.Node) error { return replaceAccessNode(root, r, narrow) }); err != nil {
				f.Message += "; the rewrite failed: " + errLine(err)
			} else {
				f.Fixed = true
			}
		}
		out = append(out, f)
	}
	return out
}

// hostWideFinding is the warning for the host-wide rule r over the
// configured stores, and the owner-scoped rules that replace it ("" owner
// stores and stores the rule does not map are left out).
func hostWideFinding(r orgconf.Access, targets []accessTarget) (DoctorFinding, []orgconf.Access) {
	type hit struct {
		t     accessTarget
		owner string
		org   bool
	}
	var hits []hit
	for _, t := range targets {
		u := strings.TrimSpace(t.URL)
		if !strings.HasPrefix(u, r.URL) {
			continue
		}
		if o := orgconf.OwnerOf(u); o != "" {
			hits = append(hits, hit{t: t, owner: o, org: strings.HasPrefix(t.Where, "org ")})
		}
	}
	var meant []string
	for _, h := range hits {
		if h.org && !slices.Contains(meant, h.owner) {
			meant = append(meant, h.owner)
		}
	}
	if len(meant) == 0 {
		for _, h := range hits {
			if !slices.Contains(meant, h.owner) {
				meant = append(meant, h.owner)
			}
		}
	}
	var others []string
	for _, h := range hits {
		if !slices.Contains(meant, h.owner) {
			others = append(others, h.t.Label+" ("+h.t.URL+", owner "+h.owner+")")
		}
	}
	msg := "~/.lore/config.yaml access: maps every repo of " + strings.TrimRight(r.URL, ":/") + " (url " + r.URL + " via " + r.Via + "), so a repo of any account on that host goes through " + r.Via
	if len(others) > 0 {
		msg += "; these stores are under another owner than the alias's (" + strings.Join(meant, ", ") + ") and go through it too: " + strings.Join(others, "; ")
	}
	var narrow []orgconf.Access
	for _, o := range meant {
		narrow = append(narrow, orgconf.Narrowed(r, o))
	}
	var b strings.Builder
	if len(narrow) == 0 {
		n := orgconf.Narrowed(r, "<owner>")
		b.WriteString("replace it in ~/.lore/config.yaml with one rule per owner the alias is for:\n" + orgconf.FixFor(n).YAML)
		return DoctorFinding{Check: DoctorCheckStoreAccess, Level: DoctorWarning, Message: msg, Fix: b.String()}, nil
	}
	b.WriteString("replace it in ~/.lore/config.yaml with the owner-scoped rules (or lore doctor --fix, which writes exactly these):\n" + orgconf.AccessKey + ":")
	for _, n := range narrow {
		b.WriteString(strings.TrimPrefix(orgconf.FixFor(n).YAML, orgconf.AccessKey+":"))
	}
	b.WriteString("\n(a global git rule the same way, per owner:")
	for _, n := range narrow {
		b.WriteString(" " + orgconf.FixFor(n).GitCommand + ";")
	}
	b.WriteString(" never a host-wide one)")
	return DoctorFinding{Check: DoctorCheckStoreAccess, Level: DoctorWarning, Message: msg, Fix: b.String()}, narrow
}

// replaceAccessNode replaces the access: entry mapping old.URL to old.Via
// with the rules of with, in its place.
func replaceAccessNode(root *yaml.Node, old orgconf.Access, with []orgconf.Access) error {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != orgconf.AccessKey || root.Content[i+1].Kind != yaml.SequenceNode {
			continue
		}
		list := root.Content[i+1]
		for j, n := range list.Content {
			var r orgconf.Access
			if n.Decode(&r) != nil || strings.TrimSpace(r.URL) != old.URL || strings.TrimSpace(r.Via) != old.Via {
				continue
			}
			repl := make([]*yaml.Node, 0, len(with))
			for _, w := range with {
				if slices.ContainsFunc(list.Content, func(e *yaml.Node) bool {
					var x orgconf.Access
					return e.Decode(&x) == nil && strings.TrimSpace(x.URL) == w.URL
				}) {
					continue // already mapped by a rule of its own
				}
				nn := &yaml.Node{}
				if err := nn.Encode(w); err != nil {
					return errs.Wrap(errs.Internal, "wire: encode the access rule", err)
				}
				repl = append(repl, nn)
			}
			list.Content = append(append(append([]*yaml.Node(nil), list.Content[:j]...), repl...), list.Content[j+1:]...)
			return nil
		}
	}
	return errs.New(errs.NotFound, "~/.lore/config.yaml has no access: rule url "+old.URL+" via "+old.Via).
		WithHint("run lore doctor again: the file changed since it was read")
}

// aliasInSharedFinding is the warning for a shared url spelled with this
// machine's ssh alias.
func aliasInSharedFinding(t accessTarget, c orgconf.Canonicalized) DoctorFinding {
	fix := orgconf.FixFor(c.Rule)
	return DoctorFinding{Check: DoctorCheckStoreAccess, Level: DoctorWarning,
		Message: "store " + t.Label + " in " + t.Where + " is " + t.URL + ", spelled with your ssh alias " + c.Alias + " (HostName " + c.HostName + "), which teammates do not have; shared config should name the canonical url " + c.URL,
		Fix:     "change the url in " + t.Where + " to " + c.URL + " (then `lore org refresh` on each machine), and add to ~/.lore/config.yaml so this machine keeps using the alias:\n" + fix.YAML + "\n(or once for every program: " + fix.GitCommand + ")"}
}

// unreachableFinding probes a cloned store's remote; bad is false when it
// answered, or the store has no clone here.
func (a *App) unreachableFinding(ctx context.Context, t accessTarget) (DoctorFinding, bool) {
	if gitenv.NoStoreFetch(os.Environ()) {
		// LORE_OFFLINE (a dry run reaches no remote) or LORE_NO_STORE_FETCH
		// (the --all parent fetched every clone just now): not probed again
		return DoctorFinding{}, false
	}
	dir, _, err := gitbackend.CloneDirOf(a.opts.Home, t.URL, "")
	if err != nil {
		return DoctorFinding{}, false
	}
	if st, serr := os.Stat(dir); serr != nil || !st.IsDir() {
		return DoctorFinding{}, false
	}
	perr := gitbackend.ProbeRemote(ctx, dir, t.URL, gitbackend.WithRoute(func(ctx context.Context, remote string) ([]string, []string) {
		return cloneRoute(ctx, a.opts.Home, a.projectDir(), remote)
	}))
	if perr == nil {
		return DoctorFinding{}, false
	}
	fix := accessFixText(t.URL, accessRules(a.opts.Home), sshAliasesFor(ctx, t.URL))
	if gitbackend.ProbeFault(perr) == gitfault.Missing {
		// H-11: the host answered "no repository": the route works, so an
		// ssh alias is not the fix; the store moved, was deleted, or this
		// machine lost access
		name := t.Name
		if name == "" {
			name = t.Label
		}
		fix = gitbackend.GoneSteps(name, repourl.Mask(t.URL))
	}
	return DoctorFinding{Check: DoctorCheckStoreAccess, Level: DoctorWarning,
		Message: "store " + t.Label + " (from " + t.Where + "): " + reasonOf(perr), // reasonOf names the masked url and git's telling line
		Fix:     fix}, true
}

// accessFixText is the fix for an unreachable canonical url: the access
// rule in use (check that alias), else the config.yaml lines and git
// command for each ssh alias of the url's host (a placeholder alias when
// there is none), else for an https url the credential to check.
func accessFixText(canon string, rules []orgconf.Access, aliases []string) string {
	if r, ok := orgconf.AccessFor(rules, canon); ok {
		msg := "~/.lore/config.yaml access: reaches " + r.URL + " via " + r.Via + "; check that this machine can reach " + r.Via + " (ssh -T, your key, the alias in ~/.ssh/config), or fix that rule's via"
		if orgconf.HostWide(r) {
			msg += "; that rule maps every repo of the host (the store_access warning about it gives the owner-scoped lines)"
		}
		return msg
	}
	host, ssh := orgconf.HostOf(canon)
	credential := "check that this machine may read " + canon + " (the host's login or token for git over https), then run `lore doctor` again"
	if !ssh {
		if len(aliases) == 0 {
			return credential
		}
		host, _ = orgconf.RemoteHost(canon)
	} else if len(aliases) == 0 {
		aliases = []string{aliasPlaceholder}
	}
	var b strings.Builder
	b.WriteString("if this machine reaches " + host + " through an ssh alias, map " + canon + "'s owner to it in ~/.lore/config.yaml (scoped to the owner, never the whole host):")
	for _, al := range aliases {
		rule, ok := orgconf.RuleVia(canon, al)
		if !ok {
			continue
		}
		fix := orgconf.FixFor(rule)
		b.WriteString("\n" + fix.YAML + "\n(or once for every program: " + fix.GitCommand + ")")
	}
	if !ssh {
		b.WriteString("\notherwise " + credential)
		return b.String()
	}
	b.WriteString("\notherwise check that your ssh key may read " + canon)
	return b.String()
}

// sshAliasesFor lists the literal Host names of ~/.ssh/config whose
// HostName is the host of the ssh or https url raw (the host itself left out).
func sshAliasesFor(ctx context.Context, raw string) []string {
	host, ok := orgconf.RemoteHost(raw)
	if !ok {
		return nil
	}
	cfg, err := probe.New(probe.Options{Home: userHome(SetupQuery{})}).ReadSSHConfig(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, h := range cfg.Hosts {
		for _, pat := range h.Patterns {
			if strings.ContainsAny(pat, "*?!") || strings.EqualFold(pat, host) || slices.Contains(out, pat) {
				continue
			}
			if strings.EqualFold(cfg.Effective(pat).HostName, host) {
				out = append(out, pat)
			}
		}
	}
	return out
}
