package wire

// LiveRef: the live ref behind `lore live …` (ops/live). Why a hook: the
// live ref machinery and its per-command session (fetch first, push and
// land last) belong to the host binary that wires live rows around every
// command (the release binary's live wiring, Options.LiveRef); wire only
// hands the operations that host's Ref. A host with no live wiring (cmd/lore
// until the live ref is ported into the core) answers E_UNSUPPORTED, the
// same answer v0.1.17 gave where live rows do not apply.

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	opslive "github.com/thesatellite-ai/lore/ops/live"
)

// liveUnsupported is the refusal when no live ref is wired (v0.1.17 text).
const liveUnsupported = "live rows are not available here (not a git repo, or LORE_LIVE=0)"

// LiveRef implements opslive.Env.
func (a *App) LiveRef(ctx context.Context) (opslive.Ref, error) {
	if a.opts.LiveRef == nil {
		return nil, errs.New(errs.Unsupported, liveUnsupported)
	}
	r, err := a.opts.LiveRef(ctx)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errs.New(errs.Unsupported, liveUnsupported)
	}
	return r, nil
}

// liveReady answers a live operation's E_NOT_PROJECT_ROOT / E_UNSUPPORTED
// before Run opens any store (v0.1.17 checked the project first, so a
// folder outside a project never got a store-open error instead).
func (a *App) liveReady(ctx context.Context, name string) error {
	if !opslive.IsOp(name) {
		return nil
	}
	_, err := a.LiveRef(ctx)
	return err
}
