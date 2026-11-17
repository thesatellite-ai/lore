package wire

// repoonly.go — the reads whose data lives in each code repo, and their
// one refusal outside it (devco D-508, D-509).
//
//	App.run ─▶ repoOnlyRefusal(name): a listed read, here no code repo
//	        ─▶ errRepoOnly: what lives in each code repo, cd into it,
//	           --store does not apply
//
// Why: these reads have no store to name. Before, from the home folder
// each refused in its own words (git's "cd into a git checkout; a write can
// also name its store…", the store-only open's "this command needs a
// repo…", a store clone's "run it in a code repo; inside a store
// folder…"), and none said that --store does not apply, so a person tried
// --store and --setup next. One list, one refusal: a read added to the
// list gets the refusal everywhere at once.

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opssession "github.com/thesatellite-ai/lore/ops/session"
	opswhycontext "github.com/thesatellite-ai/lore/ops/whycontext"
)

// repoOnlyRead is one read whose data lives in each code repo.
type repoOnlyRead struct {
	// what names the data and where it lives, as the subject of the
	// refusal's "… in each code repo": "missions, claims and the inbox
	// live", "the migration journal lives".
	what string
	// noun completes the hint's "cd into the code repo whose <noun> you
	// want".
	noun string
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
}

// repoOnlyReads is every read that answers only inside a code repo, by
// operation name. Outside one (the home folder, a plain folder) each
// refuses with errRepoOnly before anything opens.
//
// migrate.status refuses in a store clone too (devco D-509 decision): it
// reports a code repo's move from lore 0.1.17 data to row files, which a
// store clone never had; a store's own layout is `lore store doctor`'s.
var repoOnlyReads = map[string]repoOnlyRead{
	opscoord.OpInbox:         coordRepoOnlyRead,
	opscoord.OpMissionStatus: coordRepoOnlyRead,
	opscoord.OpTaskNext:      coordRepoOnlyRead,
	opsmigrate.OpStatus: {
		what:    "the migration journal (a repo's move from lore 0.1.17 data to row files, in its .lore/) lives",
		noun:    "migration",
		also:    "a store's own layout is checked by `lore store doctor`",
		inStore: true,
	},
	opssession.OpList: {
		what: "the agent sessions of a repo (in its .lore/) live",
		noun: "sessions",
	},
	opswhycontext.OpShow: {
		what:        "the last rendered context (what the agent saw, in the repo's .lore/) lives",
		noun:        "context",
		atStoreRoot: true,
	},
}

// coordRepoOnlyRead is the entry of mission status, the inbox and task
// next (devco D-508): claims and the inbox live on the repo's remote.
var coordRepoOnlyRead = repoOnlyRead{
	what: "missions, claims and the inbox live",
	noun: "missions or inbox",
	also: "`lore task list --store <org>` lists an org's tasks from anywhere",
}

// repoOnlyRefusal is the refusal of the read name run in dir, or nil when
// it is no repo-only read or dir is inside a code repo (or a store clone
// the read answers in).
func repoOnlyRefusal(ctx context.Context, name, dir string) error {
	r, ok := repoOnlyReads[name]
	if !ok {
		return nil
	}
	if sc, in := storeHere(dir); in {
		if !r.inStore && !(r.atStoreRoot && sc.folder == rowfs.FolderRoot) {
			return nil
		}
		return errRepoOnly(r, sc.shown+" is a lore store clone, not a code repo")
	}
	if insideWorkTree(ctx, dir) {
		return nil
	}
	where := "this folder"
	if dir != "" {
		where = dir
	}
	return errRepoOnly(r, where+" is not inside a code repo")
}

// errRepoOnly is the one refusal of a repo-only read: what lives in each
// code repo, why here is not one, and the way out (cd into the repo);
// --store does not apply, so the hint never names it as a way to read it.
func errRepoOnly(r repoOnlyRead, why string) error {
	hint := "cd into the code repo whose " + r.noun + " you want and run it there; --store does not apply (this read has no store rows)"
	if r.also != "" {
		hint += "; " + r.also
	}
	return errs.New(errs.Unsupported, r.what+" in each code repo: "+why).WithHint(hint)
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
