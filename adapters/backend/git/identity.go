package git

import (
	"cmp"
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitcommit"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// Who signs a store clone's commits and merges:
//
//	org store (WithSigner)  the org identity (orgs[<org>].identity), name
//	                        and email, for every commit lore makes there,
//	                        whatever op makes it and whatever the clone's
//	                        git user says (D-714: org link signed as the org
//	                        identity while adopt let a git user.name with
//	                        the org email sign; one rule now). lore sets the
//	                        clone's own git user to that same identity
//	                        (cloneidentity.go), so a hand commit matches.
//	                        A personal git email never signs an org store's
//	                        commit (Q-R2-1, spec 6.5)
//	any other store         FLAG4: the clone's git identity when git has
//	                        one, else the lore identity (`lore whoami`,
//	                        WithIdentity), else the opener's author
//	                        (WithAuthor, default DefaultAuthor)
//
// lore never writes global git config: the org identity is passed as
// GIT_AUTHOR_*/GIT_COMMITTER_* on that one git call only.
//
// One exception: a person's own change lore commits for them (a hand
// edit, store.PersonAuthored) is authored by the person
// (personSignature), with the org identity as committer (brief 2.1).

// Environment variables through which a person already gives git an
// identity; when one is set lore leaves that side to git.
const (
	envCommitterName  = "GIT_COMMITTER_NAME"
	envCommitterEmail = "GIT_COMMITTER_EMAIL"
	envAuthorName     = "GIT_AUTHOR_NAME"
	envAuthorEmail    = "GIT_AUTHOR_EMAIL"
	// envEmail is git's own last e-mail source before guessing from the
	// host name (git-commit-tree(1) "Commit information").
	envEmail = "EMAIL"
)

// identityConfigKeys matches the git config keys that name an identity:
// user.* for both sides, committer.* for the committer (git-config(1)).
const identityConfigKeys = `^(user|committer)\.(name|email)$`

// configNotFound is `git config --get-regexp`'s exit status when no key
// matches.
const configNotFound = 1

// identityEnv is the identity part of a commit's environment. explicit is
// a batch's own author (Batch.Author), a per-write choice of the caller
// that is always used; zero, the person's git identity wins and nil is
// returned, else the opener's author is passed.
func (b *Backend) identityEnv(ctx context.Context, explicit Signature) ([]string, error) {
	if !explicit.IsZero() {
		if gitcommit.IsAgentSignature(explicit.Name, explicit.Email) {
			return nil, errs.New(errs.Internal, "git: a commit is never signed as an agent ("+explicit.Name+" <"+explicit.Email+">), devco:D-983")
		}
		return signatureEnv(explicit), nil
	}
	// An org store (owner signature): the org identity signs, whatever the
	// clone's git user says (D-714: one author for every op's commit; a
	// git user.name beside the org email no longer signs some ops' commits
	// and not others; a personal email never signs, Q-R2-1, spec 6.5).
	if s, ok := b.cfg.ownerSigner(); ok {
		if store.PersonAuthored(ctx) {
			// brief 2.1: a hand edit is committed as the person who made it;
			// the org identity signs as committer.
			p, err := b.personSignature(ctx)
			if err != nil {
				return nil, err
			}
			return append(authorEnv(p), committerEnv(s)...), nil
		}
		return signatureEnv(s), nil
	}
	has, err := b.gitHasIdentity(ctx)
	if err != nil || has {
		return nil, err
	}
	return signatureEnv(b.fallback(ctx)), nil
}

// WithSigner names the org identity of a store that belongs to an org: it
// signs every commit and merge lore makes in the store (D-714). A zero or
// unusable signature is ignored (the FLAG4 rule).
func WithSigner(s Signature) Option { return func(c *config) { c.signer = s } }

// ownerSigner is the WithSigner signature when it can be written into a
// git ident line.
func (c config) ownerSigner() (Signature, bool) {
	if !validSignature(c.signer) {
		return Signature{}, false
	}
	return c.signer, true
}

// validSignature reports whether s names a name and an e-mail git can
// write into an ident line, and is a person's: an agent signature
// (gitcommit.IsAgentSignature) signs no commit (devco:D-983).
func validSignature(s Signature) bool {
	return s.Name != "" && s.Email != "" && !strings.ContainsAny(s.Name+s.Email, forbiddenSignature) && !gitcommit.IsAgentSignature(s.Name, s.Email)
}

// SignerEnv is the identity environment of a commit lore makes itself
// (outside a Backend) in an org store signed by s: s as
// GIT_AUTHOR_*/GIT_COMMITTER_*, the same rule as a Backend's commits
// (D-714), whatever the worktree's git user says. ok is false when s is
// not usable; the caller then applies the FLAG4 rule (IdentityEnv).
func SignerEnv(s Signature) (env []string, ok bool) {
	if !validSignature(s) {
		return nil, false
	}
	return signatureEnv(s), true
}

// fallback is the signature lore passes when git has no identity: the
// WithIdentity answer when it is usable, else the opener's author.
func (b *Backend) fallback(ctx context.Context) Signature {
	return usable(ctx, b.cfg.identity, b.cfg.author)
}

// usable is f's answer when it names a name and an e-mail git can write
// into an ident line, else def.
func usable(ctx context.Context, f func(context.Context) Signature, def Signature) Signature {
	if f == nil {
		return def
	}
	s := f(ctx)
	if !validSignature(s) {
		return def
	}
	return s
}

// forbiddenSignature are characters that would break a git ident line.
const forbiddenSignature = "\n\r<>"

// gitHasIdentity reports whether git names an identity for the clone
// (hasIdentity).
func (b *Backend) gitHasIdentity(ctx context.Context) (bool, error) {
	return hasIdentity(ctx, b.git, b.work)
}

// hasIdentity reports whether git names both a committer name and a
// committer e-mail in dir, from the environment or from config (system,
// global, the repository's own, as git reads them). git's host-name guess
// does not count: on most machines it is refused ("unable to auto-detect
// email address") or names an address nobody reads.
func hasIdentity(ctx context.Context, r runner, dir string) (bool, error) {
	envName, envMail := os.Getenv(envCommitterName), cmp.Or(os.Getenv(envCommitterEmail), os.Getenv(envEmail))
	if envName != "" && envMail != "" {
		return !gitcommit.IsAgentSignature(envName, envMail), nil
	}
	var name, email string
	out, err := r.run(ctx, call{dir: dir, args: []string{"config", "--get-regexp", identityConfigKeys}})
	if err != nil {
		if code, ran := exitCode(err); ran && code == configNotFound {
			return false, nil
		}
		return false, errs.Wrap(errs.Internal, "git: read the commit identity", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		key, value, _ := strings.Cut(line, " ")
		if value = strings.TrimSpace(value); value == "" {
			continue
		}
		switch {
		case strings.HasSuffix(key, ".name"):
			name = value
		case strings.HasSuffix(key, ".email"):
			email = value
		}
	}
	// devco:D-983: an old agent identity in the clone's config ("<person>
	// via <tool>" <…@lore.invalid>, owner bug 12) is no identity: the
	// caller passes the person's instead, so git never signs with it.
	name, email = cmp.Or(envName, name), cmp.Or(envMail, email)
	return name != "" && email != "" && !gitcommit.IsAgentSignature(name, email), nil
}

// IdentityEnv is the FLAG4 identity environment for a commit lore makes
// itself in the git worktree dir outside a Backend (a store's marker or
// migration commit): nil when git has an identity there, else fallback's
// signature (DefaultAuthor when fallback is nil or answers an unusable
// one) as GIT_AUTHOR_*/GIT_COMMITTER_*. git config is never written.
func IdentityEnv(ctx context.Context, dir string, fallback func(context.Context) Signature) ([]string, error) {
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		return nil, err
	}
	has, err := hasIdentity(ctx, runner{bin: bin}, dir)
	if err != nil || has {
		return nil, err
	}
	return signatureEnv(usable(ctx, fallback, DefaultAuthor)), nil
}

// signatureEnv names s as both author and committer.
func signatureEnv(s Signature) []string {
	return append(authorEnv(s), committerEnv(s)...)
}

// authorEnv names s as a commit's author.
func authorEnv(s Signature) []string {
	return []string{envAuthorName + "=" + s.Name, envAuthorEmail + "=" + s.Email}
}

// committerEnv names s as a commit's committer.
func committerEnv(s Signature) []string {
	return []string{envCommitterName + "=" + s.Name, envCommitterEmail + "=" + s.Email}
}

// authorConfigKeys matches the git config keys that name a commit's
// author: author.* first, then user.* (git-config(1)).
const authorConfigKeys = `^(user|author)\.(name|email)$`

// personSignature is the author of a person's own change in a store an
// org identity signs (store.PersonAuthored): the lore identity (`lore
// whoami`, the name and e-mail the org knows the person by, so no personal
// git e-mail lands in an org store, spec 6.5) when it is usable; else the
// author git names for the clone (GIT_AUTHOR_*, author.*, user.*); else
// the opener's author.
func (b *Backend) personSignature(ctx context.Context) (Signature, error) {
	if b.cfg.identity != nil {
		if s := b.cfg.identity(ctx); validSignature(s) {
			return s, nil
		}
	}
	s := Signature{Name: os.Getenv(envAuthorName), Email: os.Getenv(envAuthorEmail)}
	if validSignature(s) {
		return s, nil
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"config", "--get-regexp", authorConfigKeys}})
	if err != nil {
		if code, ran := exitCode(err); !ran || code != configNotFound {
			return Signature{}, errs.Wrap(errs.Internal, "git: read the author identity", err)
		}
	}
	var user, author Signature
	for line := range strings.SplitSeq(string(out), "\n") {
		key, value, _ := strings.Cut(line, " ")
		value = strings.TrimSpace(value)
		switch key {
		case "user.name":
			user.Name = value
		case "user.email":
			user.Email = value
		case "author.name":
			author.Name = value
		case "author.email":
			author.Email = value
		}
	}
	s = Signature{Name: cmp.Or(s.Name, author.Name, user.Name), Email: cmp.Or(s.Email, author.Email, user.Email)}
	if validSignature(s) {
		return s, nil
	}
	return b.cfg.author, nil
}

// dateEnv dates a commit at the configured clock.
func (b *Backend) dateEnv() []string {
	date := "@" + strconv.FormatInt(b.cfg.now().Unix(), 10) + " +0000"
	return []string{"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date}
}

// commitEnv is the environment of a commit or merge in the clone: its
// date and, when lore must supply one, its identity (identityEnv).
func (b *Backend) commitEnv(ctx context.Context, explicit Signature) ([]string, error) {
	id, err := b.identityEnv(ctx, explicit)
	if err != nil {
		return nil, err
	}
	return append(b.dateEnv(), id...), nil
}
