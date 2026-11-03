// Package signals holds the short-lived coordination facts of lore for
// teams — task claims and resource holds — on the signals ref
// (`refs/lore/signals`, in a store `refs/lore/signals/<branch>`), as
// specified in LORE_TEAM_SPEC.md F7, §3.4, §7 and fixed rules 10–11.
//
// Why a package of its own: signals behave unlike rows. They change every
// few minutes, matter only while they are fresh, and must never create
// history (design principle 1). So every write is ONE parentless commit
// holding the complete current state, pushed with
// `--force-with-lease=<ref>:<old>`; a lost lease re-runs the decision from a
// fresh read and never merges (TEAM-004). Expired signals are absent to
// readers and dropped by the next writer. Signals never land, are never
// exported to .lore/data and are skipped by the merge driver.
//
// Layout:
//   - signal.go  — the wire model (one JSON file per signal) and paths
//   - board.go   — a parsed signals tree and its queries
//   - expiry.go  — the two-part expiry rule (time AND an unchanged
//     renewed_seq observed for renew_every), plus the observation store
//   - decide.go  — pure decisions: claim, renew, release, revoke, holder
//     fencing, resource hold/release
//   - write.go   — the executor: fetch, decide, parentless commit, lease
//     push (atomic with companion refs), retry, fail closed
//
// The package is a mechanism: which folder a project uses, which repo's
// signals ref a client task uses (D8), and how a fenced-out holder is told
// are decided by callers.
package signals

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// Kind is the kind of one signal file. Closed set (§3.4).
type Kind string

const (
	// KindClaim is a holder's claim on a task or mission (F7).
	KindClaim Kind = "claim"
	// KindResource is a hold on a shared thing such as a test suite (§7.2).
	KindResource Kind = "resource"
)

// Folders inside one project folder of the signals tree (§1 "Files and
// folders": `<folder>/claims/<task-id>.json`, `<folder>/resources/<name>.json`).
const (
	ClaimsDir    = "claims"
	ResourcesDir = "resources"
	// fileExt is the extension of every signal file.
	fileExt = ".json"
)

// Ref names (§1 "Refs").
const (
	// CodeRepoRef is the signals ref of an own or unlinked code repo.
	CodeRepoRef = "refs/lore/signals"
	// storeRefPrefix + <store branch> is a store's signals ref.
	storeRefPrefix = "refs/lore/signals/"
	// CommitMessage is the fixed message of every signals commit (F7 step 3).
	CommitMessage = "lore: signals"
)

// StoreRef returns the signals ref of one store branch. A store branch may
// not contain "/" (fixed rule 3), so refs of two branches never clash.
func StoreRef(branch string) string { return storeRefPrefix + branch }

// Signal is one signal file (§3.4). The JSON field names are the wire
// format shared by every lore binary reading the ref: never rename them.
//
// Invariant: Holder, Session and ClaimToken are always set; Task is set for
// KindClaim, Name for KindResource; Expires is UTC at second precision.
type Signal struct {
	Kind Kind `json:"kind"`
	// Task is the claimed task or mission id (KindClaim).
	Task string `json:"task,omitempty"`
	// Name is the held resource (KindResource).
	Name string `json:"name,omitempty"`
	// Project is the project folder the signal belongs to (informational;
	// the file's folder is authoritative).
	Project string `json:"project,omitempty"`
	// Holder is the holder's actor id.
	Holder string `json:"holder"`
	// HolderLabel is how messages name the holder ("agent:frontend-1" or a
	// handle). Only on signals refs, which never reach a client (D8).
	HolderLabel string `json:"holder_label,omitempty"`
	// Session is the holder's LORE_SESSION; two sessions of one actor are
	// two holders (F7 edge cases).
	Session string `json:"session"`
	// ClaimToken is unique per claim; fencing checks it (TEAM-004).
	ClaimToken string `json:"claim_token"`
	// ClaimEpoch mirrors the task row's claim_epoch at claim time.
	ClaimEpoch int `json:"claim_epoch,omitempty"`
	// Since is when this claim was taken (messages: "held … since 13:58").
	Since time.Time `json:"since"`
	// Expires is the lease end; renewing moves it.
	Expires time.Time `json:"expires"`
	// RenewedSeq counts renewals; expiry needs it unchanged (F7 expire).
	RenewedSeq int `json:"renewed_seq"`
	// WorkBranch is the code branch the holder works on, pushed by notes
	// and the heartbeat daemon so a replacement can fetch it (§7.4).
	WorkBranch string `json:"work_branch,omitempty"`
	// Owns are the repo paths (directory prefixes or exact files) the holder
	// declared with --owns (§7.4 file ownership).
	Owns []string `json:"owns,omitempty"`
	// Host is the machine the holder runs on (messages only).
	Host string `json:"host,omitempty"`
}

