package wire

// rosterstore.go — a read of the org roster names its org from any folder
// (devco D-499). The owner asked "how do I list all the teams": from the
// home folder (no repo, no store clone, no setup's folders) `team list`,
// `team show` and `member list` refused --store ("unknown flag") and with
// no flag failed E_NOT_A_STORE with a git-init hint, while `rule list
// --store <org>` read the org from anywhere (orgread.go, D-471). Every
// read of the roster now takes --store <org> (metalist.RosterStore), and
// the org it names decides the roster as an org read does (namesOrgRead):
//
//	direct roster read ─▶ --store <org> ─▶ rosterPin in ctx ─▶ repoRoster: the org's roster
//	                                                       └▶ rosterApp: a store-only App on it
//	                   ─▶ --store repo ─▶ the usual path (this repo's roster)
//	                   ─▶ --store <not an org> ─▶ E_NOT_FOUND naming the orgs
//	                   ─▶ no flag, no org here, no repo ─▶ E_NOT_A_STORE naming
//	                                                       --store <org> and --setup <setup>
//
// The pin rides on ctx, not on the App: the roster ops call back into the
// App that serves the command (lore serve serves many), and the org a
// read names holds for that read only (D-477).

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/store"
)

// rosterPinKey carries the rosterPin of a read that named its org.
type rosterPinKey struct{}

// rosterPin is the org roster a read named with --store <org>: the roster
// as repoRoster answers it, and the org folder store rosterApp opens alone.
type rosterPin struct {
	at  rosterAt
	one attach.Store
}

// pinnedRoster is the roster ctx's read named, if any.
func pinnedRoster(ctx context.Context) (rosterPin, bool) {
	p, ok := ctx.Value(rosterPinKey{}).(rosterPin)
	return p, ok
}

// rosterRead reports whether spec is a direct roster op that only reads:
// the class --store <org> applies to (writes keep their setup's org).
func rosterRead(spec op.Spec) bool {
	return profilestore.IsDirect(spec.Name) && spec.OnlyReads()
}

// prepareRoster is ctx for the direct roster op e: pinned to the org a
// read's --store names, as it was otherwise. Outside every repo and store
// clone with no org for this folder it refuses with the ways to name one
// (rosterNowhereErr), never the repo open's "run git init here first".
func (a *App) prepareRoster(ctx context.Context, e op.Entry, d op.Decoder) (context.Context, error) {
	spec := e.Spec()
	if !profilestore.IsDirect(spec.Name) {
		return ctx, nil
	}
	read := rosterRead(spec)
	name := ""
	if read {
		in, err := e.Decode(d)
		if err != nil {
			return ctx, nil // runDirect reports the decode error
		}
		name = scopeOf(in).store
	}
	if name == "" {
		return ctx, a.rosterHere(ctx, read)
	}
	base, _, _ := strings.Cut(name, targetSep)
	if base == string(store.SourceRepo) {
		return ctx, nil // this repo's own roster: the usual path
	}
	if !a.namesOrgRead(ctx, e, d) {
		return ctx, a.rosterNoOrgErr(name)
	}
	p, err := a.rosterPinFor(ctx, base)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, rosterPinKey{}, p), nil
}

// rosterPinFor is the roster of the org named base: the org folder store
// of its entry in the home's org config, else the one the org hook brings
// for this folder (orgRosterSource). An org with neither has no roster
// lore can reach, refused as repoRoster refuses it.
func (a *App) rosterPinFor(ctx context.Context, base string) (rosterPin, error) {
	if cfg, _, err := orgcfg.LoadConfig(a.opts.Home); err == nil && cfg != nil {
		for i := range cfg.Orgs {
			org := &cfg.Orgs[i]
			if org.Name != base {
				continue
			}
			if ofs := org.OrgFolderStore(); ofs != nil {
				name := store.Source(base + targetSep + ofs.OrgFolder)
				at := newRosterAt(name, base+" "+ofs.OrgFolder+"/")
				at.org = base
				return rosterPin{at: at, one: attach.Store{Name: name, URL: ofs.URL, Folder: ofs.OrgFolder, Branch: attachBranch(ofs)}}, nil
			}
		}
	}
	src, folder, ok, err := a.orgRosterSource(ctx, base)
	if err != nil {
		return rosterPin{}, err
	}
	if ok {
		one, oerr := orgStoreNamed(ctx, a.opts, src)
		if oerr != nil {
			return rosterPin{}, oerr
		}
		at := newRosterAt(src, base+" "+folder+"/")
		at.org = base
		return rosterPin{at: at, one: one}, nil
	}
	return rosterPin{}, errs.New(errs.NotFound, "org "+base+" keeps no roster this machine can reach: none of its stores has an org folder").
		WithHint("set org_folder on one of the org's stores in " + lorehome.Shown() + "/config.yaml")
}

