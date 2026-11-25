package git

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
)

// catFileMissing is the word `git cat-file --batch` prints for an object
// name it cannot resolve: "<name> missing".
const catFileMissing = "missing"

// catFileHeaderFields is the field count of a found object's header line:
// "<oid> <type> <size>".
const catFileHeaderFields = 3

// readBlobs reads many objects in one `git cat-file --batch` run. names are
// object names such as "<commit>:<path>" or ":2:<path>" (an index stage);
// the result maps each found name to its content. A name that names
// nothing (no such path at that commit, no such stage, no such ref) is
// absent from the result; a name whose object exists in the history but
// not in this clone (a partial clone offline, a damaged clone) is an
// error, never read as "no such row" (fail closed, store growth SG-3a,
// devco:D-997). One process for all rows keeps a full read of a big store
// fast.
func (b *Backend) readBlobs(ctx context.Context, names []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(names))
	if len(names) == 0 {
		return out, nil
	}
	for _, n := range names {
		if strings.ContainsAny(n, "\n\r") {
			return nil, errs.New(errs.BadPath, "git: a path with a line break cannot be read")
		}
	}
	stdin := []byte(strings.Join(names, "\n") + "\n")
	raw, err := b.git.run(ctx, call{dir: b.work, args: []string{"cat-file", "--batch"}, stdin: stdin})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: read row files", err)
	}
	var missing []string
	for _, name := range names {
		nl := bytes.IndexByte(raw, '\n')
		if nl < 0 {
			return nil, errs.New(errs.Internal, "git: cat-file output ended early")
		}
		header := strings.Fields(string(raw[:nl]))
		raw = raw[nl+1:]
		if len(header) >= 2 && header[len(header)-1] == catFileMissing {
			missing = append(missing, name)
			continue
		}
		if len(header) != catFileHeaderFields {
			return nil, errs.New(errs.Internal, "git: unexpected cat-file header")
		}
		size, perr := strconv.Atoi(header[2])
		if perr != nil || size < 0 || size+1 > len(raw) {
			return nil, errs.New(errs.Internal, "git: unexpected cat-file object size")
		}
		out[name] = append([]byte(nil), raw[:size]...)
		raw = raw[size+1:] // the object, then one newline
	}
	if err := b.checkAbsent(ctx, missing); err != nil {
		return nil, err
	}
	return out, nil
}

// checkAbsent tells, for the names cat-file printed "missing" for, a name
// that names nothing from one whose object is not in this clone: git
// prints "missing" for both. A full object id is always an object the
// caller found in a tree, so it is not in the clone. For "<rev>:<path>"
// the trees are read (a blobless clone holds every tree): a path listed
// there names a blob this clone lacks. An index stage (":2:<path>") is
// absent: git writes every stage's blob into the clone when it merges.
func (b *Backend) checkAbsent(ctx context.Context, missing []string) error {
	// "<rev>:./<path>" is relative to b.work, "<rev>:<path>" to the top
	type group struct {
		rev string
		rel bool
	}
	byRev := map[group][]string{}
	var groups []group
	for _, n := range missing {
		if isOID(n) {
			return MissingObject(n)
		}
		rev, path, ok := strings.Cut(n, ":")
		if !ok || rev == "" || path == "" {
			continue // an index stage, or a bare name git could not resolve
		}
		g := group{rev: rev, rel: strings.HasPrefix(path, "./")}
		if _, seen := byRev[g]; !seen {
			groups = append(groups, g)
		}
		byRev[g] = append(byRev[g], path)
	}
	for _, g := range groups {
		rev := g.rev
		tree, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "--verify", "--quiet", rev + "^{tree}"}})
		if err != nil {
			if _, ran := exitCode(err); ran {
				continue // no such commit or ref: every path under it names nothing
			}
			return errs.Wrap(errs.Internal, "git: read row files", err)
		}
		args := []string{"--literal-pathspecs", "ls-tree", "-z"}
		if !g.rel {
			args = append(args, "--full-tree")
		}
		args = append(append(args, trimmed(tree), "--"), byRev[g]...)
		listed, err := b.git.run(ctx, call{dir: b.work, args: args})
		if err != nil {
			return errs.Wrap(errs.Internal, "git: read row files", err)
		}
		for entry := range strings.SplitSeq(strings.TrimSuffix(string(listed), "\x00"), "\x00") {
			meta, path, ok := strings.Cut(entry, "\t")
			if f := strings.Fields(meta); ok && len(f) == 3 && f[1] == typeBlob {
				return MissingObject(rev + ":" + path)
			}
		}
	}
	return nil
}

// typeBlob is the object type ls-tree prints for a file.
const typeBlob = "blob"

// MissingObject is the error for a blob the history names that this clone
// does not hold (name: an object id or "<rev>:<path>"). Every reader of an
// old blob returns it instead of reading the blob as absent. Under LORE_OFFLINE or LORE_NO_FETCH git may not fetch it
// (gitenv sets GIT_NO_LAZY_FETCH): E_OFFLINE, run `lore sync` online.
// Otherwise the clone is damaged or its partial-clone remote is gone.
func MissingObject(name string) error {
	if gitenv.NoFetch(os.Environ()) {
		return errs.New(errs.Offline, "git: "+name+" is not in this store clone and "+gitenv.EnvOffline+" / "+gitenv.EnvNoFetch+" stops git fetching it").
			WithHint("run it again online; `lore sync` fetches what the clone lacks")
	}
	return errs.New(errs.Internal, "git: "+name+" is not in this store clone (a damaged clone, or its remote no longer has it)").
		WithHint("`lore sync` fetches what the clone lacks; `lore doctor` checks the clone")
}
