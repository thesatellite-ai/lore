package host

// live_hook.go — LiveHook: v0.1.17 live rows (refs/lore/live) around every
// operation of a binary built from the root module (LORE_TEAM_SPEC F1-F3,
// F8; STORE_SERVER_SPEC phase D: cmd/lore as the release binary).
//
// Flow:
//
//	Options ─▶ WrapBackend: the repo store's backend gets liveBackend
//	        └▶ LiveRef: `lore live …` (ops/live) gets liveRefAdapter
//	Around  ─▶ a liveRun on ctx (sessions keyed by git top level)
//	        ─▶ prepare (lock, fetch when due, integrate) for the cwd repo
//	        ─▶ refuse a write when the remote ref was rewritten (E_READ_ONLY)
//	           or this binary is below min_lore_version (E_OLD_LORE)
//	        ─▶ the operation ─▶ finish every session (push, status line)
//
// No package state: the sessions live on the run's context, so `lore api`
// serves each request with its own fetch and push, as v0.1.17's api runner
// did (finishLive per request).

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/clock"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opslive "github.com/thesatellite-ai/lore/ops/live"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// HookLive is LiveHook's name.
const HookLive = "live"

// LiveConfig configures LiveHook.
type LiveConfig struct {
	// Home is the lore home (~/.lore), where config.yaml lives.
	Home string
	// WorkDir is the folder commands run in (the project is found from it).
	WorkDir string
	// Version is the binary's version (min_lore_version floor, live meta).
	Version string
	// Stderr receives the live status lines; nil is os.Stderr.
	Stderr io.Writer
	// HoldFinishLines keeps the lines a CLI operation's end-of-run push and
	// landing print (`lore live: (live, pushed)`, "landed on main …") until
	// Live.FlushHeld, so they follow the command's own result as in
	// v0.1.17 (the run finishes inside the operation, before the surface
	// prints). A `lore api` request never holds. cmd/lore sets it and
	// flushes after the command; false prints at once.
	HoldFinishLines bool
	// Clock stamps live versions and fetch ages; nil is the wall clock.
	// Test builds pass a fixed clock (LORE_NOW).
	Clock clock.Clock
	// Git runs git; nil is gitref.Exec (git from PATH).
	Git gitref.Git
	// LeakCheck is the F16 check of rows about to reach the live ref or the
	// default branch, both readable by the repo's client (where: "live
	// write" or "landing"; docs: live path → row body). cmd/lore passes
	// wire.App.LeakCheckDocs; nil checks nothing.
	LeakCheck func(ctx context.Context, where string, docs map[string][]byte) error
	// RepoAddresses is the repo identity half of `lore sync` (PROV2, Q2:
	// address notices and the ask-once question), run at the start of
	// every sync; cmd/lore passes wire.App.SyncRepoAddresses. nil skips it.
	RepoAddresses func(ctx context.Context)
	// Signer is the lore identity (`lore whoami`) that signs live commits
	// when the repo's git has no user.name/user.email (FLAG4: an existing
	// git identity always wins, git config is never changed); cmd/lore
	// passes wire.App.LoreSignature, the signer of every core store commit
	// (r3-sign). nil, or ok=false, signs as liveSignatureFallback.
	Signer func(ctx context.Context) (name, email string, ok bool)
	// OrgLive is the `live:` block of the org entry this repo resolves to
	// (orgs[].live in config.yaml; nil when no org or no block): its keys
	// override the top-level live: ones for this command (P3-MODEB-2,
	// CONFIG_GUIDE "overridden per org entry"). cmd/lore passes the org
	// resolution's entry; nil reads only the top level.
	OrgLive func(ctx context.Context) *orgconf.Live
	// MetaStatus gives the metadata lines of `lore sync status`
	// (quarantined rows, registries past their key cap; FA-META-11);
	// cmd/lore passes wire.App.MetaStatusLines. nil prints none.
	MetaStatus func(ctx context.Context) (quarantined, overCap []string)
}

// Leak check actions (the refusal names them).
const (
	leakWhereLive    = "live write"
	leakWhereLanding = "landing"
	leakWhereRepo    = "repo write"
)

// leakCheck runs cfg.LeakCheck over rows of folder (tombstones carry no
// content and are skipped).
func (h *liveHost) leakCheck(ctx context.Context, where, folder string, rows map[live.RowKey]live.File) error {
	if h.cfg.LeakCheck == nil {
		return nil
	}
	docs := make(map[string][]byte, len(rows))
	for k, f := range rows {
		if f.Deleted() {
			continue
		}
		b, err := f.EncodeBody()
		if err != nil {
			return errs.Wrap(errs.LeakBlocked, "cannot check: encode "+k.String(), err)
		}
		docs[live.RowPath(folder, k.Table, k.ID)] = b
	}
	return h.cfg.LeakCheck(ctx, where, docs)
}

