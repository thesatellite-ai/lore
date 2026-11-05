// coord_wire.go — the executor half of agent coordination (LORE_TEAM_SPEC
// F7, F9, §7): it gathers what the pure packages need from the machine — the
// git repo and its remote, the team config (lease, agents, live.autopush,
// live.remote), the caller's identity and session, the clock — and maps
// their errors to the CLI's error codes and exit statuses.
//
// Every coordination command (task claim/next/heartbeat/release/revoke/
// reassign/done on a claimed task, resource claim/release, gate approve)
// goes through openCoordWorld, so they all agree on who the caller is, which
// signals ref they write and whether the network may be used.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"saas/pkg/aicoder/clock"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/gitremote"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
)

// Coordination reads live_wire.go's envLiveAutopush ("0" = a coordination
// write fails closed, rule 11) and envLiveRemote.

// Machine-state files under <git dir>/lore/ (§1 "Files and folders").
const (
	seenFileName    = "signals-seen.json"
	heldFileName    = "held.json"
	humanSessionSep = "@"
	rootFolderName  = "_root"
)

// coordWorld is one command's view of the machine for coordination.
type coordWorld struct {
	// Dir is the project directory (cwd).
	Dir string
	// Repo is the code repository holding Dir.
	Repo *gitref.Repo
	// StateDir is <git dir>/lore (per-checkout machine state).
	StateDir string
	// RemoteName is live.remote / LORE_LIVE_REMOTE (default origin).
	RemoteName string
	// Remote is nil when the repo has no such remote (solo, cookbook 1).
	Remote gitremote.Remote
	Org    *lorecfg.Org
	Config *lorecfg.Config
	Ident  identity.Resolved
	// ActorID is the caller's actor id (natural id of its stable key).
	ActorID string
	Clock   clock.Clock
	Lease   signals.Lease
	Agents  coord.Agents
	// Autopush false makes every coordination write fail closed.
	Autopush bool
	// Role is client, own or none (F10); a client repo's coordination data
	// lives in the linked store project, never in the repo (D8).
	Role orgresolve.Role
	// Links are this repo's store links (F11); a linked repo coordinates
	// its group's tasks on the store's refs.
	Links []orgresolve.Link
	// LinkUnknown: an org resolved but its link status could not be read;
	// coordination fails closed (E_LINK_UNKNOWN), TEAM-024.
	LinkUnknown bool
	// Folder is this project's folder on the repo's own refs (`_root` for
	// a project at the repo root, its path inside the repo otherwise).
	Folder string
}

// openCoordWorld gathers the world for a coordination command in the
// working directory.
func openCoordWorld(ctx context.Context) (*coordWorld, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	sit, err := gatherOrgSituation(ctx, dir)
	if err != nil {
		return nil, err
	}
	ident, err := sit.identity()
	if err != nil {
		return nil, err
	}
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		return nil, errcodes.New(errcodes.Unsupported, "coordination needs a git repository").WithCause(err).
			WithHint("claims and holds live on refs/lore/signals of the repo's remote")
	}
	gitDir, err := repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "locate the git dir").WithCause(err)
	}
	top, err := repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--show-toplevel"}})
	folder := rootFolderName
	if err == nil {
		if rel := projectRelPath(strings.TrimSpace(string(top)), dir); !isRepoRoot(rel) {
			folder = rel
		}
	}
	w := &coordWorld{
		Dir: dir, Repo: repo, StateDir: filepath.Join(strings.TrimSpace(string(gitDir)), liveStateDirName),
		Org: sit.Result.Org, Config: sit.Config, Ident: ident, Clock: cliClock(), Folder: folder,
		Role: sit.Result.Role, LinkUnknown: sit.Result.LinkUnknown, Links: sit.Result.Links,
	}
	live := sit.Config.LiveFor(w.Org)
	w.Autopush = live.Autopush && os.Getenv(envLiveAutopush) != envValueOff
	w.RemoteName = live.Remote
	if v := strings.TrimSpace(os.Getenv(envLiveRemote)); v != "" {
		w.RemoteName = v
	}
	if w.RemoteName == "" {
		w.RemoteName = defaultRemoteName
	}
	if remoteExists(ctx, repo, w.RemoteName) {
		w.Remote = &gitremote.Git{Repo: repo, Name: w.RemoteName}
	}
	l := sit.Config.LeaseFor(w.Org)
	w.Lease = signals.Lease{TTL: l.TTL, RenewEvery: l.RenewEvery, Grace: l.Grace, PushRetries: l.PushRetries}
	a := sit.Config.AgentsFor(w.Org)
	w.Agents = coord.Agents{MaxClaims: a.MaxClaimsPerAgent, MaxAttempts: a.MaxAttempts}
	w.ActorID, err = ids.Deterministic(ids.PrefixActor, ident.StableKey)
	if err != nil {
		return nil, errcodes.New(errcodes.IdentityFailed, "derive the actor id").WithCause(err)
	}
	return w, nil
}