// ErrBadSignal means a signal file does not decode or misses required
// fields. Readers skip such files with a warning; writers drop them.
var ErrBadSignal = errors.New("signals: malformed signal file")

// normalizeTime is how every time in a signal is stored: UTC, whole
// seconds, so two binaries render the same instant byte-identically.
func normalizeTime(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

// Encode returns the canonical bytes of s (indented JSON, newline-ended).
func Encode(s Signal) ([]byte, error) {
	s.Since = normalizeTime(s.Since)
	s.Expires = normalizeTime(s.Expires)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return nil, fmt.Errorf("signals: encode %s: %w", s.Task+s.Name, err)
	}
	return buf.Bytes(), nil
}

// Decode parses one signal file and checks its required fields.
func Decode(b []byte) (Signal, error) {
	var s Signal
	if err := json.Unmarshal(b, &s); err != nil {
		return Signal{}, fmt.Errorf("%w: %w", ErrBadSignal, err)
	}
	switch {
	case s.Kind != KindClaim && s.Kind != KindResource:
		return Signal{}, fmt.Errorf("%w: unknown kind %q", ErrBadSignal, s.Kind)
	case s.Kind == KindClaim && s.Task == "":
		return Signal{}, fmt.Errorf("%w: claim without task", ErrBadSignal)
	case s.Kind == KindResource && s.Name == "":
		return Signal{}, fmt.Errorf("%w: resource without name", ErrBadSignal)
	case s.Holder == "" || s.Session == "" || s.ClaimToken == "":
		return Signal{}, fmt.Errorf("%w: holder, session and claim_token are required", ErrBadSignal)
	}
	return s, nil
}

// ClaimPath is the tree path of a claim on id in project folder.
func ClaimPath(folder, id string) string { return path.Join(folder, ClaimsDir, id+fileExt) }

// ResourcePath is the tree path of a hold on name in project folder.
func ResourcePath(folder, name string) string { return path.Join(folder, ResourcesDir, name+fileExt) }

// ErrBadName means a task id, resource name or folder cannot be a path
// segment (empty, ".", "..", or holding "/" or a control character).
var ErrBadName = errors.New("signals: invalid name")

// CheckName validates one path segment used in a signal path. Resource names
// come from users (`lore resource claim <name>`), so they are checked before
// they become a path that could escape the folder.
func CheckName(s string) error {
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\") {
		return fmt.Errorf("%w: %q", ErrBadName, s)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %q has a control character", ErrBadName, s)
		}
	}
	return nil
}

// Key identifies a signal file by folder, kind and subject.
type Key struct {
	Folder string
	Kind   Kind
	// Subject is the task id or resource name.
	Subject string
}

// Path returns the key's tree path.
func (k Key) Path() string {
	if k.Kind == KindResource {
		return ResourcePath(k.Folder, k.Subject)
	}
	return ClaimPath(k.Folder, k.Subject)
}

// ParsePath maps a tree path back to its key; ok is false for any path that
// is not `<folder>/claims/<id>.json` or `<folder>/resources/<name>.json`.
func ParsePath(p string) (Key, bool) {
	dir, file := path.Split(p)
	if !strings.HasSuffix(file, fileExt) {
		return Key{}, false
	}
	subject := strings.TrimSuffix(file, fileExt)
	dir = strings.TrimSuffix(dir, "/")
	folder, sub := path.Split(dir)
	folder = strings.TrimSuffix(folder, "/")
	if folder == "" || subject == "" {
		return Key{}, false
	}
	switch sub {
	case ClaimsDir:
		return Key{Folder: folder, Kind: KindClaim, Subject: subject}, true
	case ResourcesDir:
		return Key{Folder: folder, Kind: KindResource, Subject: subject}, true
	}
	return Key{}, false
}