// leakCheckBranch runs cfg.LeakCheck over the branch rows of a write (the
// repo store's .lore/data, which the client reads once committed); the
// documents are the rows' own bytes, tombstones skipped.
func (h *liveHost) leakCheckBranch(ctx context.Context, folder string, changes []store.Change) error {
	if h.cfg.LeakCheck == nil || len(changes) == 0 {
		return nil
	}
	docs := make(map[string][]byte, len(changes))
	for _, ch := range changes {
		if ch.Row.Deleted || len(ch.Row.Doc) == 0 {
			continue
		}
		docs[mainDataPath(folder, string(ch.Row.Table)+"/"+ch.Row.ID.String()+rowFileExt)] = ch.Row.Doc
	}
	return h.cfg.LeakCheck(ctx, leakWhereRepo, docs)
}

// liveHost is LiveHook's shared configuration.
type liveHost struct {
	cfg LiveConfig
	mu  sync.Mutex // serialises status lines of concurrent `lore api` runs
	// holding sends line() to held (a CLI run's finish, live_holdlines.go).
	holding bool
	held    []string
}

func (h *liveHost) clock() clock.Clock {
	if h.cfg.Clock != nil {
		return h.cfg.Clock
	}
	return clock.Real{}
}

func (h *liveHost) git() gitref.Git {
	if h.cfg.Git != nil {
		return h.cfg.Git
	}
	return gitref.Exec{}
}

// line writes one status line (no trailing newline) to the host's stderr.
func (h *liveHost) line(text string) {
	w := h.cfg.Stderr
	if w == nil {
		w = os.Stderr
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.holding {
		h.held = append(h.held, text)
		return
	}
	_, _ = io.WriteString(w, text+"\n") // a status line that cannot be printed is lost, the write is not
}

// liveRun is one operation's live sessions, keyed by git top level.
type liveRun struct {
	h        *liveHost
	kind     live.FetchKind
	agent    bool
	sessions map[string]*liveSession
}

type liveRunKey struct{}

// runFrom is the run on ctx (nil outside a hooked run: no live overlay).
func runFrom(ctx context.Context) *liveRun {
	r, _ := ctx.Value(liveRunKey{}).(*liveRun)
	return r
}

// session is the prepared session of the repo at top (nil when live rows
// do not apply there).
func (r *liveRun) session(ctx context.Context, top string) *liveSession {
	if s, ok := r.sessions[top]; ok {
		if s != nil {
			s.prepare(ctx)
		}
		return s
	}
	var s *liveSession
	repo, err := gitref.Open(ctx, top, r.h.git())
	if err == nil && !repo.Bare() {
		s, err = openLiveSession(ctx, r.h, repo, top, r.kind)
		if s != nil {
			s.agent = r.agent
		}
		if err != nil {
			r.h.line(liveLogPrefix + err.Error())
		}
	}
	r.sessions[top] = s
	if s != nil {
		s.prepare(ctx)
	}
	return s
}

// finish ends every session of the run, in top-level order.
func (r *liveRun) finish(ctx context.Context) {
	defer r.h.holdLines(ctx)()
	tops := make([]string, 0, len(r.sessions))
	for t := range r.sessions {
		tops = append(tops, t)
	}
	sort.Strings(tops)
	for _, t := range tops {
		if s := r.sessions[t]; s != nil {
			s.finish(ctx)
		}
	}
}

// release drops every session's live lock without finishing the run
// (nothing is pushed or landed): a git hook that refuses (a pre-push leak
// refusal) stops here.
func (r *liveRun) release() {
	for _, s := range r.sessions {
		if s != nil {
			s.release()
		}
	}
}

// LiveHook is the live-rows hook (see the file comment). The old binary
// keeps its own live wiring until it is deleted, so this hook is not in
// Default: cmd/lore adds it.
func LiveHook(cfg LiveConfig) Hook { return NewLive(cfg).Hook() }

// Hook is the live-rows operation hook (LiveHook).
func (l *Live) Hook() Hook {
	h := l.h
	return Hook{
		Name: HookLive,
		Options: func(o *wire.Options) {
			prev := o.WrapBackend
			o.WrapBackend = func(ctx context.Context, name store.Source, folder string, b store.Backend) store.Backend {
				if prev != nil {
					b = prev(ctx, name, folder, b)
				}
				return h.wrap(name, b)
			}
			o.LiveRef = h.liveRef
			if o.Sync == nil {
				o.Sync = liveSync{h: h} // live-only lore sync / lore land (P3-MODEB-3)
			}
			o.Context.Doctor = liveDoctor{h: h, inner: o.Context.Doctor} // live rows in `lore doctor` (live_doctor.go)
		},
		Around:  h.around,
		OnError: h.explainNotFound,
	}
}

// dirBackend is a backend that knows its repository folder.
type dirBackend interface {
	store.Backend
	Dir() string
}

// wrap puts liveBackend in front of the repo store (live mode on, a
// folder backend).
func (h *liveHost) wrap(name store.Source, b store.Backend) store.Backend {
	if name != store.SourceRepo {
		return b
	}
	d, ok := b.(dirBackend)
	if !ok {
		return b
	}
	top, folder := livePlace(d.Dir())
	return &liveBackend{inner: d, top: top, folder: folder, h: h, off: liveDisabled()}
}

// livePlace is the git top level holding dir and the live folder of the
// project there (`_root`, or the repo-relative path of a monorepo
// subproject; v0.1.17 liveRepoPlace).
func livePlace(dir string) (top, folder string) {
	real := dir
	if abs, err := filepath.Abs(dir); err == nil {
		real = abs
	}
	if r, err := filepath.EvalSymlinks(real); err == nil {
		real = r
	}
	for d := real; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			rel, err := filepath.Rel(d, real)
			if err != nil {
				return dir, live.RootFolder
			}
			return d, live.FolderFor(filepath.ToSlash(rel))
		}
		if filepath.Dir(d) == d {
			return dir, live.RootFolder
		}
	}
}