// defaultRemoteName is the remote used when config names none.
const defaultRemoteName = "origin"

// remoteExists reports whether the repo has a remote of that name.
func remoteExists(ctx context.Context, r *gitref.Repo, name string) bool {
	_, err := r.Run(ctx, gitref.Invocation{Args: []string{"config", "--get", "remote." + name + ".url"}})
	return err == nil
}

// IsAgent reports whether the caller is an agent actor.
func (w *coordWorld) IsAgent() bool { return w.Ident.Kind == identity.KindAgent }

// Session is the caller's session: LORE_SESSION for an agent; for a human
// humanSession (one person at one machine is one holder).
func (w *coordWorld) Session() string {
	if w.IsAgent() {
		if w.Ident.Session != "" {
			return w.Ident.Session
		}
		if v := strings.TrimSpace(os.Getenv(identity.EnvSession)); v != "" {
			return v // no org: the fallback chain does not carry the session
		}
	}
	return humanSession(w.Ident.StableKey)
}

// humanSession is a person's claim session (coordinator answer 14:05):
// `<stable_key>@<hostname>`, so two people on one shared machine are never
// one holder and one person at one machine always is.
func humanSession(stableKey string) string {
	host, _ := os.Hostname()
	return stableKey + humanSessionSep + host
}

// Caller is the signals caller for this command.
func (w *coordWorld) Caller() signals.Caller {
	host, _ := os.Hostname()
	label := w.Ident.StableKey
	if !w.IsAgent() {
		label = w.Ident.DisplayName
		if w.Ident.Handle != "" {
			label = "@" + w.Ident.Handle
		}
	}
	return signals.Caller{ActorID: w.ActorID, Label: label, Session: w.Session(), Host: host}
}

// requireAgentOrHuman refuses a coordination command from an actor that is
// neither a registered agent nor a person (§7.4 "Identity").
func (w *coordWorld) requireActor() error {
	switch w.Ident.Kind {
	case identity.KindAgent, identity.KindHuman:
		return nil
	}
	return errcodes.New(errcodes.IdentityFailed, "coordination needs an agent actor or a person on the roster").
		WithHint("start an agent session with `lore agent start --label <name>`, or set your org identity")
}

// signalsStore builds the signals store of ref in repo/remote.
func (w *coordWorld) signalsStore(repo *gitref.Repo, remote gitremote.Remote, ref string) *signals.Store {
	sig := gitref.Signature{Name: w.Ident.DisplayName, Email: signatureEmail(w.Ident.StableKey)}
	return &signals.Store{
		Repo: repo, Remote: remote, RemoteName: w.RemoteName, Ref: ref,
		Seen:  signals.SeenStore{Path: filepath.Join(w.StateDir, seenFileName)},
		Clock: w.Clock, Lease: w.Lease,
		Signature: func(at time.Time) gitref.Signature {
			s := sig
			s.When = at
			return s
		},
		Autopush: w.Autopush && remote != nil, Tokens: signals.CryptoTokens{}, Jitter: signals.CryptoJitter{},
		Trace: traceFunc(),
		Gate:  repoSignalsGate(w.Dir), // FORMAT_EVOLUTION rule 2: no claim/renew/release while read-only
	}
}

// repoSignalsChecked returns this repo's own signals store, refusing when
// the repo is a client repo (its coordination belongs in the linked store
// project, D8) or its link status is unknown (fail closed, TEAM-024).
func (w *coordWorld) repoSignalsChecked() (*signals.Store, error) {
	if w.LinkUnknown {
		return nil, errcodes.New(errcodes.LinkUnknown, "cannot tell whether this repo is a client repo; refusing to write coordination data into it").
			WithHint("`lore whoami` shows the link status; fetch the store and retry")
	}
	if w.Role == orgresolve.RoleClient {
		return nil, errStoreSignalsNotWired()
	}
	return w.repoSignals(), nil
}

// errStoreSignalsNotWired refuses coordination in a client repo until the
// store project's signals ref is reachable from this build (stores work).
func errStoreSignalsNotWired() error {
	return errcodes.New(errcodes.NotImplemented, "this is a client repo: its claims and holds belong on the linked store's signals ref, which this build cannot reach yet").
		WithHint("never written into the client repo (D8); use a build that includes the stores work")
}

