package wire

// locktimeout.go — `store.lock_timeout` (CLI_REFERENCE 6.1, SPEC 6.2;
// F63 left its home open). DECISION FA-AGENT-8: it is a LOCAL setting in
// ~/.lore/config.yaml (`store: {lock_timeout: 30s}`): how long this
// machine waits for a store lock is about this machine, not a team rule.
//
//	open ─▶ opts.LockTimeout == 0 ─▶ readLockTimeout(home)
//	     ─▶ store.OpenOptions.LockTimeout ─▶ every backend's rowfs lock

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/thesatellite-ai/lore/logic/yamlerr"
	"github.com/thesatellite-ai/lore/model/errs"
)

// lockTimeoutDoc is the part of config.yaml this file reads.
type lockTimeoutDoc struct {
	Store struct {
		LockTimeout string `yaml:"lock_timeout"`
	} `yaml:"store"`
}

// maxLockTimeout caps the setting: a typo like 10h must not hang every
// command for hours behind a stuck lock.
const maxLockTimeout = 10 * time.Minute

// readLockTimeout is store.lock_timeout from <home>/config.yaml; 0 when
// unset (the backends' 10 s default). A value that is not a Go duration
// in (0, 10m] fails closed with E_CONFIG_INVALID.
func readLockTimeout(home string) (time.Duration, error) {
	b, err := os.ReadFile(filepath.Clean(configPath(home)))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, errs.Wrap(errs.Internal, "wire: read "+ConfigFile, err)
	}
	var doc lockTimeoutDoc
	if err = yamlerr.Decode(b, &doc, yamlerr.Lenient); err != nil {
		return 0, configYAMLErr(err)
	}
	if doc.Store.LockTimeout == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(doc.Store.LockTimeout)
	if err != nil || d <= 0 || d > maxLockTimeout {
		return 0, errs.New(errs.ConfigInvalid, "~/.lore/"+ConfigFile+": store.lock_timeout "+doc.Store.LockTimeout+" is not a duration between 0 and 10m (e.g. 30s)")
	}
	return d, nil
}
