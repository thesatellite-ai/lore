// Package signals is a forwarding shell: the code moved to github.com/thesatellite-ai/lore/adapters/signals
// (PASS3 P3-PORT-LIVE). Every exported name here is an alias or a thin
// wrapper, so the old importers need no edit; new code imports the root
// package directly.
package signals

import (
	root "github.com/thesatellite-ai/lore/adapters/signals"
	"time"
)

// Board forwards to the root package.
type Board = root.Board

// Caller forwards to the root package.
type Caller = root.Caller

// Change forwards to the root package.
type Change = root.Change

// ClaimPlan forwards to the root package.
type ClaimPlan = root.ClaimPlan

// ClaimRequest forwards to the root package.
type ClaimRequest = root.ClaimRequest

// Companion forwards to the root package.
type Companion = root.Companion

// CryptoJitter forwards to the root package.
type CryptoJitter = root.CryptoJitter

// CryptoTokens forwards to the root package.
type CryptoTokens = root.CryptoTokens

// Decide forwards to the root package.
type Decide = root.Decide

// Held forwards to the root package.
type Held = root.Held

// HeldError forwards to the root package.
type HeldError = root.HeldError

// HoldRequest forwards to the root package.
type HoldRequest = root.HoldRequest

// Jitter forwards to the root package.
type Jitter = root.Jitter

// Key forwards to the root package.
type Key = root.Key

// Kind forwards to the root package.
type Kind = root.Kind

// Lease forwards to the root package.
type Lease = root.Lease

// NotHeldError forwards to the root package.
type NotHeldError = root.NotHeldError

// Observation forwards to the root package.
type Observation = root.Observation

// Observations forwards to the root package.
type Observations = root.Observations

// OfflineError forwards to the root package.
type OfflineError = root.OfflineError

// RemoteCompanion forwards to the root package.
type RemoteCompanion = root.RemoteCompanion

// Result forwards to the root package.
type Result = root.Result

// RowState forwards to the root package.
type RowState = root.RowState

// SeenStore forwards to the root package.
type SeenStore = root.SeenStore

// Signal forwards to the root package.
type Signal = root.Signal

// Store forwards to the root package.
type Store = root.Store

// Tokens forwards to the root package.
type Tokens = root.Tokens

// View forwards to the root package.
type View = root.View

// WIPError forwards to the root package.
type WIPError = root.WIPError

// Constants forwarded from the root package.
const (
	ClaimsDir     = root.ClaimsDir
	CodeRepoRef   = root.CodeRepoRef
	CommitMessage = root.CommitMessage
	KindClaim     = root.KindClaim
	KindResource  = root.KindResource
	ResourcesDir  = root.ResourcesDir
)

// Variables forwarded from the root package.
var (
	ErrBadName   = root.ErrBadName
	ErrBadSignal = root.ErrBadSignal
	ErrNotHeld   = root.ErrNotHeld
	ErrOffline   = root.ErrOffline
	ErrPartial   = root.ErrPartial
	ErrRejected  = root.ErrRejected
	ErrWIPLimit  = root.ErrWIPLimit
)

// CheckHolder forwards to the root package.
func CheckHolder(v root.View, c root.Caller, folder string, id string, rowEpoch int) (root.Signal, error) {
	return root.CheckHolder(v, c, folder, id, rowEpoch)
}

// CheckName forwards to the root package.
func CheckName(s string) error { return root.CheckName(s) }

// ClaimPath forwards to the root package.
func ClaimPath(folder string, id string) string { return root.ClaimPath(folder, id) }

// DecideClaim forwards to the root package.
func DecideClaim(v root.View, req root.ClaimRequest) (root.ClaimPlan, error) {
	return root.DecideClaim(v, req)
}

// DecideHold forwards to the root package.
func DecideHold(v root.View, req root.HoldRequest) (root.Signal, error) {
	return root.DecideHold(v, req)
}

// DecideRelease forwards to the root package.
func DecideRelease(v root.View, c root.Caller, folder string, id string) (root.Signal, error) {
	return root.DecideRelease(v, c, folder, id)
}

// DecideRenew forwards to the root package.
func DecideRenew(v root.View, c root.Caller, folder string, id string) (root.Signal, error) {
	return root.DecideRenew(v, c, folder, id)
}

// DecideUnhold forwards to the root package.
func DecideUnhold(v root.View, c root.Caller, folder string, name string) (string, error) {
	return root.DecideUnhold(v, c, folder, name)
}

// Decode forwards to the root package.
func Decode(b []byte) (root.Signal, error) { return root.Decode(b) }

// Encode forwards to the root package.
func Encode(s root.Signal) ([]byte, error) { return root.Encode(s) }

// ExpiredForHolder forwards to the root package.
func ExpiredForHolder(s root.Signal, now time.Time) bool { return root.ExpiredForHolder(s, now) }

// FindOwn forwards to the root package.
func FindOwn(v root.View, c root.Caller, folder string, id string) (root.Signal, bool) {
	return root.FindOwn(v, c, folder, id)
}

// ParseBoard forwards to the root package.
func ParseBoard(tree map[string][]byte) (root.Board, []string) { return root.ParseBoard(tree) }

// ParsePath forwards to the root package.
func ParsePath(p string) (root.Key, bool) { return root.ParsePath(p) }

// ResourcePath forwards to the root package.
func ResourcePath(folder string, name string) string { return root.ResourcePath(folder, name) }

// StoreRef forwards to the root package.
func StoreRef(branch string) string { return root.StoreRef(branch) }