// repoSignals is the signals store of this repo's own ref (own or unlinked
// repos, §1 "Refs").
func (w *coordWorld) repoSignals() *signals.Store {
	return w.signalsStore(w.Repo, w.Remote, signals.CodeRepoRef)
}

// held is this checkout's held-claims record.
func (w *coordWorld) held() coord.HeldStore {
	return coord.HeldStore{Path: filepath.Join(w.StateDir, heldFileName)}
}

// signatureEmail turns a stable key (human:<email>, agent:<label>) into a
// commit e-mail; agents get a non-routable address.
func signatureEmail(stableKey string) string {
	if v, ok := strings.CutPrefix(stableKey, identity.KindHuman+":"); ok && strings.Contains(v, "@") {
		return v
	}
	return strings.ReplaceAll(stableKey, ":", ".") + "@lore.invalid"
}

// traceFunc writes signals trace lines to stderr when LORE_TRACE=1.
func traceFunc() func(string) {
	if os.Getenv(envLoreTrace) != "1" {
		return nil
	}
	return func(line string) { fmt.Fprintln(os.Stderr, "trace: "+line) }
}

// envLoreTrace turns on --trace output (§1).
const envLoreTrace = "LORE_TRACE"

// coordError maps coordination failures to CLI errors with the spec's
// codes (§1 "Error codes"); everything else passes through.
func coordError(err error) error {
	if err == nil {
		return nil
	}
	var cli *errcodes.CLIError
	if errors.As(err, &cli) {
		return err
	}
	var fe *coord.FencedError
	var he *signals.HeldError
	var we *signals.WIPError
	var nw *coord.NoWorkError
	var pe *coord.PoisonedError
	var off *signals.OfflineError
	switch {
	case errors.As(err, &fe):
		var code errcodes.Code
		switch fe.Kind {
		case coord.FenceCancelled:
			code = errcodes.Cancelled
		case coord.FenceReassigned:
			code = errcodes.Reassigned
		case coord.FenceLost:
			code = errcodes.LostClaim
		default:
			code = errcodes.LostClaim // an unknown kind fails closed as a lost claim
		}
		return errcodes.New(code, fe.Error()).WithHint("stop work on this task; `lore task next` finds other work")
	case errors.As(err, &off):
		return errcodes.New(errcodes.Offline, off.Error()).
			WithHint("coordination writes are never queued; reconnect (or turn live.autopush on) and run the command again")
	case errors.As(err, &he):
		msg := he.Error()
		if coord.IsMission(he.By.Task) && !he.SameActor {
			msg = fmt.Sprintf("%s is coordinated by %s (until %s)", he.By.Task, holderLabel(he.By), he.By.Expires.UTC().Format(time.Kitchen))
		}
		// EXACT ERRORS (P2-TASK-3): a hold is contention, not bad input.
		// Someone else holds it → E_CONTENDED (CLI_REFERENCE 5: a claim on
		// a name someone else holds); your own actor in another session →
		// E_REFUSED (waiting will not help; the message names the
		// LORE_ACTOR fix).
		if he.SameActor {
			return errcodes.New(errcodes.Refused, msg)
		}
		return errcodes.New(errcodes.Contended, msg).WithHint("retry after the hold ends, or pick other work with `lore task next`")
	case errors.As(err, &we):
		return errcodes.New(errcodes.InvalidInput, we.Error())
	case errors.As(err, &nw):
		return errcodes.New(errcodes.NoWork, nw.Error())
	case errors.Is(err, coord.ErrContended):
		return errcodes.New(errcodes.Contended, "ready work exists but every claim attempt lost a race").WithHint("retry after a short wait")
	case errors.As(err, &pe):
		return errcodes.New(errcodes.InvalidInput, pe.Error())
	case errors.Is(err, coord.ErrUnknownTask):
		return errcodes.New(errcodes.NotFound, err.Error()).WithHint("`lore sync peek` shows rows not fetched yet")
	case errors.Is(err, coord.ErrNotOpen):
		return errcodes.New(errcodes.InvalidInput, err.Error())
	case errors.Is(err, signals.ErrNotHeld):
		return errcodes.New(errcodes.LostClaim, err.Error())
	case errors.Is(err, signals.ErrBadName):
		return errcodes.New(errcodes.InvalidInput, err.Error())
	}
	return err
}

func holderLabel(s signals.Signal) string {
	if s.HolderLabel != "" {
		return strings.TrimPrefix(s.HolderLabel, identity.KindAgent+":")
	}
	return s.Holder
}