// liveProjectRoot is the nearest folder holding .lore/ from dir up to the repo
// top level ("" when none).
func liveProjectRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for d := abs; ; d = filepath.Dir(d) {
		if fi, err := os.Stat(filepath.Join(d, ".lore")); err == nil && fi.IsDir() {
			return d
		}
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return ""
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// liveSkip are operations that never touch the live ref (v0.1.17
// liveSkipPrefixes): they run before a project exists, edit machine files
// only, or hold their own locks (migrate takes M, ranked before L).
var liveSkip = []string{"init", "config", "identity", "whoami", "org", "migrate", "stores", "version", "spec"}

// liveSkipOps are single operations that never touch the live ref although
// their noun does: task heartbeat renews claims on the signals ref only
// (coord.Renew stages no row), and its --daemon form runs for the whole
// agent session, so holding the live lock for it would block every other
// lore command in the checkout until the session ends.
var liveSkipOps = map[string]bool{"task.heartbeat": true}

// fetchAlways are the operations that always fetch (TEAM-025).
var fetchAlways = map[string]bool{
	"sync.run": true, "sync.status": true, "land.run": true,
	opslive.OpRestore: true, opslive.OpCompact: true,
}

// leakExemptOps are the nouns whose branch writes skip the write-time F16
// check (noSessionLeakCheck): migrate rewrites the rows a repo already
// holds into the 0.1.18 format and adds no text of its own.
var leakExemptOps = map[string]bool{"migrate": true}

// leakExemptKey marks a run of a leakExemptOps operation on its context.
type leakExemptKey struct{}

// leakExempt reports whether ctx is a run of a leakExemptOps operation.
func leakExempt(ctx context.Context) bool {
	v, _ := ctx.Value(leakExemptKey{}).(bool)
	return v
}

// noun is the first word of an operation name ("memory" of "memory.add").
func noun(name string) string {
	n, _, _ := strings.Cut(name, ".")
	return n
}

func skipLive(name string) bool {
	if liveSkipOps[name] {
		return true
	}
	noun, _, _ := strings.Cut(name, ".")
	for _, s := range liveSkip {
		if noun == s {
			return true
		}
	}
	return false
}

// around is LiveHook's Around (see the file comment).
func (h *liveHost) around(ctx context.Context, run *Run, next Next) (op.Result, error) {
	spec := run.Entry.Spec()
	if leakExemptOps[noun(spec.Name)] {
		ctx = context.WithValue(ctx, leakExemptKey{}, true)
	}
	if liveDisabled() || skipLive(spec.Name) {
		return next(ctx)
	}
	r := &liveRun{h: h, sessions: map[string]*liveSession{}, agent: run.Caller.Agent != ""}
	if fetchAlways[spec.Name] {
		r.kind = live.FetchAlways
	}
	ctx = context.WithValue(ctx, liveRunKey{}, r)
	defer r.finish(ctx)
	if root := liveProjectRoot(h.cfg.WorkDir); root != "" {
		top, _ := livePlace(root)
		if s := r.session(ctx, top); s != nil {
			s.ensureGitWiring(ctx, root) // merge driver, attributes, hook blocks (gitwire.go)
			if err := h.refuse(ctx, s, spec); err != nil {
				return op.Result{}, err
			}
		}
	}
	return next(ctx)
}

// refuse is the pre-run part of v0.1.17 livePreRun: the offline note, and
// for a write the rewritten-remote and version-floor refusals.
func (h *liveHost) refuse(ctx context.Context, s *liveSession, spec op.Spec) error {
	if s.fetchErr != nil {
		if age := s.fetchAge(); age != "" {
			h.line(liveLogPrefix + "offline: using live rows as fetched " + age + " ago")
		}
	}
	if spec.Access != op.AccessWrite || opslive.IsOp(spec.Name) {
		return nil // reads, and `lore live …` (restore is the repair), always run
	}
	if s.blocked != nil {
		return errs.Wrap(errs.ReadOnly, "the remote live ref was deleted or rewritten; lore stopped writing to it", s.blocked).
			WithHint("your local copy is kept under " + live.RefBackupPrefix + "; run `lore live restore` to put it back")
	}
	if err := s.ref.CheckFloor(ctx, h.cfg.Version); errors.Is(err, live.ErrBelowFloor) {
		return errs.Wrap(errs.OldLore, "this lore is too old to write live rows here (read-only)", err).
			WithHint("run `lore upgrade`; reading still works")
	}
	return nil
}
