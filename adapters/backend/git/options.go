package git

import (
	"context"
	"slices"
	"strings"
	"time"
)

// Merger merges one row file that both sides of a sync changed (spec 11).
// The git backend never decides a merge itself: the engine injects the
// core's field-level merge (logic/merge), the same function every backend
// uses.
//
// base is the common ancestor's document (nil when both sides created the
// row independently), ours the local one, theirs the remote one; all are
// canonical row documents of the same table and id. merged must be a
// canonical document of that row. conflict true means the row cannot be
// merged automatically: the sync then fails closed (nothing merged or
// pushed, the local commits kept, the row listed in SyncReport.Refused).
type Merger interface {
	Merge(base, ours, theirs []byte) (merged []byte, conflict bool, err error)
}

// Signature is a commit author: the actor a batch is written for, or the
// opener's default.
type Signature struct {
	Name  string
	Email string
}

// IsZero reports an unset signature.
func (s Signature) IsZero() bool { return s.Name == "" && s.Email == "" }

// DefaultAuthor signs commits when neither the opener nor the batch names
// an author.
var DefaultAuthor = Signature{Name: "lore", Email: "lore@localhost"}

// DefaultBranch is the store branch when the store does not name one (an
// empty remote, or one whose HEAD cannot be read).
const DefaultBranch = "main"

// DefaultBinary is the git program looked up on PATH.
const DefaultBinary = "git"

// config holds the options of one opener (or one direct Open).
type config struct {
	merger Merger
	author Signature
	// identity is the lazy lore identity (WithIdentity); nil uses author.
	identity func(context.Context) Signature
	// signer is the store owner's signature (WithSigner); when set it signs
	// every commit and merge whatever git knows (Q-R2-1).
	signer      Signature
	now         func() time.Time
	bin         string
	branch      string
	lockTimeout time.Duration
	// origins maps a canonical Remote.URL to the origin spelling NewOpener
	// writes into the clone (WithOrigins); absent, the origin is Remote.URL.
	origins map[string]string
	// trailers are appended to every commit and merge this backend makes
	// (WithTrailers): lore's own commits only, never a person's (FQ7).
	trailers []string
	// trailerFuncs add trailers asked for at each commit (WithTrailerFunc)
	trailerFuncs []func() []string
	// route gives the per-call git options and environment of the active
	// setup's ssh route (WithRoute); nil = none.
	route func(ctx context.Context, remote string) (args, env []string)
	// accessNote names the access rule a remote is reached through
	// (WithAccessNote); nil = none.
	accessNote func(remote string) string
	// own marks a person's own clone (`lore store use`, withOwnClone):
	// its origin spelling is kept and it is never removed by lore.
	own bool
	// keepSpelling keeps an existing origin that names the same store in
	// another spelling (PrepareClone: the org link code never fights the
	// backend over how origin is spelled).
	keepSpelling bool
	// handGit is the merge driver and pre-commit hook a store clone gets
	// for plain git (WithHandGit, handgit.go); zero = none.
	handGit HandGit
	// cloneIdentity names the git identity a store clone gets in its own
	// .git/config when it has none there (WithCloneIdentity,
	// cloneidentity.go); nil = none.
	cloneIdentity func(ctx context.Context, remote string) CloneIdentity
	// identitySay is told when a write or sync gave an existing clone its
	// identity (WithCloneIdentitySay); nil = nobody.
	identitySay func(ctx context.Context, dir string, id CloneIdentity)
}

// withOwnClone marks the clone NewOpener opens as a person's own clone.
func withOwnClone() Option { return func(c *config) { c.own = true } }

// WithRoute sets how a store clone reaches its remote (ONBOARDING_DESIGN
// 3.4, Q5): args are global git options put before every subcommand of the
// clone (`-c url.git@<alias>:.insteadOf=git@github.com:`), env is added to
// its environment (GIT_SSH_COMMAND, the fallback without an alias). It is
// asked once per OpenClone with the open's context and the remote url the
// clone fetches from (so the route can be the setup that owns that store),
// never written into any git config, and only lore's own clone calls get it.
func WithRoute(f func(ctx context.Context, remote string) (args, env []string)) Option {
	return func(c *config) { c.route = f }
}

