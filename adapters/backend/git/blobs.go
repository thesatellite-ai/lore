package git

import (
	"bytes"
	"context"
	"strconv"
	"strings"

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
// the result maps each found name to its content, a missing one is absent.
// One process for all rows keeps a full read of a big store fast.
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
	for _, name := range names {
		nl := bytes.IndexByte(raw, '\n')
		if nl < 0 {
			return nil, errs.New(errs.Internal, "git: cat-file output ended early")
		}
		header := strings.Fields(string(raw[:nl]))
		raw = raw[nl+1:]
		if len(header) >= 2 && header[len(header)-1] == catFileMissing {
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
	return out, nil
}
