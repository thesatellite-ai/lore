package wire

// repoonly.go — the operations whose data lives in each code repo, and
// their one refusal outside it (devco D-508, D-509; writes D-523).
//
//	App.run ─▶ repoOnlyRefusal(name): a listed op, here no code repo
//	        ─▶ errRepoOnly: what lives in each code repo, cd into it,
//	           --store does not apply
//
// Why: these reads have no store to name. Before, from the home folder
// each refused in its own words (git's "cd into a git checkout; a write can
// also name its store…", the store-only open's "this command needs a
// repo…", a store clone's "run it in a code repo; inside a store
// folder…"), and none said that --store does not apply, so a person tried
// --store and --setup next. One list, one refusal: an op added to the
// list gets the refusal everywhere at once.
//
// The writes that change a code repo's own files (its stores.yaml, its
// org link, its CLAUDE.md directive, its CI workflow, its 0.1.17 data's
// migration, lore fix's repair of the repo) joined the list (devco
// D-523): from the home folder and in a store clone they refused with
// hints of their own ("cd into the code repo that should attach the
// store", "lore init sets this repo up", "not inside a git repo here" in a
// store clone that is one), none the repo-only class.

import (
	"context"
	"github.com/thesatellite-ai/lore/op"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opsdirective "github.com/thesatellite-ai/lore/ops/directive"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opssession "github.com/thesatellite-ai/lore/ops/session"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	opswhycontext "github.com/thesatellite-ai/lore/ops/whycontext"
)

// repoOnlyOp is one operation whose data lives in each code repo.
type repoOnlyOp struct {
	// what names the data and where it lives, as the subject of the
	// refusal's "… in each code repo": "missions, claims and the inbox
	// live", "the migration journal lives".
	what string
	// noun completes the hint's "cd into the code repo whose <noun> you
	// want" (a read).
	noun string
	// cd, when set, replaces "whose <noun> you want": the repo a write
	// changes ("that should attach the store").
	cd string
	// reason is why --store does not apply ("" for a read's "this read has
	// no store rows").
	reason string
	// also is an extra way out the hint names after the shared one ("" for
	// none), e.g. the store read that lists an org's tasks from anywhere.
	also string
	// inStore refuses in a store clone too. A read left false there keeps
	// its own path: the coordination reads say a store clone is no code
	// repo themselves (errCoordInStore); session list and why-context
	// answer from this machine's files.
	inStore bool
	// atStoreRoot: refuse only at a store clone's top (outside every
	// folder): a store folder answers it (its own render), the root is no
	// project's folder and holds no render of its own (D-509, matrix).
	atStoreRoot bool
	// outsideWorks leaves the folder outside every repo and store clone to
	// the op's own path: it does its work there by design (store set-url
	// --name <org> from the home folder moves that org's clone, D-514) or
	// answers there in its own words; only a store clone refuses it.
	outsideWorks bool
}

// repoOnlyOps is every operation that works only inside a code repo, by
// operation name. Outside one (the home folder, a plain folder) each
// refuses with errRepoOnly before anything opens.
var repoOnlyOps = map[string]repoOnlyOp{
	opscoord.OpInbox:         coordRepoOnlyRead,
	opscoord.OpMissionStatus: coordRepoOnlyRead,
	opscoord.OpTaskNext:      coordRepoOnlyRead,
	opsmigrate.OpStatus:      migrateRepoOnly,
	opsmigrate.OpRun:         migrateRepoOnly,
	opsmigrate.OpRollback:    migrateRepoOnly,
	opsmigrate.OpUnlock:      migrateRepoOnly,
	opssession.OpList: {
		what: "the agent sessions of a repo (in its .lore/) live",
		noun: "sessions",
	},
	opswhycontext.OpShow: {
		what:        "the last rendered context (what the agent saw, in the repo's .lore/) lives",
		noun:        "context",
		atStoreRoot: true,
	},
	// the writes that change a code repo's own files (devco D-523)
	// store add, init, remove and set-url refuse through their own gate
	// (refuseStoresFileOutsideRepo: init --name me and set-url of an org's
	// store work outside a repo) with storesYAMLRepoOnly
	opsorgcmd.OpLink: {
		what:   "a repo's link to a folder of its org's store (in its .lore/) lives",
		cd:     "that should link to the org's folder",
		reason: "it writes that repo's own link",
	},
	opsfix.OpRun: {
		what:    "the setup lore fix repairs (a repo's .lore/, hooks and agent files) lives",
		cd:      "you want repaired",
		reason:  "it repairs that repo's own files",
		also:    "a store's own layout is checked by `lore store doctor`",
		inStore: true,
	},
	opsdirective.OpInstall: {
		what:         "the lore directive (a block in a repo's agent file, CLAUDE.md or AGENTS.md) lives",
		cd:           "whose agent file should carry it",
		reason:       "it edits that repo's agent file",
		inStore:      true,
		outsideWorks: true,
	},
	opssync.OpInstallAction: {
		what:    "the sync workflow (.github/workflows of a repo) lives",
		cd:      "whose CI should run lore sync",
		reason:  "it writes that repo's workflow file",
		inStore: true,
	},
}