// rosterHere refuses a roster op that has no roster to work on: no org
// for this folder, and no repo or store clone here whose own roster it
// could be. Anywhere else it is nil (the usual path decides).
func (a *App) rosterHere(ctx context.Context, read bool) error {
	if insideWorkTree(ctx, a.opts.RepoDir) || inStore(a.opts.RepoDir) {
		return nil
	}
	r, err := a.repoRoster(ctx)
	if err != nil || r.src != store.SourceRepo {
		return nil // an org's roster, or the resolution's own refusal
	}
	return a.rosterNowhereErr(read)
}

// rosterNowhereErr is E_NOT_A_STORE (exit 19, as the repo open's refusal
// was: scripts key on it) for a roster op run where no org applies: the
// hint names --store <org> (a read) and --setup <setup>, with the orgs and
// the setups this machine has; a write's names --setup and says --store is
// the reads' (D-523).
func (a *App) rosterNowhereErr(read bool) error {
	orgs, setups := a.knownOrgs(), a.orgSetups()
	var ways []string
	if read {
		ways = append(ways, "name the org with --store <org>"+knownList("orgs", orgs))
		ways = append(ways, "name a setup with --setup <setup>"+knownList("setups", setups))
	} else {
		// devco D-523: a change of the roster writes through the setup
		// that holds the org (its identity signs the commit); --store
		// <org> names the org for the roster reads only, said so here so
		// a person who read `team list --store <org>` does not try it on
		// a change next
		ways = append(ways, "name the setup of the org with --setup <setup>"+knownList("setups", setups)+" (--store <org> names the org for the roster reads, `lore team list --store <org>`, not for a change)"+knownList("orgs", orgs))
	}
	ways = append(ways, "or cd into a code repo or a store clone of the org")
	verb := "read the teams and people from"
	if !read {
		verb = "change"
	}
	return errs.New(errs.NotAStore, "no org applies to this folder (it is no code repo, store clone or setup's folder), so there is no roster to "+verb).
		WithHint(strings.Join(ways, "; "))
}

// rosterNoOrgErr refuses --store name on a roster read when name is no
// org here: the teams and people live only in an org's roster.
func (a *App) rosterNoOrgErr(name string) error {
	return errs.New(errs.NotFound, "--store "+name+" names no org here, and teams and people are kept in an org's roster").
		WithHint("name an org with --store <org>" + knownList("orgs", a.knownOrgs()) + ", or --store repo for this repo's own roster")
}

// knownOrgs is the names of the orgs in the home's org config, sorted.
func (a *App) knownOrgs() []string {
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		return nil
	}
	var out []string
	for i := range cfg.Orgs {
		if n := strings.TrimSpace(cfg.Orgs[i].Name); n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// orgSetups is the names of the enabled setups that work in an org (a solo
// setup has no roster), sorted.
func (a *App) orgSetups() []string {
	cfg, err := ReadSetupsConfig(a.opts.Home)
	if err != nil {
		return nil
	}
	var out []string
	for _, s := range cfg.Setups {
		if s.Disabled || strings.TrimSpace(s.Org) == "" {
			continue
		}
		out = append(out, s.Name)
	}
	slices.Sort(out)
	return out
}

// knownList is " (<what>: a, b)" for a hint, "" when names is empty.
func knownList(what string, names []string) string {
	if len(names) == 0 {
		return ""
	}
	return " (" + what + ": " + strings.Join(names, ", ") + ")"
}