// WithAccessNote gives the backend the text a failed network call (clone,
// fetch, push, ls-remote) of remote adds to its error: the access rule the
// call went through and the next step ("" when no rule applies). wire
// passes orgconf.AccessFailureNote of the person's config.yaml rule.
func WithAccessNote(f func(remote string) string) Option {
	return func(c *config) { c.accessNote = f }
}

// Option configures the git backend.
type Option func(*config)

// WithMerger injects the row merge Sync uses. Without one, a row both sides
// changed fails the sync closed.
func WithMerger(m Merger) Option { return func(c *config) { c.merger = m } }

// WithAuthor sets the default commit author (default DefaultAuthor).
func WithAuthor(s Signature) Option { return func(c *config) { c.author = s } }

// WithIdentity sets the lore identity that signs a commit or merge when
// git has no identity of its own for the clone (FLAG4). It is called only
// then, so resolving it costs nothing on a machine where git knows the
// person; a zero or unusable answer falls back to the WithAuthor author.
func WithIdentity(f func(context.Context) Signature) Option {
	return func(c *config) { c.identity = f }
}

// WithClock replaces time.Now for commit dates and the stat cursor.
func WithClock(now func() time.Time) Option { return func(c *config) { c.now = now } }

// WithGit names the git binary (default DefaultBinary on PATH).
func WithGit(path string) Option { return func(c *config) { c.bin = path } }

// WithBranch pins the store branch instead of following the remote's HEAD.
func WithBranch(name string) Option { return func(c *config) { c.branch = name } }

// WithOrigins tells NewOpener the person's own spelling of each store url
// (OriginOf of the url as written), keyed by the canonical credential-free
// Remote.URL the opener receives: the clone's origin is that spelling, so a
// url.<base>.insteadOf rule keyed on it applies and an scp-like relative
// path stays relative. The map is copied. A url absent from it gets
// Remote.URL. Callers must pass credential-free values (OriginOf output).
func WithOrigins(m map[string]string) Option {
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return func(c *config) { c.origins = cp }
}

// WithTrailers appends "Key: value" trailer lines to every commit and
// merge commit the backend makes (wire passes Lore-Version and, in CI,
// Lore-CI-Run: gitcommit.OwnCommitTrailers). The backend only ever commits
// to lore's own stores and live branches, never to a person's commit
// (FQ7). Empty lines and lines with a newline are dropped.
func WithTrailers(lines ...string) Option {
	cp := slices.Clone(lines)
	return func(c *config) { c.trailers = append(c.trailers, cp...) }
}

// WithTrailerFunc adds trailer lines asked for at each commit (f is
// called then, so a caller can resolve them lazily): wire names the agent
// a person's commits were made through (Lore-Agent) here, because a
// clone's git identity is always the person (owner bug 12).
func WithTrailerFunc(f func() []string) Option {
	return func(c *config) {
		if f != nil {
			c.trailerFuncs = append(c.trailerFuncs, f)
		}
	}
}

// Join bundles several options into one (for callers that take a single
// Option slot).
func Join(opts ...Option) Option {
	cp := slices.Clone(opts)
	return func(c *config) {
		for _, o := range cp {
			if o != nil {
				o(c)
			}
		}
	}
}

// withTrailers is msg with the configured trailers, then the per-write
// extra ones (store.CommitTrailers, e.g. Lore-Moved-To; PROVENANCE 5.4),
// after one blank line (git interpret-trailers form); msg unchanged when
// there are none. A blank or multi-line entry is dropped.
func (c config) withTrailers(msg string, extra ...string) string {
	var keep []string
	all := slices.Clone(c.trailers)
	for _, f := range c.trailerFuncs {
		all = append(all, f()...)
	}
	for _, t := range slices.Concat(all, extra) {
		if t = strings.TrimSpace(t); t != "" && !strings.ContainsAny(t, "\r\n") {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		return msg
	}
	return strings.TrimRight(msg, "\n") + "\n\n" + strings.Join(keep, "\n")
}

// WithLockTimeout sets how long a write or sync waits for the store lock
// (default rowfs.DefaultLockTimeout).
func WithLockTimeout(d time.Duration) Option { return func(c *config) { c.lockTimeout = d } }

// build applies opts over the defaults.
func build(opts []Option) config {
	c := config{author: DefaultAuthor, now: time.Now, bin: DefaultBinary}
	for _, o := range opts {
		o(&c)
	}
	if c.author.IsZero() {
		c.author = DefaultAuthor
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.bin == "" {
		c.bin = DefaultBinary
	}
	return c
}
