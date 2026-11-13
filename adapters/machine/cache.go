package machine

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/adapters/osrename"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// IDSource says where a machine id came from (profile field id_source).
type IDSource string

// The id sources (6.8).
const (
	SourceOS      IDSource = "os_machine_id"
	SourceRuntime IDSource = "runtime_key"
	SourceRandom  IDSource = "random"
)

// CacheFile is the fingerprint cache under the lore home (CFG11).
const CacheFile = "machine.json"

// randomIDBytes is the size of a random machine id (64 hex, like the HMAC).
const randomIDBytes = 32

// cache file permissions: private to the user.
const (
	cacheMode = 0o600
	homeMode  = 0o700
)

// cacheWire is ~/.lore/machine.json.
type cacheWire struct {
	ID       string   `json:"id"`
	IDSource IDSource `json:"id_source"`
}

// Load returns this machine's id and where it came from (see the package
// flow). home is the lore home; getenv reads the runtime variables.
func Load(home string, getenv func(string) string) (ids.MachineID, IDSource, error) {
	if key := RuntimeKey(getenv); key != "" {
		id, err := Derive(key)
		return id, SourceRuntime, err
	}
	path := filepath.Join(home, CacheFile)
	if b, err := os.ReadFile(filepath.Clean(path)); err == nil {
		var w cacheWire
		if json.Unmarshal(b, &w) == nil {
			if id, perr := ids.ParseMachine(w.ID); perr == nil && (w.IDSource == SourceOS || w.IDSource == SourceRandom) {
				return id, w.IDSource, nil
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ids.MachineID{}, "", errs.Wrap(errs.Internal, "machine: read "+CacheFile, err)
	}
	id, err := ID()
	src := SourceOS
	if err != nil {
		if !errs.Has(err, errs.NotFound) {
			return ids.MachineID{}, "", err
		}
		b := make([]byte, randomIDBytes)
		_, _ = rand.Read(b) // crypto/rand.Read never fails on supported platforms
		if id, err = ids.MachineFromDigest(b); err != nil {
			return ids.MachineID{}, "", err
		}
		src = SourceRandom
	}
	if werr := writeCache(home, cacheWire{ID: id.String(), IDSource: src}); werr != nil {
		return ids.MachineID{}, "", werr
	}
	return id, src, nil
}

// noWrites is SkipCacheWrites' process-wide switch.
var noWrites atomic.Bool

// SkipCacheWrites keeps machine.json as it is for the rest of the process
// (`lore fix --dry-run` writes nothing, B4-14): the id is worked out the
// same way and used, only not saved.
// It returns the func that lifts the switch again.
func SkipCacheWrites() func() {
	noWrites.Store(true)
	return func() { noWrites.Store(false) }
}

// writeCache writes machine.json atomically (temp file + rename).
func writeCache(home string, w cacheWire) error {
	if noWrites.Load() {
		return nil
	}
	if err := os.MkdirAll(home, homeMode); err != nil {
		return errs.FileIO("create the lore home", home, err)
	}
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return errs.Wrap(errs.Internal, "machine: encode "+CacheFile, err)
	}
	tmp, err := os.CreateTemp(home, CacheFile+".*")
	if err != nil {
		return errs.Wrap(errs.Internal, "machine: write "+CacheFile, err)
	}
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), cacheMode)
	}
	if werr == nil {
		werr = osrename.Rename(tmp.Name(), filepath.Join(home, CacheFile))
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return errs.Wrap(errs.Internal, "machine: write "+CacheFile, werr)
	}
	return nil
}
