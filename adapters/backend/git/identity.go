package git

import (
	"cmp"
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// Who signs a store clone's commits and merges, the same source a hand
// edit in the clone uses (D-h) wherever that is safe:
//
//	org store (WithSigner)  the clone's git identity when its email is the
//	                        org identity's (one author for lore's commits
//	                        and hand edits; owner report 2026-11-12), else
//	                        the org identity: a personal git email never
//	                        signs an org store's commit (Q-R2-1, spec 6.5)
//	any other store         FLAG4: the clone's git identity when git has
//	                        one, else the lore identity (`lore whoami`,
//	                        WithIdentity), else the opener's author
//	                        (WithAuthor, default DefaultAuthor)
//
// To have lore's org commits carry your git name, give the clone a git
// identity with the org email (an includeIf for ~/.lore/stores, or git
// config in the clone). lore never writes git config: the fallback is passed
// as GIT_AUTHOR_*/GIT_COMMITTER_* on that one git call only.
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
		return signatureEnv(explicit), nil
	}
	// An org store (owner signature): the clone's git identity signs when
	// it uses the org identity's email, as a hand edit there does (D-h),
	// so lore's commits and the person's carry one author (owner report
	// 2026-11-12: the org display name from lore beside the git user name from
	// git, one email); any other git identity (a personal email) never
	// signs an org store's commit (Q-R2-1, LORE_TEAM_SPEC 6.5): the org
	// identity does.
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
		if sameEmail(gitEmail(ctx, b.git, b.work), s.Email) {
			return nil, nil
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
// signs the store's commits and merges unless the clone's git identity
// uses the same email (then git signs). A zero or unusable signature is
// ignored (the FLAG4 rule).
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
// (outside a Backend) in an org store signed by s: nil when the git
// identity of dir uses s's email (git signs, as for a hand edit), else s
// as GIT_AUTHOR_*/GIT_COMMITTER_*. ok is false when s is not usable; the
// caller then applies the FLAG4 rule (IdentityEnv).
func SignerEnvFor(ctx context.Context, dir string, s Signature) (env []string, ok bool) {
	if !validSignature(s) {
		return nil, false
	}
	if bin, err := lookGit(DefaultBinary); err == nil && sameEmail(gitEmail(ctx, runner{bin: bin}, dir), s.Email) {
		return nil, true
	}
	return signatureEnv(s), true
}

// SignerEnv is s as GIT_AUTHOR_*/GIT_COMMITTER_*; ok is false when s is
// not usable.
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

// gitEmail is the committer email git uses in dir ("" when none): the
// environment first, then config, as git reads them.
func gitEmail(ctx context.Context, r runner, dir string) string {
	if e := os.Getenv(envCommitterEmail); e != "" {
		return e
	}
	out, err := r.run(ctx, call{dir: dir, args: []string{"config", "--get", "user.email"}})
	if err != nil {
		return os.Getenv(envEmail)
	}
	return strings.TrimSpace(string(out))
}

// sameEmail reports whether a and b are one address (case-insensitive).
func sameEmail(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	return a != "" && strings.EqualFold(a, b)
}
