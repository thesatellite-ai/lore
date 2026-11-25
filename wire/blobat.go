package wire

import (
	"context"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
)

// blobAt reads the file name names ("<rev>:<path>", "<rev>:./<path>")
// in the checkout top. found=false: rev has no such path, or rev does not
// exist. err: rev's tree names the path but its blob is not in this clone
// (a partial clone offline, a damaged clone); a reader never takes that
// as "no such file" (fail closed, store growth SG-3a, devco:D-997). Why a
// second call: git prints the same failure for both, and rev-parse reads
// trees only, which every clone holds.
func blobAt(ctx context.Context, top, name string) (body []byte, found bool, err error) {
	body, cerr := liveGit(ctx, top, "cat-file", "blob", name)
	if cerr == nil {
		return body, true, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if _, rerr := liveGit(ctx, top, "rev-parse", "--verify", "--quiet", name); rerr == nil {
		return nil, false, gitbackend.MissingObject(name)
	}
	return nil, false, nil
}
