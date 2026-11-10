package coord

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/thesatellite-ai/lore/adapters/osrename"
)

// HeldRecord is what one session remembers about a claim it took, kept in
// the clone's machine state (`.git/lore/held.json`, §1).
//
// Why it exists: the signals ref says who holds a task NOW; after a human
// override the holder's claim is simply gone. To tell the holder why
// (E_CANCELLED / E_REASSIGNED / E_LOST_CLAIM, §7.4) lore compares the row
// with what the holder saw when it claimed. The commit-msg hook also reads
// it, offline, to add the `Lore-Task:` trailer while a claim is held (F9).
// It is a cache of the holder's own past, never a source of truth for
// fencing: fencing always reads the signals ref.
type HeldRecord struct {
	Task string `json:"task"`
	// Folder and Ref locate the claim (project folder, signals ref).
	Folder string `json:"folder"`
	Ref    string `json:"ref"`
	Token  string `json:"token"`
	Epoch  int    `json:"epoch"`
	// Assignee is the row's assignee when the claim was taken.
	Assignee string `json:"assignee,omitempty"`
	// Repo is the code repo the task belongs to ("" = any).
	Repo string `json:"repo,omitempty"`
	// InRepo is true when the task row lives in this checkout's repo (its
	// own project, or its linked client task row). Only then may the
	// commit-msg hook write `Lore-Task: <id>` (coordinator Q13, fail
	// closed: a store-only task's id never goes into a client commit).
	// Records written before this field read false: no trailer.
	InRepo  bool      `json:"in_repo,omitempty"`
	Claimed time.Time `json:"claimed"`
	Expires time.Time `json:"expires"`
}

// HeldStore persists HeldRecords per session.
type HeldStore struct {
	// Path is the JSON file (e.g. <git dir>/lore/held.json); "" disables it.
	Path string
}

type heldFile struct {
	Sessions map[string][]HeldRecord `json:"sessions"`
}

func (h HeldStore) read() heldFile {
	f := heldFile{Sessions: map[string][]HeldRecord{}}
	if h.Path == "" {
		return f
	}
	b, err := os.ReadFile(h.Path)
	if err != nil {
		return f
	}
	if json.Unmarshal(b, &f) != nil || f.Sessions == nil {
		return heldFile{Sessions: map[string][]HeldRecord{}}
	}
	return f
}

// heldDirMode is the mode of the held.json directory (<git dir>/lore):
// machine state of this user only, never world-readable.
const heldDirMode = 0o750

// heldMaxAge drops records of claims that expired long ago, so the file
// stays bounded even when a session never released (principle 1).
const heldMaxAge = 7 * 24 * time.Hour

func (h HeldStore) write(f heldFile, now time.Time) error {
	if h.Path == "" {
		return nil
	}
	for s, recs := range f.Sessions {
		kept := recs[:0]
		for _, r := range recs {
			if now.Sub(r.Expires) < heldMaxAge {
				kept = append(kept, r)
			}
		}
		if len(kept) == 0 {
			delete(f.Sessions, s)
		} else {
			f.Sessions[s] = kept
		}
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("coord: encode held: %w", err)
	}
	if err = os.MkdirAll(filepath.Dir(h.Path), heldDirMode); err != nil {
		return fmt.Errorf("coord: held dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(h.Path), ".held-*")
	if err != nil {
		return fmt.Errorf("coord: held temp: %w", err)
	}
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name()) // a leftover temp file is harmless; the write error is the one to report
		return fmt.Errorf("coord: write held: %w", errors.Join(werr, cerr))
	}
	if err := osrename.Rename(tmp.Name(), h.Path); err != nil {
		_ = os.Remove(tmp.Name()) // a leftover temp file is harmless; the write error is the one to report
		return fmt.Errorf("coord: save held: %w", err)
	}
	return nil
}

// Get returns the session's record for task.
func (h HeldStore) Get(session, task string) (HeldRecord, bool) {
	for _, r := range h.read().Sessions[session] {
		if r.Task == task {
			return r, true
		}
	}
	return HeldRecord{}, false
}

// List returns the session's records, newest claim first.
func (h HeldStore) List(session string) []HeldRecord {
	recs := append([]HeldRecord(nil), h.read().Sessions[session]...)
	sort.Slice(recs, func(i, j int) bool { return recs[i].Claimed.After(recs[j].Claimed) })
	return recs
}

// Put stores or replaces the session's record for r.Task.
func (h HeldStore) Put(session string, r HeldRecord, now time.Time) error {
	f := h.read()
	recs := f.Sessions[session]
	out := recs[:0]
	for _, x := range recs {
		if x.Task != r.Task {
			out = append(out, x)
		}
	}
	f.Sessions[session] = append(out, r)
	return h.write(f, now)
}

// Drop forgets the session's record for task.
func (h HeldStore) Drop(session, task string, now time.Time) error {
	f := h.read()
	recs := f.Sessions[session]
	out := recs[:0]
	for _, x := range recs {
		if x.Task != task {
			out = append(out, x)
		}
	}
	f.Sessions[session] = out
	return h.write(f, now)
}
