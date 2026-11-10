package gitref

import (
	"context"
	"slices"
	"strings"
	"sync"
)

// ReadCache is a Git that remembers the answers of read-only git commands
// for one bounded scope (one lore operation on one repository), so code that
// asks the same question many times — "where is refs/lore/live?", "what is
// under .lore/data at HEAD?" — runs git once instead of once per row. One
// `lore resource import` of 40 rows asked those five questions about a
// thousand times each before this existed (~6000 git processes, ~18 s).
//
// What is cached, and for how long:
//
//   - check-ref-format: its answer depends on the name only, so it is kept
//     for the life of the cache.
//   - for-each-ref, ls-tree, rev-parse, cat-file, merge-base and the read
//     form of symbolic-ref (one name, no -d): kept until the next command
//     that is not one of these runs through the same cache (update-ref,
//     commit-tree, fetch, push, hash-object, config, ...), or until
//     Invalidate. Anything not recognised as a read counts as a write, so a
//     new plumbing call can only make the cache drop more, never serve stale.
//
// Invariant: after a write that went through this cache returns, no read
// answer from before it is served again. A read that overlapped a write
// is answered but not remembered. Writes made to the same repository by
// anything else (another process, another Git value, a hook) are not seen:
// the owner must call Invalidate at the points where those can happen (for
// lore: after taking the live lock), and must not keep a cache longer than
// one operation.
//
// Only answers from a git that ran to an exit status are kept (success or a
// plain non-zero exit such as "no such ref"); a missing git or a cancelled
// context is passed through and never remembered. Invocations with extra
// Env are neither cached nor treated as reads (they set identities, index
// files or dates, which writes do). Safe for concurrent use.
type ReadCache struct {
	inner Git

	mu sync.Mutex
	// pure holds answers that never go stale (check-ref-format).
	pure map[string]cachedAnswer
	// reads holds answers valid until the next write or Invalidate.
	reads map[string]cachedAnswer
	// gen moves on every write start, write end and Invalidate; a read
	// stores its answer only when gen did not move while it ran.
	gen uint64
	// writing counts writes in flight: no read is stored while one runs.
	writing int
}

// cachedAnswer is one remembered git result. out is never handed out
// directly (callers get a copy) so no caller can change another's answer.
type cachedAnswer struct {
	out []byte
	err error
}

// NewReadCache wraps inner (nil means Exec{}) with an empty cache. Make one
// per operation scope and pass it to Open; never share one across
// operations or keep it in a package variable.
func NewReadCache(inner Git) *ReadCache {
	if inner == nil {
		inner = Exec{}
	}
	return &ReadCache{inner: inner, pure: map[string]cachedAnswer{}, reads: map[string]cachedAnswer{}}
}

// Invalidate forgets every answer that depends on refs or objects, for a
// caller that knows the repository may have changed outside this cache
// (another process wrote while we waited for a lock).
func (c *ReadCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropReadsLocked()
}

// dropReadsLocked empties reads and moves gen; c.mu must be held.
func (c *ReadCache) dropReadsLocked() {
	c.gen++
	clear(c.reads)
}

// readClass says how long an invocation's answer may be kept.
type readClass int

const (
	// classWrite: not cached, and it drops every ref-dependent answer.
	classWrite readClass = iota
	// classPure: the answer depends on the arguments only.
	classPure
	// classRead: the answer depends on refs and objects; kept until the
	// next write.
	classRead
)

// genReads are the subcommands that only read refs, objects or repository
// paths whatever their flags.
var genReads = map[string]bool{
	"for-each-ref": true, "ls-tree": true, "rev-parse": true, "cat-file": true, "merge-base": true,
}

// classify sorts an invocation into classWrite, classPure or classRead.
// Global options before the subcommand ("-C", "-c") and any Env make it a
// write: they can point git elsewhere or change its config, and nothing
// that passes them needs the cache.
func classify(inv Invocation) readClass {
	if len(inv.Args) == 0 || len(inv.Env) != 0 || strings.HasPrefix(inv.Args[0], "-") {
		return classWrite
	}
	sub, rest := inv.Args[0], inv.Args[1:]
	switch {
	case sub == "check-ref-format":
		return classPure
	case genReads[sub]:
		return classRead
	case sub == "symbolic-ref" && symbolicRefReads(rest):
		return classRead
	}
	return classWrite
}

// symbolicRefReads reports whether symbolic-ref's arguments ask a question
// (one name, no delete) rather than set or delete a symbolic ref.
func symbolicRefReads(args []string) bool {
	names := 0
	for _, a := range args {
		switch {
		case a == "-d" || a == "--delete" || a == "-m":
			return false
		case strings.HasPrefix(a, "-"):
		default:
			names++
		}
	}
	return names == 1
}

// cacheKey identifies an invocation: directory, arguments and stdin, each
// separated by a byte git never puts in an argument.
func cacheKey(inv Invocation) string {
	var b strings.Builder
	b.WriteString(inv.Dir)
	for _, a := range inv.Args {
		b.WriteByte(0)
		b.WriteString(a)
	}
	b.WriteByte(0)
	b.WriteByte(0)
	b.Write(inv.Stdin)
	return b.String()
}

// Run implements Git.
func (c *ReadCache) Run(ctx context.Context, inv Invocation) ([]byte, error) {
	class := classify(inv)
	if class == classWrite {
		return c.runWrite(ctx, inv)
	}
	key := cacheKey(inv)
	c.mu.Lock()
	table := c.reads
	if class == classPure {
		table = c.pure
	}
	if a, ok := table[key]; ok {
		c.mu.Unlock()
		return slices.Clone(a.out), a.err
	}
	gen, writing := c.gen, c.writing
	c.mu.Unlock()

	out, err := c.inner.Run(ctx, inv)
	if !rememberable(ctx, err) {
		return out, err
	}
	c.mu.Lock()
	if class == classPure || (writing == 0 && c.writing == 0 && c.gen == gen) {
		table[key] = cachedAnswer{out: slices.Clone(out), err: err}
	}
	c.mu.Unlock()
	return out, err
}

// runWrite runs a write and drops the ref-dependent answers both before it
// starts (no read stored meanwhile can predate it) and after it ends.
func (c *ReadCache) runWrite(ctx context.Context, inv Invocation) ([]byte, error) {
	c.mu.Lock()
	c.writing++
	c.dropReadsLocked()
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.writing--
		c.dropReadsLocked()
		c.mu.Unlock()
	}()
	return c.inner.Run(ctx, inv)
}

// rememberable reports whether a result may be kept: git ran and exited
// (zero or not) and the context was not cancelled meanwhile.
func rememberable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if err == nil {
		return true
	}
	_, exited := ExitCode(err)
	return exited
}
