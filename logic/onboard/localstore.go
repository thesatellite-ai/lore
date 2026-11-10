package onboard

// localstore.go — the personal store step's "A local folder" choice is the
// person's one personal store `me` (~/.lore/stores/me), shared by all
// their setups: the personal: block of config.yaml is global, not per
// setup (decided 2026-11-09, journey joiner). Keeping one job's notes from
// another is what that job's org stores are for. The page names the setups
// already on it, so picking it never looks like a new, separate store.

import (
	neturl "net/url"
	"path"
	"strings"
)

// IsDefaultLocalStore reports whether url is the default local personal
// store folder (a file url ending in /stores/me; "local" is the page's
// choice for it).
func IsDefaultLocalStore(url string) bool {
	url = strings.TrimSpace(url)
	if url == PersonalStoreLocal {
		return true
	}
	u, err := neturl.Parse(url)
	if err != nil || u.Scheme != "file" {
		return false
	}
	p := path.Clean(u.Path)
	return path.Base(p) == "me" && path.Base(path.Dir(p)) == "stores"
}

// SetupsOnLocalStore names the live setups other than except whose
// personal store is the default local folder: their own personal_store,
// or the shared personal.store they inherit.
func SetupsOnLocalStore(cfg Config, except string) []string {
	var out []string
	for _, e := range cfg.Setups {
		if e.Name == except {
			continue
		}
		switch ps := strings.TrimSpace(e.PersonalStore); ps {
		case PersonalStoreNone:
		case "", PersonalStoreInherit:
			if IsDefaultLocalStore(cfg.SharedStore) {
				out = append(out, e.Name)
			}
		default:
			if IsDefaultLocalStore(ps) {
				out = append(out, e.Name)
			}
		}
	}
	return out
}

// LocalStoreDetail is the page's detail line of the local choice: the
// folder, and, when other setups already keep their notes there, that it
// is shared with them.
func LocalStoreDetail(cfg Config, setup string) string {
	others := SetupsOnLocalStore(cfg, setup)
	if len(others) == 0 {
		return LocalStoreURL
	}
	return LocalStoreURL + " (your one personal store, shared with " + plural(len(others), "your setup ", "your setups ") + strings.Join(others, ", ") + ")"
}
