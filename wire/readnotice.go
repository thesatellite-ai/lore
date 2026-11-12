package wire

// readnotice.go — a read command never writes into a person's repo or
// its .git (plain files sweep 3.3, 2026-11-12). When a read finds
// something only a write may fix (git wiring missing, a .gitattributes
// block missing), it says so once per state; the memory of what it said
// lives in the lore home, <home>/state/read-notices/, never in the repo.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// ReadNoticeDir is the folder under <home>/state.
const ReadNoticeDir = "read-notices"

// readNoticeKeyLen is how many hex digits of the key's hash name a file.
const readNoticeKeyLen = 24

// readNoticePath is the file remembering key under home.
func readNoticePath(home, key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(home, localStateDir, ReadNoticeDir, hex.EncodeToString(sum[:])[:readNoticeKeyLen])
}

// ReadNoticeSaid reports whether a read already gave the notice for key
// with this fingerprint.
func ReadNoticeSaid(home, key, fingerprint string) bool {
	if home == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Clean(readNoticePath(home, key)))
	return err == nil && string(b) == fingerprint+"\n"
}

// MarkReadNotice remembers that a read gave the notice for key with this
// fingerprint. Best effort: a lost mark only repeats the notice.
func MarkReadNotice(home, key, fingerprint string) {
	if home == "" {
		return
	}
	p := readNoticePath(home, key)
	if os.MkdirAll(filepath.Dir(p), cacheDirMode) == nil {
		_ = os.WriteFile(p, []byte(fingerprint+"\n"), homeReadmeMode) // best effort, see above
	}
}
