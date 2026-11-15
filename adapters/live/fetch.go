package live

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"
)

// FetchKind says how much a command wants fresh remote state (F3 step 1).
type FetchKind int

const (
	// FetchCached is for an ordinary command: it fetches unless the last fetch is
	// younger than live.fetch_ttl, and never when Autopush is false.
	FetchCached FetchKind = iota
	// FetchAlways is for `lore sync` and for a write whose push was rejected (its
	// compare-and-swap must never act on stale data).
	FetchAlways
)

// FetchInputs are what ShouldFetch decides from.
type FetchInputs struct {
	Now time.Time
	// LastFetch is the stamp of the last fetch (zero = never).
	LastFetch time.Time
	TTL       time.Duration
	Kind      FetchKind
	// Autopush false means lore never touches the network on its own. Live
	// rows pass false only for LORE_LIVE_AUTOPUSH=0: live.autopush: false
	// stops pushes, never fetches (D18 amended). A store passes its own
	// autopush (storepush_standalone.go).
	Autopush bool
	// HasRemote is false for a repo without the live remote.
	HasRemote bool
	// LastFailure is the stamp of the last fetch that could not reach the
	// remote (zero = none since the last good fetch), and Backoff how long
	// a FetchCached command waits after it before it tries again (zero =
	// TTL). A write's TTL is short (orgconf.ForWrite) but its backoff is
	// the read TTL: offline, every command otherwise waited for the
	// network's connect timeout again (devco:U-6, 41 s writes).
	LastFailure time.Time
	Backoff     time.Duration
}

// ShouldFetch is F3 step 1. A stamp in the future (a clock that jumped
// back) counts as stale, so a skewed clock can never stop fetches for good.
func ShouldFetch(in FetchInputs) bool {
	if !in.HasRemote {
		return false
	}
	if in.Kind == FetchAlways {
		return true
	}
	if !in.Autopush {
		return false
	}
	if BackingOff(in) {
		return false
	}
	if in.LastFetch.IsZero() || in.LastFetch.After(in.Now) {
		return true
	}
	return in.Now.Sub(in.LastFetch) >= in.TTL
}

// BackingOff reports whether a FetchCached command skips the network
// because a fetch failed to reach the remote less than Backoff (else TTL)
// ago and nothing reached it since (devco:U-6). A failure stamp in the
// future (a clock that jumped back) never holds fetches back. FetchAlways
// (`lore sync`, a rejected push) always tries.
func BackingOff(in FetchInputs) bool {
	if in.Kind == FetchAlways || in.LastFailure.IsZero() || in.LastFailure.After(in.Now) {
		return false
	}
	if !in.LastFetch.IsZero() && !in.LastFetch.Before(in.LastFailure) {
		return false // a later fetch got through
	}
	wait := in.Backoff
	if wait <= 0 {
		wait = in.TTL
	}
	return in.Now.Sub(in.LastFailure) < wait
}

// fetchFailedFile records the last fetch that could not reach the remote,
// beside fetchStampFile (devco:U-6).
const fetchFailedFile = "fetch-failed"

// FetchFailed reads the last failed-fetch time from stateDir (zero when
// none).
func FetchFailed(stateDir string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, readTrim(filepath.Join(stateDir, fetchFailedFile)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// WriteFetchFailed records a fetch at now that could not reach the remote,
// so the next commands back off (BackingOff) instead of waiting for the
// network again.
func WriteFetchFailed(stateDir string, now time.Time) error {
	return writeState(filepath.Join(stateDir, fetchFailedFile), []byte(now.UTC().Format(time.RFC3339Nano)+"\n"))
}

// ClearFetchFailed removes the failure stamp once a fetch got through.
// Best effort by design: a stamp left behind is older than the good fetch
// stamp, which BackingOff ignores.
func ClearFetchFailed(stateDir string) {
	_ = os.Remove(filepath.Join(stateDir, fetchFailedFile))
}

// fetchStampFile is the shared fetch stamp (§1: .git/lore/, shared by all
// worktrees of the repo).
const fetchStampFile = "fetch-stamp"

// FetchStamp reads the last fetch time from stateDir (zero when never).
func FetchStamp(stateDir string) time.Time {
	s := readTrim(filepath.Join(stateDir, fetchStampFile))
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// WriteFetchStamp records a fetch at now; the remote was reached, so a
// failure stamp goes.
func WriteFetchStamp(stateDir string, now time.Time) error {
	ClearFetchFailed(stateDir)
	return writeState(filepath.Join(stateDir, fetchStampFile), []byte(now.UTC().Format(time.RFC3339Nano)+"\n"))
}

func contentHash(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
