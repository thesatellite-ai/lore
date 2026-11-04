package rest

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Token file constants (spec 9.7).
const (
	// TokenFile is the token's file name inside lore's home (~/.lore).
	TokenFile = "api-token"
	// TokenBytes is the token's random length; the file holds it as hex.
	TokenBytes = 32
	// tokenMode is the only mode lore writes or accepts for the token file:
	// a token readable by other users would let them call the API as you.
	tokenMode fs.FileMode = 0o600
	// homeMode is the mode of a home directory lore creates.
	homeMode fs.FileMode = 0o700
	// groupOtherBits are the permission bits that must be clear.
	groupOtherBits fs.FileMode = 0o077
	// tempFilePattern names the temporary file a new token is written to
	// before the atomic rename.
	tempFilePattern = ".api-token-*"
)

// TokenPath is the token file of a lore home directory.
func TokenPath(home string) string { return filepath.Join(home, TokenFile) }

// LoadOrCreateToken returns the API token of home, creating it on first use
// (32 random bytes, hex, mode 0600, written atomically).
//
// Invariant: an existing token file is used only when it is a regular file
// with no group or other permission bits and holds exactly one hex token;
// anything else is refused (E_CONFIG_INVALID), never silently fixed, so a
// file someone else could read or replace is never trusted.
func LoadOrCreateToken(home string) (string, error) {
	tok, err := readToken(home)
	if errs.Has(err, errs.NotFound) {
		return RotateToken(home)
	}
	return tok, err
}

// RotateToken replaces home's token with a new random one (`lore api token
// rotate`) and returns it. Every client holding the old token gets 401 from
// then on.
func RotateToken(home string) (string, error) {
	raw := make([]byte, TokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", errs.Wrap(errs.Internal, "rest: read random bytes", err)
	}
	tok := hex.EncodeToString(raw)
	if err := os.MkdirAll(home, homeMode); err != nil {
		return "", errs.Wrap(errs.Internal, "rest: create "+home, err)
	}
	f, err := os.CreateTemp(home, tempFilePattern)
	if err != nil {
		return "", errs.Wrap(errs.Internal, "rest: create token file", err)
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp) // best effort: the temp file holds an unused token
		}
	}()
	if err := f.Chmod(tokenMode); err != nil {
		_ = f.Close()
		return "", errs.Wrap(errs.Internal, "rest: chmod token file", err)
	}
	if _, err := f.WriteString(tok + "\n"); err != nil {
		_ = f.Close()
		return "", errs.Wrap(errs.Internal, "rest: write token file", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return "", errs.Wrap(errs.Internal, "rest: sync token file", err)
	}
	if err := f.Close(); err != nil {
		return "", errs.Wrap(errs.Internal, "rest: close token file", err)
	}
	if err := os.Rename(tmp, TokenPath(home)); err != nil {
		return "", errs.Wrap(errs.Internal, "rest: install token file", err)
	}
	ok = true
	return tok, nil
}

// readToken reads and checks an existing token; E_NOT_FOUND when absent.
func readToken(home string) (string, error) {
	p := TokenPath(home)
	st, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", errs.New(errs.NotFound, "rest: no api token yet")
	}
	if err != nil {
		return "", errs.Wrap(errs.Internal, "rest: stat "+p, err)
	}
	if !st.Mode().IsRegular() {
		return "", errs.New(errs.ConfigInvalid, p+" is not a regular file").WithHint("remove it; lore creates a new token")
	}
	if st.Mode().Perm()&groupOtherBits != 0 {
		return "", errs.New(errs.ConfigInvalid, p+" has mode "+strconv.FormatUint(uint64(st.Mode().Perm()), 8)+"; other users could read it").
			WithHint("run: chmod 600 " + p + " (or lore api token rotate)")
	}
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		return "", errs.Wrap(errs.Internal, "rest: read "+p, err)
	}
	tok := string(b)
	if n := len(tok); n > 0 && tok[n-1] == '\n' {
		tok = tok[:n-1]
	}
	if raw, err := hex.DecodeString(tok); err != nil || len(raw) != TokenBytes {
		return "", errs.New(errs.ConfigInvalid, p+" does not hold a lore api token").WithHint("run: lore api token rotate")
	}
	return tok, nil
}
