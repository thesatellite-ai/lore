package host

// clientwiring.go — lore changes no tracked file of a client repo, git
// wiring included (devco:D-754, hunt H-13).
//
// A repo an org names as a client repo (its repos: pattern or a client
// link) is the client's: `lore doctor --fix` already skips its agent hooks
// saying "lore never changes its tracked files". Its git wiring did not
// hold to that line when the repo still carried row files from before it
// became a client repo (a lore that set it up as an own project): the same
// run rewrote the tracked .gitattributes, and a write or `lore sync` could
// re-include .lore/data in the tracked .gitignore. Now every writer of a
// tracked file asks the same question first:
//
//	client repo ─▶ .gitattributes rules ─▶ the clone's .git/info/attributes
//	            ─▶ .gitignore lines     ─▶ the clone's .git/info/exclude
//	            ─▶ a rule hiding rows   ─▶ reported, never rewritten
//	own repo    ─▶ as before (the tracked files, for the owner to commit)
//
// The question is the org resolution's (LiveConfig.ClientRepo); a nil
// classifier or one that cannot tell answers "not a client repo", as the
// agent hooks step does.

import "context"

// ClientRepoFunc reports whether the repo the command runs in resolves as
// a client repo of an org. cmd/lore passes the org resolution's role; nil
// (a test host, a build with no orgs) is never a client repo.
type ClientRepoFunc func(ctx context.Context) bool

// clientRepoHere asks fn, treating nil as "not a client repo". Callers ask
// it last, after the cheaper reasons to keep a file local, since it may
// resolve the org.
func clientRepoHere(ctx context.Context, fn ClientRepoFunc) bool {
	return fn != nil && fn(ctx)
}

// clientExcludeReason is the reason announceExclude gives for a client
// repo; clientIgnoredNote is the read's and write's line when a rule of
// the client's .gitignore hides row files lore will not re-include there.
const (
	clientExcludeReason = "this is a client repo, so lore changes none of its tracked files"
	clientIgnoredNote   = "git ignores lore data in this client repo; lore changes none of its tracked files, so " + gitignoreName + " is left as it is (change that rule by hand if these rows belong in the repo):\n  "
)
