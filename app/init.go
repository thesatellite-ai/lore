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
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
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
// already set up on this machine: the core's repo cache, and v0.1.17's
// lore.db and its Mode-B pointers (lore.yaml, the legacy lore.toml). All are
// gitignored, so a fresh clone carries none of them.
var localStateFiles = []string{filepath.Base(wire.RepoCacheFile), "lore.db", "lore.yaml", "lore.toml"}

// hasLocalState reports whether any localStateFiles entry exists in
// wd/.lore. It must be read before the app opens the repo store, which
// creates the cache.
func hasLocalState(wd string) bool {
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
func initScope(o *wire.Options) {
	o.OrgStores = nil
	o.OrgEntryStore = nil
	o.DefaultTarget = nil
}

// initCommand is `lore init [<path>] [--name] [--target] [--non-interactive]`;
// run has already taken <path> off the arguments (initpath.go).
func (h *host) initCommand(wd string) *cobra.Command {
	var in repoinit.InitIn
	// Read now, before any command opens the store (and creates the cache).
	ih := h.initHost(wd, hasLocalState(wd))
	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Initialize a lore project in this repository (project row and repo id)",
		Example: "  # set up the repo you are in (points AGENTS.md, or the tool files found, at .lore/LORE.md)\n" +
			"  lore init\n" +
			"  # a monorepo package as its own project, wired for Claude Code\n" +
			"  lore init services/billing --target claude_code",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 { // run takes the path off first; reaching here means it could not
				return errs.New(errs.InvalidInput, "init: run lore init "+args[0]+" as the whole command")
			}
			out, err := repoinit.Run(cmd.Context(), ih, in)
			if err != nil {
				return err
			}
			_, _ = io.WriteString(cmd.ErrOrStderr(), out.WarningLines()) // stderr gone: nothing left to report to
			_, err = io.WriteString(cmd.OutOrStdout(), out.Body())
			return err
		},
	}
	cmd.Flags().StringVar(&in.Name, initFlagName, "", "project name (default: the repo name of origin's URL, else the folder name)")
	cmd.Flags().StringSliceVar(&in.Target, flagTarget, nil, "AI tool profile(s) or file(s) to point at .lore/LORE.md (default: the tools of the setup that applies here, else tool files found in the repo, else AGENTS.md)")
	cmd.Flags().BoolVar(&in.NonInteractive, flagNonInteractive, false, "accepted for v0.1.17 scripts; init never asks")
	return cmd
}

// initHost is repoinit.Host over the CLI's host: the app for the repo
// store, the live rows for the first fetch, the render machinery for the
// tool files, and the helpers of initsub.go / initname.go for the checkout.
type initHost struct {
	h     *host
	wd    string
	local bool
}

// initHost is the repoinit.Host for wd; local is hasLocalState(wd) read at
// process start. The registry's repo.init uses the same value through
// wire (RepoInitHost), so `lore repo init` and `lore init` agree.
func (h *host) initHost(wd string, local bool) *initHost {
	return &initHost{h: h, wd: wd, local: local}
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

// DefaultName implements repoinit.Host (initname.go).
func (i *initHost) DefaultName(ctx context.Context) string { return defaultProjectName(ctx, i.wd) }

// OriginURL implements repoinit.Host (initname.go).
func (i *initHost) OriginURL(ctx context.Context) string { return projectOriginURL(ctx, i.wd) }

// CreateProject implements repoinit.Host: the projects row in the repo store.
func (i *initHost) CreateProject(ctx context.Context, fields map[string]value.Value) (string, ids.ID[ids.Project], error) {
	w, err := i.h.app.Create(ctx, store.SourceRepo, schema.TableProjects, fields)
	if err != nil {
		return "", ids.ID[ids.Project]{}, err
	}
	pid, err := ids.As[ids.Project](w.Row.ID)
	if err != nil {
		return "", ids.ID[ids.Project]{}, err
	}
	return w.Row.ID.String(), pid, nil
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

// Prime implements repoinit.Host.
func (i *initHost) Prime(ctx context.Context) {
	if i.h.live != nil {
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
