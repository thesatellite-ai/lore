package app

// init.go — `lore init` of the standalone binary: a thin caller of the
// registry operation repo.init (ops/repoinit, ONBOARDING_DESIGN 4.10 and
// conflict 10.2), so the CLI word and the setup page take one path. The
// decisions (refuse, adopt, mint) live in repoinit.Run; this file supplies
// its Host from the app, the live rows and the render machinery, and
// prints the result on stdout and the warnings on stderr, as before.
//
// The v0.1.17 parts that still live in the old binary (lore.db, git merge
// driver and hooks, the first lsync pass) are listed in the old-code
// removal plan as parity gaps (FA-OLDCODE-2).

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/repoinit"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// Flags of `lore init` (v0.1.17 names). flagNonInteractive is accepted
// for v0.1.17 scripts and changes nothing: init never asks (CLI_REFERENCE
// 2.14, Q-R2-4, P3-CMDLORE-1).
const (
	initFlagName       = "name"
	flagTarget         = "target"
	flagNonInteractive = "non-interactive"
)

// localStateFiles are the files under .lore/ that mark a checkout as
// already set up on this machine: an earlier build's repo cache, and
// v0.1.17's lore.db and its Mode-B pointers (lore.yaml, the legacy
// lore.toml). All are gitignored, so a fresh clone carries none of them.
var localStateFiles = []string{filepath.Base(wire.RepoCacheFile), "lore.db", "lore.yaml", "lore.toml"}

// hasLocalState reports whether the checkout at wd has a repo cache under
// home (wire.RepoCachePath) or any localStateFiles entry in wd/.lore. It
// must be read before the app opens the repo store, which creates the
// cache.
func hasLocalState(home, wd string) bool {
	if home != "" {
		if _, err := os.Lstat(wire.RepoCachePath(home, wd)); err == nil {
			return true
		}
	}
	for _, f := range localStateFiles {
		if _, err := os.Lstat(filepath.Join(wd, rowfs.LoreDir, f)); err == nil {
			return true
		}
	}
	return false
}

// initScope narrows o to what `lore init` needs, as v0.1.17's
// initCoreOptions did: the org (so the rows init writes carry the person)
// and the repo-write guard stay; the org's stores, its entry stores and
// the default target go. Init writes only the repo store, and the org's
// stores may have no local copy yet or be broken: a code repo listed as a
// store is refused by every later command (E_NOT_A_STORE, F12), but it
// must not stop the repo from being set up.
//
// The client-repo role stays (G-ORGMIG-8): without it the repo store is a
// client by default (fail closed), and in a repo with no org a detected
// agent's id was then projected to its owner, which init's own minimal
// actor row does not name, so the repos and context.targets rows of the
// same init were written by nobody. The role check reads the org's links
// and fails closed to client on a broken store, which never stops init.
//
// One exception (H-1): in a repo linked as a client, the rows init writes
// into the repo get the F16 leak check, which reads the linked folder; with
// the org's stores dropped it always refused ("cannot check: store
// unreachable and nothing cached"), so init could never run there. A
// client repo keeps the org's stores; any other repo still opens none.
// The role is asked lazily, when the app first opens its stores (after
// init has rooted the project), never before.
func initScope(o *wire.Options) {
	stores, isClient := o.OrgStores, o.RepoIsClient
	o.OrgStores = nil
	if stores != nil && isClient != nil {
		o.OrgStores = func(ctx context.Context) ([]attach.Store, error) {
			if client, err := isClient(ctx); err != nil || !client {
				return nil, nil // not a client (or unknown: the guard fails closed on its own)
			}
			return stores(ctx)
		}
	}
	o.OrgEntryStore = nil
	o.DefaultTarget = nil
}

