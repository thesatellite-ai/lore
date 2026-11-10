package onboard

// importbanner.go — the Home "import" banner's wording and its dismiss key
// (owner report 2026-11-06): after removing his only setup, Home said
// "Found an existing lore install" although all it found was the identity
// and personal.store config.yaml still holds. The banner now says what it
// found and where, and says "existing lore install" only for real
// leftovers of a lore before setups (identity.yaml/.toml; never their
// *.migrated backups, which lore wrote after importing them).

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// BannerFacts is what an import scan found that the banner names.
type BannerFacts struct {
	// Legacy is set when files of a lore before setups exist (identity.yaml,
	// identity.toml; never their .migrated backups): a real older install.
	Legacy bool
	// LegacyFiles are those files' paths, written with ~.
	LegacyFiles []string
	// IdentityName is the identity's display name (else its key) found in
	// config.yaml or a legacy file; "" none.
	IdentityName string
	// PersonalStore is personal.store of config.yaml ("" none).
	PersonalStore string
	// ConfigPath is config.yaml's path, written with ~.
	ConfigPath string
	// Proposals are the setup names the scan proposes.
	Proposals []string
	// Renamed explains a proposal whose own name was taken, by its new
	// name: "personal-2" → "personal is taken by an existing setup".
	Renamed map[string]string
	// Covered are the proposals left out because a setup (enabled,
	// disabled or in the trash) already covers them, as the scan words
	// them; with an older install and nothing to import, the banner says
	// so once instead of hiding what it found (realmac #20).
	Covered []string
}

// coveredKeyMark sets the "already covered" line's key apart from a
// proposal banner's.
const coveredKeyMark = "covered"

// bannerKeyLen is how many hex characters of the hash a key keeps (enough
// to tell two installs apart; it is not a secret).
const bannerKeyLen = 16

// ImportBanner is the Home banner's one line, "" when nothing is proposed
// and nothing of an older install was found. With an older install that
// existing setups already cover it says so ("…: nothing to import — work
// (in the trash) already has the same identity; restore it with: …")
// rather than staying silent; a renamed proposal says why ("personal-2:
// personal is taken by an existing setup").
func ImportBanner(f BannerFacts) string {
	n := len(f.Proposals)
	where := strings.Join(f.LegacyFiles, ", ")
	if where == "" {
		where = "~/.lore"
	}
	if n == 0 {
		if f.Legacy && len(f.Covered) > 0 {
			return "Found an existing lore install (" + where + "): nothing to import — " + strings.Join(f.Covered, "; ") + "."
		}
		return ""
	}
	names := make([]string, 0, n)
	for _, p := range f.Proposals {
		if why := f.Renamed[p]; why != "" {
			p += ": " + why
		}
		names = append(names, p)
	}
	setups := strconv.Itoa(n) + " setup"
	if n != 1 {
		setups += "s"
	}
	setups += " (" + strings.Join(names, ", ") + ")"
	if f.Legacy {
		return "Found an existing lore install (" + where + ") — import " + setups + "?"
	}
	cfg := f.ConfigPath
	if cfg == "" {
		cfg = "~/.lore/config.yaml"
	}
	var parts []string
	if f.IdentityName != "" {
		parts = append(parts, "lore identity ("+f.IdentityName+")")
	}
	if f.PersonalStore != "" {
		parts = append(parts, "personal store ("+shortStore(f.PersonalStore)+")")
	}
	if len(parts) == 0 {
		return "Found lore repos and settings in " + cfg + " that no setup uses — import " + setups + "?"
	}
	verb := "is"
	if len(parts) > 1 {
		verb = "are"
	}
	what := "Your " + strings.Join(parts, " and ")
	count := plural(n, "a setup", strconv.Itoa(n)+" setups")
	if len(f.Renamed) > 0 {
		count = setups
	}
	return what + " " + verb + " still saved in " + cfg + " — make " + count + " from " + plural(len(parts), "it", "them") + "?"
}

// plural picks one or many.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// ImportBannerKey is the dismiss key of f: a hash of the values the banner
// names, so a dismissed banner stays hidden until one of them changes
// (another identity, another store, a new proposal).
func ImportBannerKey(f BannerFacts) string {
	h := sha256.New()
	vals := append([]string{strconv.FormatBool(f.Legacy), f.IdentityName, f.PersonalStore}, f.Proposals...)
	if len(f.Proposals) == 0 && len(f.Covered) > 0 {
		// The "already covered" line: keyed by the install it found, not by
		// how the covering setup is worded (disabling it must not bring the
		// dismissed line back).
		vals = append(append(vals, coveredKeyMark), f.LegacyFiles...)
	}
	for _, v := range vals {
		_, _ = h.Write([]byte(v)) // a hash never fails
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:bannerKeyLen]
}

// shortStore is owner/repo of a store url when it has one, else the url.
func shortStore(url string) string {
	if o, r, ok := OwnerRepo(url); ok {
		return o + "/" + r
	}
	return url
}
