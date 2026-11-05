// Package gitremote is a forwarding shell: the code moved to github.com/thesatellite-ai/lore/adapters/gitremote
// (PASS3 P3-PORT-LIVE). Every exported name here is an alias or a thin
// wrapper, so the old importers need no edit; new code imports the root
// package directly.
package gitremote

import (
	"context"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	root "github.com/thesatellite-ai/lore/adapters/gitremote"
)

// FaultRemote forwards to the root package.
type FaultRemote = root.FaultRemote

// FetchRequest forwards to the root package.
type FetchRequest = root.FetchRequest

// Git forwards to the root package.
type Git = root.Git

// PushRequest forwards to the root package.
type PushRequest = root.PushRequest

// PushResult forwards to the root package.
type PushResult = root.PushResult

// RefResult forwards to the root package.
type RefResult = root.RefResult

// RefUpdate forwards to the root package.
type RefUpdate = root.RefUpdate

// RefspecState forwards to the root package.
type RefspecState = root.RefspecState

// Remote forwards to the root package.
type Remote = root.Remote

// Status forwards to the root package.
type Status = root.Status

// TransportError forwards to the root package.
type TransportError = root.TransportError

// UpdateMode forwards to the root package.
type UpdateMode = root.UpdateMode

// Constants forwarded from the root package.
const (
	LoreRefsPrefix       = root.LoreRefsPrefix
	ModeFastForward      = root.ModeFastForward
	ModeLease            = root.ModeLease
	StatusAtomicFailed   = root.StatusAtomicFailed
	StatusNonFastForward = root.StatusNonFastForward
	StatusOK             = root.StatusOK
	StatusProtected      = root.StatusProtected
	StatusRemoteRejected = root.StatusRemoteRejected
	StatusStale          = root.StatusStale
	StatusUpToDate       = root.StatusUpToDate
	TrackingPrefix       = root.TrackingPrefix
)

// Variables forwarded from the root package.
var (
	ErrAtomicUnsupported = root.ErrAtomicUnsupported
	ErrBadRequest        = root.ErrBadRequest
	ErrRemoteRefMissing  = root.ErrRemoteRefMissing
	ErrUnconfirmed       = root.ErrUnconfirmed
)

// EnsureTrackingRefspec forwards to the root package.
func EnsureTrackingRefspec(ctx context.Context, r *gitref.Repo, remote string) (root.RefspecState, error) {
	return root.EnsureTrackingRefspec(ctx, r, remote)
}

// NewFaultRemote forwards to the root package.
func NewFaultRemote(inner root.Remote) *root.FaultRemote { return root.NewFaultRemote(inner) }

// TrackingRef forwards to the root package.
func TrackingRef(remote string, ref string) (string, bool) { return root.TrackingRef(remote, ref) }

// TrackingRefspecs forwards to the root package.
func TrackingRefspecs(remote string, branches ...string) []string {
	return root.TrackingRefspecs(remote, branches...)
}
