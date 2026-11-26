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
	return b.readBlobsBy(ctx, names, false)
}

// replayBlobs is readBlobs for the roster replay: a read of bulkBlobs or
// more names runs git itself (call.bulk). Why (bug hunt H-279,
// devco:D-1017): the replay reads a chunk's row versions in commit order,
// which is about the pack's own order, so git's delta cache serves them;
// gitfast inflated each one with its delta chain again, 92% of a first
// sync's CPU at 100k commits. Other reads keep gitfast: a doctor's row
// read asks in id order, where git's cache misses and gitfast's object
// cache does better (20k rows of one folder: 4.9 s with gitfast, 18 s
// with git).
func (b *Backend) replayBlobs(ctx context.Context, names []string) (map[string][]byte, error) {
	return b.readBlobsBy(ctx, names, len(names) >= bulkBlobs)
}

// readBlobsBy is readBlobs, run by git itself when bulk is set.
func (b *Backend) readBlobsBy(ctx context.Context, names []string, bulk bool) (map[string][]byte, error) {
	out := make(map[string][]byte, len(names))
	if len(names) == 0 {
		return out, nil
	}
	for _, n := range names {
		if strings.ContainsAny(n, "\n\r") {
			return nil, errs.New(errs.BadPath, "git: a path with a line break cannot be read")
		}
	}
	// a blobless clone (devco:D-1001): cat-file must not fetch each
	// missing blob in its own request; the ones it lacks are fetched
	// below in one request, then read again
	var env []string
	partial := partialCloneAt(b.work)
	if partial {
		env = []string{noLazyFetchEnv}
	}
	missing, err := b.catBatch(ctx, names, env, bulk, out)
	if err != nil {
		return nil, err
	}
	if partial && len(missing) > 0 {
		lacking, aerr := b.absentObjects(ctx, missing)
		if aerr != nil {
			return nil, aerr
		}
		if len(lacking) > 0 {
			oids := make([]string, 0, len(lacking))
			again := make([]string, 0, len(lacking))
			for _, l := range lacking {
				oids = append(oids, l.oid)
				again = append(again, l.name)
			}
			if err := b.fetchMissing(ctx, oids); err != nil {
				return nil, err
			}
			if missing, err = b.catBatch(ctx, again, env, bulk, out); err != nil {
				return nil, err
			}
		} else {
			missing = nil
		}
	}
	if err := b.checkAbsent(ctx, missing); err != nil {
		return nil, err
	}
	return out, nil
}

// noLazyFetchEnv stops git fetching an object a partial clone lacks.
const noLazyFetchEnv = "GIT_NO_LAZY_FETCH=1"

// bulkBlobs is how many names make a replay's blob read a bulk one
// (replayBlobs, call.bulk): git reads them itself. Below it gitfast
// answers in-process and saves the git process (a few row versions of
// one commit).
const bulkBlobs = 256

// catBatch reads names with one `git cat-file --batch` into out and
// returns the names cat-file printed "missing" for.
func (b *Backend) catBatch(ctx context.Context, names, env []string, bulk bool, out map[string][]byte) ([]string, error) {
	stdin := []byte(strings.Join(names, "\n") + "\n")
	raw, err := b.git.run(ctx, call{dir: b.work, args: []string{"cat-file", "--batch"}, env: env, stdin: stdin, bulk: bulk})
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
	return missing, nil
}

// checkAbsent tells, for the names cat-file printed "missing" for, a name
// that names nothing from one whose object is not in this clone: git
// prints "missing" for both. The first name whose object the history
// holds but this clone lacks is MissingObject; names that name nothing
// are absent (nil).
func (b *Backend) checkAbsent(ctx context.Context, missing []string) error {
	lacking, err := b.absentObjects(ctx, missing)
	if err != nil {
		return err
	}
	if len(lacking) > 0 {
		return MissingObject(lacking[0].shown)
	}
	return nil
}

// lackedObject is a name cat-file could not read whose blob the history
// holds: name as asked, oid the blob, shown how errors name it.
type lackedObject struct {
	name, oid, shown string
}

// absentObjects returns, among the names cat-file printed "missing" for,
// those whose blob exists in the history but not in this clone. A full
// object id is always an object the caller found in a tree, so it is not
// in the clone. For "<rev>:<path>" the trees are read (a blobless clone
// holds every tree): a path listed there names a blob this clone lacks.
// An index stage (":2:<path>") is absent: git writes every stage's blob
// into the clone when it merges.
func (b *Backend) absentObjects(ctx context.Context, missing []string) ([]lackedObject, error) {
	// "<rev>:./<path>" is relative to b.work, "<rev>:<path>" to the top
	type group struct {
		rev string
		rel bool
	}
	byRev := map[group][]string{}
	var groups []group
	var lacking []lackedObject
	for _, n := range missing {
		if isOID(n) {
			lacking = append(lacking, lackedObject{name: n, oid: n, shown: n})
			continue
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
			return nil, errs.Wrap(errs.Internal, "git: read row files", err)
		}
		args := []string{"--literal-pathspecs", "ls-tree", "-z"}
		if !g.rel {
			args = append(args, "--full-tree")
		}
		args = append(append(args, trimmed(tree), "--"), byRev[g]...)
		listed, err := b.git.run(ctx, call{dir: b.work, args: args})
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "git: read row files", err)
		}
		for entry := range strings.SplitSeq(strings.TrimSuffix(string(listed), "\x00"), "\x00") {
			meta, path, ok := strings.Cut(entry, "\t")
			if f := strings.Fields(meta); ok && len(f) == 3 && f[1] == typeBlob {
				name := rev + ":" + path
				if g.rel {
					name = rev + ":./" + path
				}
				lacking = append(lacking, lackedObject{name: name, oid: f[2], shown: rev + ":" + path})
			}
		}
	}
	return lacking, nil
}

// typeBlob is the object type ls-tree prints for a file.
const typeBlob = "blob"

// MissingObject is the error for a blob the history names that this clone
// does not hold (name: an object id or "<rev>:<path>"). Every reader of an
// old blob returns it instead of reading the blob as absent. Under LORE_OFFLINE or LORE_NO_FETCH git may not fetch it
// (gitenv sets GIT_NO_LAZY_FETCH): E_OFFLINE, run it online (or `lore
// store prefetch` before going offline, devco:D-1001).
// Otherwise the clone is damaged or its partial-clone remote is gone.
func MissingObject(name string) error {
	if gitenv.NoFetch(os.Environ()) {
		return errs.New(errs.Offline, "git: "+name+" is not in this store clone and "+gitenv.EnvOffline+" / "+gitenv.EnvNoFetch+" stops git fetching it").
			WithHint("run it again online, where lore fetches it; to read old versions offline, run `lore store prefetch --store <name>` while online first (lore sync fetches new history only)")
	}
	return errs.New(errs.Internal, "git: "+name+" is not in this store clone (a damaged clone, or its remote no longer has it)").
		WithHint("`lore sync` fetches what the clone lacks; `lore doctor` checks the clone")
}