// migrateRepoOnly is the entry of migrate status, run, rollback and
// unlock (D-509, D-523): a store clone never held lore 0.1.17 data; a
// store's own layout is `lore store doctor`'s.
var migrateRepoOnly = repoOnlyOp{
	what:    "the migration journal (a repo's move from lore 0.1.17 data to row files, in its .lore/) lives",
	noun:    "migration",
	also:    "a store's own layout is checked by `lore store doctor`",
	inStore: true,
}

// storesYAMLRepoOnly is the refusal of store add, init, remove and set-url
// (D-523) where they edit no repo's stores.yaml: each edits the
// stores.yaml of the repo that attaches the store. Their own gate
// (refuseStoresFileOutsideRepo) raises it, not repoOnlyOps: init --name
// me and set-url of an org's store do their work outside a repo.
var storesYAMLRepoOnly = repoOnlyOp{
	what:    "the stores a repo attaches (a store command edits a repo's .lore/stores.yaml) live",
	cd:      "that should attach (or drop) the store",
	reason:  "it edits that repo's stores.yaml",
	also:    "`lore store list` shows every store this machine knows from anywhere",
	inStore: true,
}

// coordRepoOnlyRead is the entry of mission status, the inbox and task
// next (devco D-508): claims and the inbox live on the repo's remote.
var coordRepoOnlyRead = repoOnlyOp{
	what: "missions, claims and the inbox live",
	noun: "missions or inbox",
	also: "`lore task list --store <org>` lists an org's tasks from anywhere",
}

// repoOnlyRefusal is the refusal of the read name run in dir, or nil when
// it is no repo-only read or dir is inside a code repo (or a store clone
// the read answers in).
func repoOnlyRefusal(ctx context.Context, name, dir string) error {
	r, ok := repoOnlyOps[name]
	if !ok {
		return nil
	}
	if sc, in := storeHere(dir); in {
		if !r.inStore && !(r.atStoreRoot && sc.folder == rowfs.FolderRoot) {
			return nil
		}
		return errRepoOnly(r, sc.shown+" is a lore store clone, not a code repo")
	}
	if r.outsideWorks || insideWorkTree(ctx, dir) {
		return nil
	}
	where := "this folder"
	if dir != "" {
		where = dir
	}
	return errRepoOnly(r, where+" is not inside a code repo")
}

// errRepoOnly is the one refusal of a repo-only op: what lives in each
// code repo, why here is not one, and the way out (cd into the repo);
// --store does not apply, so the hint never names it as a way to reach it.
func errRepoOnly(r repoOnlyOp, why string) error {
	return errRepoOnlyAs(errs.Unsupported, r, why)
}

// errRepoOnlyAs is errRepoOnly with code: the stores.yaml writes keep
// E_NOT_A_STORE (exit 19), the code their refusal always had and scripts
// key on, with the class's words (devco D-523).
func errRepoOnlyAs(code errs.Code, r repoOnlyOp, why string) error {
	cd, reason := "whose "+r.noun+" you want", "this read has no store rows"
	if r.cd != "" {
		cd = r.cd
	}
	if r.reason != "" {
		reason = r.reason
	}
	hint := "cd into the code repo " + cd + " and run it there; --store does not apply (" + reason + ")"
	if r.also != "" {
		hint += "; " + r.also
	}
	return errs.New(code, r.what+" in each code repo: "+why).WithHint(hint)
}

// errCoordNoRepo is the refusal of coordination outside a code repo
// (devco D-508): the shared repo-only refusal with the coordination
// wording. The coordination world's open names it too.
func errCoordNoRepo(dir string) error {
	where := "this folder"
	if dir != "" {
		where = dir
	}
	return errRepoOnly(coordRepoOnlyRead, where+" is not inside a code repo")
}

// fleetFix reports a `lore fix --all` (or --all --dirs …) run: it walks a
// folder of repos and the setups' repos from wherever it starts (the home
// folder, a folder holding code repos), so it is not a one-repo command
// and the repo-only refusal never applies to it (devco D-528: D-523's
// refusal had made `lore fix --all` from a folder of repos exit 23).
func fleetFix(e op.Entry, d op.Decoder) bool {
	if e.Spec().Name != opsfix.OpRun {
		return false
	}
	in, err := e.Decode(d)
	if err != nil {
		return false
	}
	if all, ok := in.Bool("all"); ok && all {
		return true
	}
	return len(in.Positionals()) > 0 // --dirs (with --all) folders
}
