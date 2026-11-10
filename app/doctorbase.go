package app

// doctorbase.go — the base section of `lore doctor` on cmd/lore (identity
// and PATH, wire.App.CoreDoctorBase), handed to the app through
// wire.Options.Context.Doctor. Like lateRender, the host needs the app,
// which is built from the options, so the options get lateDoctor and the
// app is set right after wire.New.

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	"github.com/thesatellite-ai/lore/wire"
)

// lateDoctor forwards opsdoctor.Host to the app set after it is built.
type lateDoctor struct{ app *wire.App }

// Base implements opsdoctor.Host (fix: the reversed --supersedes links).
func (l *lateDoctor) Base(ctx context.Context, fix bool) (opsdoctor.Base, error) {
	if l.app == nil {
		return opsdoctor.Base{}, errs.New(errs.Internal, "doctor host not set")
	}
	return l.app.CoreDoctorBase(ctx, fix)
}
