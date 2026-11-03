package signals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Lease is the claim timing (§1 config keys `lease.*`), already merged from
// defaults, top-level config and the org entry by the caller.
type Lease struct {
	// TTL is how long a claim or renewal holds (default 20m).
	TTL time.Duration
	// RenewEvery is the heartbeat interval, and also how long a reader must
	// have seen an unchanged renewed_seq before it may treat a claim as
	// expired (default 5m).
	RenewEvery time.Duration
	// Grace is extra time past Expires before others may reclaim (2m).
	Grace time.Duration
	// PushRetries bounds re-decisions after lost leases (5).
	PushRetries int
}

// Observation is what one clone saw of one signal file: which claim
// (token), which renewal (seq), and since when it has seen exactly that.
//
// Why it exists: expiry must not trust the reader's clock alone. A reader
// whose clock runs 30 minutes fast would otherwise steal every lease. A
// claim is expired only when the time check passes on the reader's clock
// AND the reader itself has watched the same renewed_seq for at least
// renew_every — a holder that keeps renewing changes the seq and resets the
// watch, whatever the clocks say (F7 expire, "clock skew").
type Observation struct {
	Token string    `json:"token"`
	Seq   int       `json:"seq"`
	Since time.Time `json:"since"`
}

// Observations maps a signal's tree path to what this clone saw of it.
type Observations map[string]Observation

// Observe records board as seen at now: a signal with the same token and
// seq keeps its Since; anything new or renewed starts a fresh watch; paths
// gone from the board are forgotten, so the map never grows past the
// board's size (principle 1). It returns a new map and does not modify o.
func (o Observations) Observe(b Board, now time.Time) Observations {
	out := make(Observations, len(b))
	for p, s := range b {
		prev, ok := o[p]
		if ok && prev.Token == s.ClaimToken && prev.Seq == s.RenewedSeq {
			out[p] = prev
			continue
		}
		out[p] = Observation{Token: s.ClaimToken, Seq: s.RenewedSeq, Since: normalizeTime(now)}
	}
	return out
}

// Expired reports whether a reader may treat the signal at p as absent:
// now is past Expires + Grace on the reader's clock AND the reader has seen
// the same token and renewed_seq for at least RenewEvery. A signal the
// reader has never observed is not expired yet (the watch starts now): the
// cost is one renew interval of delay, never a stolen lease.
func (o Observations) Expired(p string, s Signal, now time.Time, l Lease) bool {
	if !now.After(s.Expires.Add(l.Grace)) {
		return false
	}
	ob, ok := o[p]
	if !ok || ob.Token != s.ClaimToken || ob.Seq != s.RenewedSeq {
		return false
	}
	return now.Sub(ob.Since) >= l.RenewEvery
}

// ExpiredForHolder reports whether the holder itself must consider its
// claim gone: past Expires on its own clock (F7 renew: "not expired").
func ExpiredForHolder(s Signal, now time.Time) bool { return now.After(s.Expires) }

// SeenStore persists Observations for one clone. The file lives in the
// clone's machine-state folder (`.git/lore/`, §1), never in the work tree.
type SeenStore struct {
	// Path is the JSON file, e.g. <git dir>/lore/signals-seen.json.
	Path string
}

// file is the on-disk shape: observations per signals ref, so a repo that
// reads both its own signals ref and a store's keeps them apart.
type seenFile struct {
	Refs map[string]Observations `json:"refs"`
}

// Load returns the observations recorded for ref; a missing or unreadable
// file is an empty set (the only cost is a later expiry, never an early one).
func (s SeenStore) Load(_ context.Context, ref string) Observations {
	f := s.read()
	if o, ok := f.Refs[ref]; ok {
		return o
	}
	return Observations{}
}

func (s SeenStore) read() seenFile {
	b, err := os.ReadFile(s.Path)
	if err != nil {
		return seenFile{Refs: map[string]Observations{}}
	}
	var f seenFile
	if json.Unmarshal(b, &f) != nil || f.Refs == nil {
		return seenFile{Refs: map[string]Observations{}}
	}
	return f
}

// seenFileMode keeps machine state private to the user.
const seenFileMode = 0o600

// Save stores o for ref, atomically (temp file + rename) so a concurrent
// lore command in the same clone reads either the old or the new file.
func (s SeenStore) Save(_ context.Context, ref string, o Observations) error {
	f := s.read()
	f.Refs[ref] = o
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("signals: encode observations: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return fmt.Errorf("signals: observations dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".signals-seen-*")
	if err != nil {
		return fmt.Errorf("signals: observations temp: %w", err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(name) // a leftover temp file is harmless; the write error is the one to report
		return fmt.Errorf("signals: write observations: %w", errors.Join(werr, cerr))
	}
	if err := os.Chmod(name, seenFileMode); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = os.Remove(name) // a leftover temp file is harmless; the write error is the one to report
		return fmt.Errorf("signals: chmod observations: %w", err)
	}
	if err := os.Rename(name, s.Path); err != nil {
		_ = os.Remove(name) // a leftover temp file is harmless; the write error is the one to report
		return fmt.Errorf("signals: save observations: %w", err)
	}
	return nil
}

// sortedPaths returns the board's paths in a stable order (for output and
// deterministic decisions).
func sortedPaths(b Board) []string {
	ps := make([]string, 0, len(b))
	for p := range b {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return ps
}
