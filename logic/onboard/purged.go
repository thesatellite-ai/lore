package onboard

// purged.go — the tombstone a permanent delete leaves (owner report
// 2026-11-07): after `lore setup trash purge personal` the import scan
// found the identity and personal.store still in config.yaml (a purge
// keeps them unless asked) and offered the deleted setup straight back.
// A purge now records one trash.purged entry, and every import proposal it
// matches is left out. The entry holds the setup's name, kind and org in
// clear, and only fingerprints of its identity key and personal store, so
// a purge with --clear-identity / --clear-personal-store keeps neither
// value in the file and can still recognise them when an old file or a
// repo's stores.yaml brings them back.

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// PurgedSetup is one trash.purged entry: what a permanently deleted setup
// was, enough to recognise its import proposal and nothing more.
type PurgedSetup struct {
	Name string `yaml:"name" json:"name"`
	Kind Kind   `yaml:"kind" json:"kind"`
	// Org is a member/admin setup's org (orgs[] keeps it in clear anyway).
	Org string `yaml:"org,omitempty" json:"org,omitempty"`
	// IdentitySHA and StoreSHA are IdentityFingerprint / StoreFingerprint
	// of the solo setup's effective identity key and personal store at the
	// purge ("" when it had none).
	IdentitySHA string `yaml:"identity_sha,omitempty" json:"identity_sha,omitempty"`
	StoreSHA    string `yaml:"store_sha,omitempty" json:"store_sha,omitempty"`
	// GitHubSHA is GitHubFingerprint of the GitHub login the setup worked
	// as (github.user; for a solo setup without one, its identity handle,
	// the login an import stands for), "" when it had none. Home stops
	// suggesting a new setup for that login (issue 6); an entry written
	// before this field has none.
	GitHubSHA string `yaml:"github_sha,omitempty" json:"github_sha,omitempty"`
	// PurgedAt is when the purge ran, RFC 3339 UTC.
	PurgedAt string `yaml:"purged_at" json:"purged_at"`
}

// fingerprintLen is how many hex characters a fingerprint keeps: enough
// to tell two identities or stores apart, short enough to read; it is a
// recognition mark, not a secret.
const fingerprintLen = 16

// IdentityFingerprint is the fingerprint of an identity key (case and
// surrounding space ignored, as identity keys compare); "" for "".
func IdentityFingerprint(key string) string {
	return fingerprint(strings.ToLower(strings.TrimSpace(key)))
}

// StoreFingerprint is the fingerprint of a store url (a trailing slash and
// .git ignored, as store urls compare); "" for "", inherit and none.
func StoreFingerprint(url string) string {
	u := strings.TrimSpace(url)
	if u == PersonalStoreInherit || u == PersonalStoreNone {
		return ""
	}
	return fingerprint(strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git"))
}

// GitHubFingerprint is the fingerprint of a GitHub login (case and
// surrounding space ignored, as GitHub logins compare); "" for "".
func GitHubFingerprint(login string) string {
	return fingerprint(strings.ToLower(strings.TrimSpace(login)))
}

// fingerprint is the short sha256 hex of s, "" for "".
func fingerprint(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:fingerprintLen]
}

// PurgedOf is the tombstone of trashed setup t purged at at (RFC 3339).
// identity and shared are config.yaml's top-level identity key and
// personal.store at the purge: a solo setup with no identity of its own,
// or an inherit store, used those.
func PurgedOf(t TrashedSetup, identity, shared, at string) PurgedSetup {
	s := t.Setup
	p := PurgedSetup{Name: s.Name, Kind: s.Kind, Org: s.Org, PurgedAt: at}
	login := s.GitHub.User
	if strings.TrimSpace(login) == "" && s.Kind == KindSolo {
		login = s.Identity.Handle
	}
	p.GitHubSHA = GitHubFingerprint(login)
	if s.Kind != KindSolo {
		return p
	}
	key := s.Identity.Key
	if strings.TrimSpace(key) == "" {
		key = identity
	}
	store := strings.TrimSpace(s.PersonalStore)
	if store == "" || store == PersonalStoreInherit {
		store = shared
	}
	p.IdentitySHA, p.StoreSHA = IdentityFingerprint(key), StoreFingerprint(store)
	return p
}
