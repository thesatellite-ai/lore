package git

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Who signs a store clone's commits and merges. Q-R2-1: a store that
// belongs to an org with an identity is always signed by that org
// identity (WithSigner; LORE_TEAM_SPEC 6.5: org commits never carry a
// person's personal git email). Every other store follows owner decision
// FLAG4: the person's own git identity when git has one for the clone, else the
// lore identity (`lore whoami`, WithIdentity), else the opener's author
// (WithAuthor, default DefaultAuthor). lore never writes git config: the fallback is passed
// as GIT_AUTHOR_*/GIT_COMMITTER_* on that one git call only.

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
		return signatureEnv(explicit), nil
	}
	// Q-R2-1: an org store's owner signature beats the person's git
	// identity, because org commits must never carry a personal email.
	if s, ok := b.cfg.ownerSigner(); ok {
		return signatureEnv(s), nil
	}
	has, err := b.gitHasIdentity(ctx)
	if err != nil || has {
		return nil, err
	}
	return signatureEnv(b.fallback(ctx)), nil
}

// WithSigner names the signature that signs every commit and merge of
// the store, whatever git knows (Q-R2-1: the org identity of a store that
// belongs to an org). A zero or unusable signature is ignored, leaving the
// FLAG4 rule (git identity, else WithIdentity, else WithAuthor).
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
// write into an ident line.
func validSignature(s Signature) bool {
	return s.Name != "" && s.Email != "" && !strings.ContainsAny(s.Name+s.Email, forbiddenSignature)
}

// SignerEnv is the identity environment of a commit lore makes itself
// (outside a Backend) in a store whose owner signs it (Q-R2-1, WithSigner):
// s as GIT_AUTHOR_*/GIT_COMMITTER_*, whatever git knows. ok is false when s
// is not usable; the caller then applies the FLAG4 rule (IdentityEnv).
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
	name := os.Getenv(envCommitterName) != ""
	email := os.Getenv(envCommitterEmail) != "" || os.Getenv(envEmail) != ""
	if name && email {
		return true, nil
	}
	out, err := r.run(ctx, call{dir: dir, args: []string{"config", "--get-regexp", identityConfigKeys}})
	if err != nil {
		if code, ran := exitCode(err); ran && code == configNotFound {
			return false, nil
		}
		return false, errs.Wrap(errs.Internal, "git: read the commit identity", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		key, value, _ := strings.Cut(line, " ")
		if strings.TrimSpace(value) == "" {
			continue
		}
		switch {
		case strings.HasSuffix(key, ".name"):
			name = true
		case strings.HasSuffix(key, ".email"):
			email = true
		}
	}
	return name && email, nil
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
	return []string{
		envAuthorName + "=" + s.Name, envAuthorEmail + "=" + s.Email,
		envCommitterName + "=" + s.Name, envCommitterEmail + "=" + s.Email,
	}
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