// initCommand is `lore init [--path <path>] [--name] [--target] [--non-interactive]`;
// run has already taken <path> off the arguments (initpath.go).
func (h *host) initCommand(wd string) *cobra.Command {
	var in repoinit.InitIn
	// Read now, before any command opens the store (and creates the cache).
	ih := h.initHost(wd, hasLocalState(h.home, wd))
	cmd := &cobra.Command{
		Use:   "init [--" + initPathFlag + " <path>]",
		Short: "Initialize a lore project in this repository (project row and repo id)",
		Example: "  # set up the repo you are in (points AGENTS.md, or the tool files found, at .lore/LORE.md)\n" +
			"  lore init\n" +
			"  # a monorepo package as its own project, wired for Claude Code\n" +
			"  lore init --path services/billing --target claude_code",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 { // run takes the path off first; reaching here means it could not
				return errs.New(errs.InvalidInput, "init: run lore init --path "+args[0]+" as the whole command")
			}
			// the rows init writes are named like any write's (wire/host
			// writeloc.go: lore repo init runs through that hook)
			ctx, written := wire.WithWriteLog(cmd.Context())
			out, err := repoinit.Run(ctx, ih, in)
			if err != nil {
				return err
			}
			if os.Getenv(hosthooks.EnvSyncQuiet) != "1" {
				for _, l := range wire.WrittenLines(written.Files()) {
					_, _ = io.WriteString(cmd.ErrOrStderr(), l+"\n") // stderr gone: nothing left to report to
				}
			}
			_, _ = io.WriteString(cmd.ErrOrStderr(), out.WarningLines()) // stderr gone: nothing left to report to
			_, err = io.WriteString(cmd.OutOrStdout(), out.Body())
			return err
		},
	}
	// --path is read before the command runs (initPathArg); declared here
	// so --help lists it.
	_ = cmd.Flags().String(initPathFlag, "", "the folder to set up (default: the one you are in)")
	cmd.Flags().StringVar(&in.Name, initFlagName, "", "project name (default: the repo name of origin's URL, else the folder name)")
	cmd.Flags().StringSliceVar(&in.Target, flagTarget, nil, "AI tool profile(s) or file(s) to point at .lore/LORE.md (default: the tools of the setup that applies here, else tool files found in the repo, else AGENTS.md)")
	cmd.Flags().BoolVar(&in.NonInteractive, flagNonInteractive, false, "accepted for v0.1.17 scripts; init never asks")
	_ = cmd.Flags().MarkHidden(flagNonInteractive) // parsed for old scripts, never offered in --help (B3-17)
	return cmd
}

// initHost is repoinit.Host over the CLI's host: the app for the repo
// store, the live rows for the first fetch, the render machinery for the
// tool files, and the helpers of initsub.go / initname.go for the checkout.
type initHost struct {
	h     *host
	wd    string
	local bool
	// hadLoreData and atTop are read at process start (initHost): wd held
	// row files (.lore/data), and wd is its git checkout's top folder.
	hadLoreData, atTop bool
}

// initHost is the repoinit.Host for wd; local is hasLocalState(wd) read at
// process start. The registry's repo.init uses the same value through
// wire (RepoInitHost), so `lore repo init` and `lore init` agree.
func (h *host) initHost(wd string, local bool) *initHost {
	return &initHost{h: h, wd: wd, local: local, hadLoreData: wire.HoldsLoreData(wd), atTop: isGitTop(wd)}
}

// WorkDir implements repoinit.Host.
func (i *initHost) WorkDir() string { return i.wd }

// HadLocalState implements repoinit.Host.
func (i *initHost) HadLocalState() bool { return i.local }

// SharedRepoMeta implements repoinit.Host (initsub.go).
func (i *initHost) SharedRepoMeta(ctx context.Context) (rowfs.RepoMeta, bool, error) {
	return sharedRepoMeta(ctx, i.wd)
}

// OwnRepoMeta implements repoinit.Host.
func (i *initHost) OwnRepoMeta(_ context.Context) bool {
	_, own, _ := rowfs.ReadRepoMeta(i.wd) // unreadable counts as absent, as before the move
	return own
}

// RootAt implements repoinit.Host (initsub.go, OWNERQ-1).
func (i *initHost) RootAt(_ context.Context) (func(), error) { return rootInitAt(i.wd) }

// Project implements repoinit.Host.
func (i *initHost) Project(ctx context.Context) (ids.ID[ids.Project], error) {
	return i.h.app.Project(ctx)
}

// OtherProjectFolders implements repoinit's optional monorepo host: the
// folders (relative to the repo top, "." for the top) of the checkout's
// other lore projects, sorted; none when it cannot tell.
func (i *initHost) OtherProjectFolders(ctx context.Context) []string {
	metas, err := checkoutRepoMetas(ctx, i.wd)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(metas))
	for rel := range metas {
		out = append(out, rel)
	}
	slices.Sort(out)
	return out
}

// DefaultName implements repoinit.Host (initname.go).
func (i *initHost) DefaultName(ctx context.Context) string { return defaultProjectName(ctx, i.wd) }

// OriginURL implements repoinit.Host (initname.go).
func (i *initHost) OriginURL(ctx context.Context) string { return projectOriginURL(ctx, i.wd) }

// CreateProject implements repoinit.Host: the projects row in the repo store.
func (i *initHost) CreateProject(ctx context.Context, id ids.ID[ids.Project], fields map[string]value.Value) (string, ids.ID[ids.Project], error) {
	var w engine.Written
	var err error
	if id.IsZero() {
		w, err = i.h.app.Create(ctx, store.SourceRepo, schema.TableProjects, fields)
	} else {
		w, err = i.h.app.CreateWithID(ctx, store.SourceRepo, schema.TableProjects, id.Any(), fields)
	}
	if err != nil {
		return "", ids.ID[ids.Project]{}, err
	}
	pid, err := ids.As[ids.Project](w.Row.ID)
	if err != nil {
		return "", ids.ID[ids.Project]{}, err
	}
	return w.Row.ID.String(), pid, nil
}

