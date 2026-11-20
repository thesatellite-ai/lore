package git

// cloneidentity.go — the git identity of a store clone (owner run
// 2026-11-12: a store clone took the person's global git identity, so a
// hand commit in an org store carried their personal email). A store
// clone is a plain git checkout a person commits in by hand; lore gives
// it the identity lore signs that store's commits with, in the clone's
// own .git/config, and remembers what it wrote in .git/lore/identity:
//
//	open ─▶ setup ─▶ ensureCloneIdentity: only when this open makes the
//	   clone or the run is an explicit setup or sync (WithGitSetUp); any
//	   other open (a read, owner bug 6) leaves it to the clone's first
//	   write or `lore sync` (setUpHandGitOnce), told through
//	   WithCloneIdentitySay
//	   no repo-local user.email ─▶ write WithCloneIdentity(remote)
//	   user.email = the one lore wrote, and the identity changed since
//	        (the org entry was written after the clone, the setup's email
//	        changed) ─▶ write the new one
//	   any other repo-local user.email ─▶ the person's choice: kept (lore
//	        doctor's store_identity check flags it when it differs)
//	   ─▶ IdentitySetUp names what was written; wire prints one line
//
// Only the clone's .git/config is written, never the global or system
// git config. The commits lore makes itself are signed exactly as before
// (identity.go): the org identity for an org store, else git's identity
// for the clone, which is now this one.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Git config keys of a clone's identity.
const (
	ConfigUserName  = "user.name"
	ConfigUserEmail = "user.email"
)

// IdentityRecordFile is the file in a clone's .git/lore that holds the
// user.email lore last wrote into the clone's .git/config.
const IdentityRecordFile = "identity"

// CloneIdentity is the identity a store clone gets, and why that one.
type CloneIdentity struct {
	Signature
	// Why says whose identity it is ("your identity in org acme, the one
	// lore signs this store's commits with").
	Why string
	// Who is Why in a few words ("org acme", "personal store owner",
	// "lore identity") for the one-line notice when lore sets it.
	Who string
	// Was is the user.email lore had written before and replaced ("" for
	// a clone that had none).
	Was string
}

// WithCloneIdentity sets the identity every store clone this opener opens
// gets in its .git/config when it has no repo-local user.email: f is asked
// with the remote url; an unusable answer (no name or email) writes
// nothing.
func WithCloneIdentity(f func(ctx context.Context, remote string) CloneIdentity) Option {
	return func(c *config) { c.cloneIdentity = f }
}

// WithCloneIdentitySay sets who is told when a write or an explicit sync
// gave an existing clone its identity (an open leaves that to them, owner
// bug 6: a read writes nothing into the clone); a clone lore makes, or an
// explicit setup's open, reports it through IdentitySetUp instead.
func WithCloneIdentitySay(f func(ctx context.Context, dir string, id CloneIdentity)) Option {
	return func(c *config) { c.identitySay = f }
}

// IdentitySetUp is the identity this open wrote into the clone's
// .git/config; ok is false when it wrote none.
func (b *Backend) IdentitySetUp() (CloneIdentity, bool) {
	return b.identitySet, !b.identitySet.IsZero()
}

// LocalConfig is the clone's own (repo-local) value of a git config key,
// "" when unset.
func LocalConfig(ctx context.Context, dir, key string) (string, error) {
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		return "", err
	}
	return localConfig(ctx, runner{bin: bin}, dir, key)
}

// localConfig is LocalConfig over r.
func localConfig(ctx context.Context, r runner, dir, key string) (string, error) {
	out, err := r.run(ctx, call{dir: dir, args: []string{"config", "--local", "--get", key}})
	if err != nil {
		if code, ran := exitCode(err); ran && code == configNotFound {
			return "", nil
		}
		return "", errs.Wrap(errs.Internal, "git: read "+key+" of "+dir, err)
	}
	return trimmed(out), nil
}

// SetLocalIdentity writes name and email into the clone's own .git/config
// (never the global one) and records the email as lore's.
func SetLocalIdentity(ctx context.Context, dir string, s Signature) error {
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		return err
	}
	return setLocalIdentity(ctx, runner{bin: bin}, dir, s)
}

