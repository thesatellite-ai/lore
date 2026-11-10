// Package tmpdir makes private temporary folders and files that still
// work when the system temp folder cannot be used: TMPDIR names a folder
// that is gone, or one lore may not write in. Each caller names a
// fallback folder it already writes in (the repo's git folder, the cache
// folder, lore's home), so a temp file lands on the same disk as the data
// it is made for and is removed by the caller as before.
package tmpdir

import (
	"os"
)

// Dir is os.MkdirTemp in the system temp folder, else in the first
// fallback where it works. A fallback "" is skipped (it would mean the
// system temp folder again). The error is the system temp folder's when
// every place fails.
func Dir(prefix string, fallbacks ...string) (string, error) {
	dir, err := os.MkdirTemp("", prefix)
	if err == nil {
		return dir, nil
	}
	for _, f := range fallbacks {
		if f == "" {
			continue
		}
		if d, ferr := os.MkdirTemp(f, prefix); ferr == nil {
			return d, nil
		}
	}
	return "", err
}

// File is os.CreateTemp with the same fallbacks as Dir.
func File(pattern string, fallbacks ...string) (*os.File, error) {
	f, err := os.CreateTemp("", pattern)
	if err == nil {
		return f, nil
	}
	for _, d := range fallbacks {
		if d == "" {
			continue
		}
		if g, ferr := os.CreateTemp(d, pattern); ferr == nil {
			return g, nil
		}
	}
	return nil, err
}