// LinkedProject implements repoinit.Host (wire.App.LinkedRemoteProject):
// only for the repo's top folder, and only when it held no row files
// (.lore/data) when the process started: a link by remote names a repo
// with no lore data; a monorepo subfolder is its own project. A .lore/
// holding only machine-local leftovers (lock, state/, an old render: what
// a copied or re-cloned checkout keeps, ignored by git) still counts as
// no lore data (B1-18 round 2: such a leftover made init mint a new id).
func (i *initHost) LinkedProject(ctx context.Context) (ids.ID[ids.Project], string, bool) {
	if i.hadLoreData || !i.atTop {
		return ids.ID[ids.Project]{}, "", false
	}
	return i.h.app.LinkedRemoteProject(ctx)
}

// SeedRepoMeta implements repoinit.Host.
func (i *initHost) SeedRepoMeta(ctx context.Context, pid ids.ID[ids.Project]) error {
	return i.h.app.SeedRepoMeta(ctx, pid)
}

// InheritRepoID implements repoinit.Host (initsub.go).
func (i *initHost) InheritRepoID(ctx context.Context, shared rowfs.RepoMeta) error {
	return i.h.inheritRepoID(ctx, shared)
}

// EnsureRepoIdentity implements repoinit.Host.
func (i *initHost) EnsureRepoIdentity(ctx context.Context) (string, error) {
	id, _, err := i.h.app.EnsureRepoIdentity(ctx)
	return id, err
}

// EnsureGitignore implements repoinit.Host: lore's local files in wd's
// .gitignore, as v0.1.17's init did.
func (i *initHost) EnsureGitignore(ctx context.Context) error {
	return hosthooks.EnsureGitignore(ctx, i.wd)
}

// initCommandName is the name `lore init`'s lines carry.
const initCommandName = "lore init"

// Prime implements repoinit.Host.
func (i *initHost) Prime(ctx context.Context) {
	if i.h.live != nil {
		if wire.CommandName(ctx) == wire.CommandName(context.Background()) {
			// `lore init` runs outside the registry: its set-up lines say
			// "lore init:" (wire/cmdname.go), as `lore repo init`'s say theirs
			ctx = wire.WithCommandName(ctx, initCommandName)
		}
		i.h.live.Prime(ctx)
	}
}

// WireToolFiles implements repoinit.Host (TOOL NEUTRAL,
// renderhost.WireToolFiles): no render machinery, nothing to wire.
func (i *initHost) WireToolFiles(ctx context.Context, targets []string) (string, error) {
	if i.h.render == nil {
		return "", nil
	}
	var lines strings.Builder
	err := i.h.render.WireToolFiles(ctx, &lines, i.wd, targets)
	return lines.String(), err
}

// SetupTools implements repoinit.Host: the applying setup's tools
// (wire.App.SetupInitTools).
func (i *initHost) SetupTools(ctx context.Context) repoinit.SetupTools {
	if i.h.app == nil {
		return repoinit.SetupTools{}
	}
	return i.h.app.SetupInitTools(ctx)
}

// repoInitWords is `lore repo init`, the registry's own word for repo.init.
var repoInitWords = [2]string{repoinit.Noun, initCmdName}

// isInitRun reports whether args run init, as `lore init` or `lore repo
// init`: run narrows the app's options for both (initScope), so the
// registry word opens no org store either.
func isInitRun(args []string) bool {
	var words []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	switch {
	case len(words) == 0:
		return false
	case words[0] == initCmdName:
		return true
	default:
		return len(words) > 1 && words[0] == repoInitWords[0] && words[1] == repoInitWords[1]
	}
}

// lateInit is wire.Options.Context.RepoInit (repoinit.Env): main builds the
// options before the host exists, then sets ih once it does.
type lateInit struct{ ih *initHost }

// RepoInitHost implements repoinit.Env.
func (l *lateInit) RepoInitHost(_ context.Context) (repoinit.Host, error) {
	if l.ih == nil {
		return nil, errs.New(errs.Unsupported, "repo init: the command host is not built yet")
	}
	return l.ih, nil
}

// isGitTop reports whether wd is the top folder of a git checkout (it
// holds .git, a folder or a worktree's file).
func isGitTop(wd string) bool {
	_, err := os.Lstat(filepath.Join(wd, ".git"))
	return err == nil
}
