package live

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// FetchKind says how much a command wants fresh remote state (F3 step 1).
type FetchKind int

const (
	// FetchCached is for an ordinary command: it fetches unless the last fetch is
	// younger than live.fetch_ttl, and never when autopush is off.
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
	// Autopush false (LORE_LIVE_AUTOPUSH=0 / live.autopush: false) means
	// lore never touches the network on its own.
	Autopush bool
	// HasRemote is false for a repo without the live remote.
	HasRemote bool
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
	if in.LastFetch.IsZero() || in.LastFetch.After(in.Now) {
		return true
	}
	return in.Now.Sub(in.LastFetch) >= in.TTL
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

// ClearFetchStamp removes the fetch stamp, so the next FetchCached
// command fetches (and so pushes) whatever its age; a missing stamp is no
// error.
func ClearFetchStamp(stateDir string) error {
	err := os.Remove(filepath.Join(stateDir, fetchStampFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// WriteFetchStamp records a fetch at now.
func WriteFetchStamp(stateDir string, now time.Time) error {
	return writeState(filepath.Join(stateDir, fetchStampFile), []byte(now.UTC().Format(time.RFC3339Nano)+"\n"))
}

func contentHash(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
