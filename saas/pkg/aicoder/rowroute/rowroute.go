// Package rowroute is a forwarding shell: the code moved to github.com/thesatellite-ai/lore/logic/rowroute
// (PASS3 P3-PORT-LIVE). Every exported name here is an alias or a thin
// wrapper, so the old importers need no edit; new code imports the root
// package directly.
package rowroute

import (
	root "github.com/thesatellite-ai/lore/logic/rowroute"
)

// Choice forwards to the root package.
type Choice = root.Choice

// Decision forwards to the root package.
type Decision = root.Decision

// Destination forwards to the root package.
type Destination = root.Destination

// Outcome forwards to the root package.
type Outcome = root.Outcome

// Place forwards to the root package.
type Place = root.Place

// Request forwards to the root package.
type Request = root.Request

// Router forwards to the root package.
type Router = root.Router

// Row forwards to the root package.
type Row = root.Row

// Target forwards to the root package.
type Target = root.Target

// TargetKind forwards to the root package.
type TargetKind = root.TargetKind

// Write forwards to the root package.
type Write = root.Write

// Constants forwarded from the root package.
const (
	ChoiceDefault      = root.ChoiceDefault
	ChoiceLive         = root.ChoiceLive
	ChoiceNoLive       = root.ChoiceNoLive
	PlaceBranch        = root.PlaceBranch
	PlaceLive          = root.PlaceLive
	ReasonBranchRow    = root.ReasonBranchRow
	ReasonCompanion    = root.ReasonCompanion
	ReasonDefaultLive  = root.ReasonDefaultLive
	ReasonDefaultLocal = root.ReasonDefaultLocal
	ReasonDeleteBranch = root.ReasonDeleteBranch
	ReasonFlagLive     = root.ReasonFlagLive
	ReasonFlagNoLive   = root.ReasonFlagNoLive
	ReasonLiveWins     = root.ReasonLiveWins
	TargetPersonal     = root.TargetPersonal
	TargetRepo         = root.TargetRepo
	TargetStore        = root.TargetStore
)

// Variables forwarded from the root package.
var (
	ErrNoDestination = root.ErrNoDestination
)

// Decide forwards to the root package.
func Decide(req Request, row Row) Decision { return root.Decide(req, row) }

// DecideAll forwards to the root package.
func DecideAll(req Request, rows []Row) []Decision { return root.DecideAll(req, rows) }