// setLocalIdentity is SetLocalIdentity over r.
func setLocalIdentity(ctx context.Context, r runner, dir string, s Signature) error {
	for _, kv := range [][2]string{{ConfigUserName, s.Name}, {ConfigUserEmail, s.Email}} {
		if _, err := r.run(ctx, call{dir: dir, args: []string{"config", "--local", kv[0], kv[1]}}); err != nil {
			return errs.Wrap(errs.Internal, "git: set "+kv[0]+" in "+dir+"/.git/config", err)
		}
	}
	state := StateDirOf(dir)
	if err := os.MkdirAll(state, cloneDirMode); err != nil {
		return errs.FileIO("create the clone state folder", state, err)
	}
	p := filepath.Join(state, IdentityRecordFile)
	if err := os.WriteFile(p, []byte(s.Email+"\n"), 0o600); err != nil {
		return errs.FileIO("write", p, err)
	}
	return nil
}

// RecordedIdentity is the user.email lore last wrote into the clone at
// dir ("" when none is recorded): a repo-local user.email equal to it is
// lore's to update, any other is the person's (wire's set-url carry asks).
func RecordedIdentity(dir string) (string, error) { return recordedIdentity(dir) }

// recordedIdentity is the user.email lore last wrote into the clone at
// dir ("" when none is recorded).
func recordedIdentity(dir string) (string, error) {
	p := filepath.Join(StateDirOf(dir), IdentityRecordFile)
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", errs.FileIO("read", p, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// ensureCloneIdentity gives the clone its identity (see the file comment).
func (b *Backend) ensureCloneIdentity(ctx context.Context, remote string) error {
	if b.cfg.cloneIdentity == nil {
		return nil
	}
	id, set, err := ensureIdentityAt(ctx, b.git, b.work, func() CloneIdentity { return b.cfg.cloneIdentity(ctx, remote) })
	if set {
		b.identitySet = id
	}
	return err
}

// EnsureCloneIdentity gives the store clone at dir the identity id by the
// same rule as a Backend's open (see the file comment): a clone with no
// repo-local user.email, or still the one lore wrote while id changed,
// gets id in its .git/config; a person's own user.email stays. The org
// layer (adapters/orgcfg commitStore) calls it before every commit it
// makes, so a clone it made for a link, lore-org.yaml or an added store's
// marker carries the same git user as one the backend made (devco:D-782:
// the clone `lore org store add` made had the person's global email).
// set reports whether it wrote; id.Was is then the email it replaced.
func EnsureCloneIdentity(ctx context.Context, dir string, id CloneIdentity) (CloneIdentity, bool, error) {
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		return CloneIdentity{}, false, err
	}
	return ensureIdentityAt(ctx, runner{bin: bin}, dir, func() CloneIdentity { return id })
}

// ensureIdentityAt is the one clone identity rule over r: want is asked
// only when the clone's identity is lore's to write.
func ensureIdentityAt(ctx context.Context, r runner, dir string, want func() CloneIdentity) (CloneIdentity, bool, error) {
	cur, err := localConfig(ctx, r, dir, ConfigUserEmail)
	if err != nil {
		return CloneIdentity{}, false, err
	}
	was := ""
	if cur != "" {
		rec, rerr := recordedIdentity(dir)
		if rerr != nil || rec == "" || !strings.EqualFold(rec, cur) {
			return CloneIdentity{}, false, rerr // the person's own identity for this clone stays
		}
		was = cur
	}
	id := want()
	id.Name, id.Email = strings.TrimSpace(id.Name), strings.TrimSpace(id.Email)
	if !validSignature(id.Signature) {
		return CloneIdentity{}, false, nil
	}
	if was != "" {
		name, nerr := localConfig(ctx, r, dir, ConfigUserName)
		if nerr != nil || (strings.EqualFold(was, id.Email) && name == id.Name) {
			return CloneIdentity{}, false, nerr // what lore wrote is still right
		}
	}
	if err := setLocalIdentity(ctx, r, dir, id.Signature); err != nil {
		return CloneIdentity{}, false, err
	}
	id.Was = was
	return id, true, nil
}
