package wire

// setupdiscover_cache.go — setup.discover's kept answer
// (opssetup.DiscoverCacheStore): one 0600 JSON file under the machine
// state folder, so reopening the setup page shows the accounts at once
// instead of after 2–8 s of gh, GitHub API and ssh -T probes. It holds
// what the page already shows (logins, names, emails, avatars, aliases,
// key paths); never a token (the facts port has no token field).
//
// Validity is decided in ops/setup (TTL and fingerprint); this file only
// names the fingerprint: the size and modification time of every file an
// answer is read from, so editing ~/.ssh/config, gh's hosts.yml, a git
// config or lore's config.yaml makes the next load probe again.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

var _ opssetup.DiscoverCacheStore = (*App)(nil)

const (
	// discoverCacheFile is the kept answer under <lore home>/state.
	discoverCacheFile = "discover-cache.json"
	// discoverCacheMode: owner only, like every state file.
	discoverCacheMode = 0o600
	// discoverCacheFormat changes when the cached shape does, so an
	// older file is never served as a newer answer.
	discoverCacheFormat = "discover-v1"
)

// discoverCachePath is the file of this lore home.
func (a *App) discoverCachePath() string {
	return filepath.Join(a.opts.Home, localStateDir, discoverCacheFile)
}

// discoverSourceFiles are the files a discovery answer is read from.
func (a *App) discoverSourceFiles(ctx context.Context) []string {
	h := userHome(SetupQuery{})
	files := []string{
		filepath.Join(a.opts.Home, "config.yaml"),
		filepath.Join(h, ".ssh", "config"),
		filepath.Join(h, ".gitconfig"),
		filepath.Join(h, ".config", "git", "config"),
	}
	if p, err := a.onboardProbe(ctx); err == nil {
		files = append(files, p.GHHostsPath())
	}
	return files
}

// DiscoverFingerprint implements opssetup.DiscoverCacheStore.
func (a *App) DiscoverFingerprint(ctx context.Context) string {
	var b strings.Builder
	b.WriteString(discoverCacheFormat)
	for _, f := range a.discoverSourceFiles(ctx) {
		b.WriteString("|")
		b.WriteString(f)
		if st, err := os.Stat(f); err == nil {
			b.WriteString(":" + strconv.FormatInt(st.Size(), 10) + ":" + strconv.FormatInt(st.ModTime().UnixNano(), 10))
		}
	}
	return b.String()
}

// LoadDiscover implements opssetup.DiscoverCacheStore; a missing or
// broken file is no answer (the next job rewrites it).
func (a *App) LoadDiscover(context.Context) (opssetup.CachedDiscover, bool) {
	b, err := os.ReadFile(a.discoverCachePath())
	if err != nil {
		return opssetup.CachedDiscover{}, false
	}
	var c opssetup.CachedDiscover
	if json.Unmarshal(b, &c) != nil || c.At.IsZero() {
		return opssetup.CachedDiscover{}, false
	}
	return c, true
}

// SaveDiscover implements opssetup.DiscoverCacheStore: write a temp file
// and rename it, so a reader never sees half an answer.
func (a *App) SaveDiscover(_ context.Context, c opssetup.CachedDiscover) error {
	c.Out.Status = opssetup.DiscoverStatus{}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	path := a.discoverCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), discoverCacheFile+".*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), discoverCacheMode)
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
	}
	return werr
}
