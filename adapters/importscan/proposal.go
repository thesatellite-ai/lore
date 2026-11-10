package importscan

// proposal.go — the facts turned into setups to import (pure, no I/O):
//
//	one member setup per orgs[] entry   org, the org's repos' folders, the host alias of its store url
//	one solo setup "personal"           the identity (config.yaml, else the newest legacy file),
//	                                    personal.store, the repos of no org, its store url's alias
//
// A proposal an existing setup already covers (same org; a solo setup
// with the same personal store) is left out and named in Covered. A name
// taken by another setup gets a -2, -3 … suffix. Folders are the repo's
// parent/** when every repo under that parent goes to the same setup, else
// the repo root/** (two setups never get the same glob: folders.overlap).

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// Proposal field names (the page labels each).
const (
	FieldName          = "name"
	FieldKind          = "kind"
	FieldOrg           = "org"
	FieldDefault       = "default"
	FieldIdentityKey   = "identity.key"
	FieldIdentityName  = "identity.name"
	FieldHandle        = "identity.handle"
	FieldPersonalStore = "personal_store"
	FieldHostAlias     = "github.host_alias"
	FieldFolder        = "folders"
	FieldOrgIdentity   = "org identity"
	FieldOrgStore      = "org store"
)

// soloName is the solo proposal's name.
const soloName = "personal"

// globAll is appended to a folder to cover everything below it.
const globAll = "/**"

// draft is a proposal being built with its repos.
type draft struct {
	p     Proposal
	repos []Repo
}

// propose builds the proposals from the facts.
func propose(cfg configFacts, ids []identityFact, repos []Repo, home string) ([]Proposal, []string) {
	taken := map[string]bool{}
	inTrash := map[string]bool{}
	hasDefault := false
	for _, s := range cfg.setups {
		taken[s.name] = true
		inTrash[s.name] = inTrash[s.name] || s.trashed
		hasDefault = hasDefault || (s.isDefault && !s.trashed)
	}
	var (
		drafts  []*draft
		covered []string
	)
	byOrg := map[string]*draft{}
	for _, o := range cfg.orgs {
		if existing, ok := setupWithOrg(cfg.setups, o.name.value); ok {
			covered = append(covered, existing.coveredLine("already uses org "+o.name.value))
			byOrg[o.name.value] = nil
			continue
		}
		if gone, ok := purgedWithOrg(cfg.purged, o.name.value); ok {
			covered = append(covered, purgedLine(gone, "used org "+o.name.value))
			byOrg[o.name.value] = nil
			continue
		}
		d := &draft{p: Proposal{Entry: onboard.SetupEntry{Kind: onboard.KindMember, Org: o.name.value}}}
		d.field(FieldOrg, o.name.value, cfg.path, o.name.line, "orgs[] entry; a member setup reads its identity and stores from it")
		if !o.identity.zero() {
			d.field(FieldOrgIdentity, firstNonEmpty(o.identity.name.value, o.identity.key.value), cfg.path, firstLine(o.identity), "kept in orgs["+o.name.value+"].identity, not copied")
		}
		for _, s := range o.stores {
			d.field(FieldOrgStore, s.value, cfg.path, s.line, "kept in orgs["+o.name.value+"].stores")
		}
		if len(o.stores) > 0 {
			if a := hostAlias(o.stores[0].value); a != "" {
				d.p.Entry.GitHub.HostAlias = a
				d.field(FieldHostAlias, a, cfg.path, o.stores[0].line, "the ssh Host of the org's store url")
			}
		}
		byOrg[o.name.value] = d
		drafts = append(drafts, d)
	}
	solo := soloDraft(cfg, ids)
	var soloRepos []Repo
	for _, r := range repos {
		org := r.Org
		if org == "" {
			org = orgOfStores(cfg.orgs, r.Stores)
		}
		if d, ok := byOrg[org]; ok && org != "" {
			if d != nil {
				d.repos = append(d.repos, r)
			}
			continue
		}
		soloRepos = append(soloRepos, r)
		if solo.p.Entry.PersonalStore == "" {
			for _, s := range r.Stores {
				if s.Name == "me" {
					solo.p.Entry.PersonalStore = s.URL
					solo.field(FieldPersonalStore, s.URL, r.StoresFile, s.Line, "the repo's personal store (me)")
				}
			}
		}
	}
	solo.repos = soloRepos
	if !solo.empty() {
		if existing, why, ok := soloCovering(cfg.setups, solo.p.Entry.PersonalStore, cfg.personalStore.value, solo.p.Entry.Identity.Key); ok {
			covered = append(covered, existing.coveredLine(why))
		} else if gone, why, ok := purgedSoloCovering(cfg.purged, solo.p.Entry.PersonalStore, cfg.personalStore.value, solo.p.Entry.Identity.Key); ok {
			covered = append(covered, purgedLine(gone, why))
		} else {
			if a := hostAlias(solo.p.Entry.PersonalStore); a != "" {
				solo.p.Entry.GitHub.HostAlias = a
				solo.field(FieldHostAlias, a, "", 0, "the ssh Host of the personal store url")
			}
			drafts = append([]*draft{solo}, drafts...)
		}
	}
	assignFolders(drafts, home)
	out := make([]Proposal, 0, len(drafts))
	for _, d := range drafts {
		base := soloName
		if d.p.Entry.Kind != onboard.KindSolo {
			base = setupNameFor(d.p.Entry.Org)
		}
		d.p.Entry.Name = uniqueName(base, taken)
		taken[d.p.Entry.Name] = true
		d.p.Fields = append([]Field{{Field: FieldName, Value: d.p.Entry.Name, Note: nameNote(base, d.p.Entry.Name, inTrash[base])},
			{Field: FieldKind, Value: string(d.p.Entry.Kind)}}, d.p.Fields...)
		if !hasDefault && d.p.Entry.Kind == onboard.KindSolo {
			d.p.Entry.Default = true
			d.p.Fields = append(d.p.Fields, Field{Field: FieldDefault, Value: "true", Note: "no setup is the default yet"})
		}
		for _, r := range d.repos {
			d.p.Repos = append(d.p.Repos, r.Root)
		}
		out = append(out, d.p)
	}
	return out, covered
}

// soloDraft starts the solo proposal: identity and personal store.
func soloDraft(cfg configFacts, ids []identityFact) *draft {
	d := &draft{p: Proposal{Entry: onboard.SetupEntry{Kind: onboard.KindSolo}}}
	id, note := cfg.identity, "config.yaml identity"
	if id.zero() && len(ids) > 0 {
		id, note = ids[0], "a lore before this version wrote it; config.yaml has no identity"
	}
	set := func(dst *string, field string, l located) {
		if l.value != "" {
			*dst = l.value
			d.field(field, l.value, id.file, l.line, note)
		}
	}
	set(&d.p.Entry.Identity.Key, FieldIdentityKey, id.key)
	set(&d.p.Entry.Identity.Name, FieldIdentityName, id.name)
	set(&d.p.Entry.Identity.Handle, FieldHandle, id.handle)
	if s := cfg.personalStore; s.value != "" {
		d.p.Entry.PersonalStore = s.value
		d.field(FieldPersonalStore, s.value, cfg.path, s.line, "personal.store")
	}
	return d
}

// empty reports a solo draft with nothing worth importing.
func (d *draft) empty() bool {
	return d.p.Entry.Identity.IsZero() && d.p.Entry.PersonalStore == "" && len(d.repos) == 0
}

// field records one value's provenance.
func (d *draft) field(name, val, file string, line int, note string) {
	d.p.Fields = append(d.p.Fields, Field{Field: name, Value: val, File: file, Line: line, Note: note})
}

// assignFolders sets each draft's folders from its repos.
func assignFolders(drafts []*draft, home string) {
	owners := map[string]map[*draft]bool{}
	for _, d := range drafts {
		for _, r := range d.repos {
			p := filepath.Dir(r.Root)
			if owners[p] == nil {
				owners[p] = map[*draft]bool{}
			}
			owners[p][d] = true
		}
	}
	for _, d := range drafts {
		seen := map[string]bool{}
		for _, r := range d.repos {
			dir := filepath.Dir(r.Root)
			if len(owners[dir]) > 1 || dir == home || dir == filepath.Dir(dir) {
				dir = r.Root
			}
			glob := tildePath(dir, home) + globAll
			if seen[glob] {
				continue
			}
			seen[glob] = true
			d.p.Entry.Folders = append(d.p.Entry.Folders, glob)
			note := "a known repo in machine.db"
			if r.From == FromScan {
				note = "found by the folder scan"
			}
			d.p.Fields = append(d.p.Fields, Field{Field: FieldFolder, Value: glob, File: r.Root, Note: note})
		}
	}
}

// setupWithOrg is the existing member/admin setup of org (ok false when
// none).
func setupWithOrg(setups []existingSetup, org string) (existingSetup, bool) {
	for _, s := range setups {
		if s.org == org && s.kind != string(onboard.KindSolo) {
			return s, true
		}
	}
	return existingSetup{}, false
}

// coveredLine is one Covered line: the covering setup, its state when it
// is not active, why it covers the proposal, and the command that brings
// it back — "work (in the trash) already has the same identity; restore it
// with: lore setup trash restore work". One level of parentheses only, and
// never "your setups": the covering setup may be the only one and not
// active (polish: every setup in the trash read "your setups already
// cover it: personal (setup work in the trash (lore setup trash restore
// work) has the same identity)").
func (s existingSetup) coveredLine(why string) string {
	switch {
	case s.trashed:
		return s.name + " (in the trash) " + why + "; restore it with: lore setup trash restore " + s.name
	case s.disabled:
		return s.name + " (disabled) " + why + "; enable it with: lore setup enable " + s.name
	}
	return s.name + " " + why
}

// soloCovering is an existing solo setup with the same identity key or
// the same personal store (any solo setup when none was found), and why it
// covers the solo proposal; ok false when none. A setup with no
// personal_store or "inherit" uses the shared personal.store, so it covers
// that store too (realmac NEW-3: Home offered to import "personal-2" for
// the personal.store an existing inherit setup already uses). The identity
// match keeps state lore wrote itself out of the proposals: the Stores
// step writes personal.store for a setup whose identity is config.yaml's,
// and that is not an older install (polish item 3).
func soloCovering(setups []existingSetup, store, shared, identityKey string) (who existingSetup, why string, ok bool) {
	for _, s := range setups {
		if s.kind != string(onboard.KindSolo) {
			continue
		}
		own := s.personalStore
		if own == "" || own == onboard.PersonalStoreInherit {
			own = shared
		}
		switch {
		case identityKey != "" && strings.EqualFold(s.identityKey, identityKey):
			return s, coverSameIdentity, true
		case store != "" && sameURL(own, store):
			return s, coverSameStore, true
		case store == "":
			return s, coverSolo, true
		}
	}
	return existingSetup{}, "", false
}

// Why a solo setup covers the solo proposal (the Covered line's words,
// after the setup's name).
const (
	coverSameIdentity = "already has the same identity"
	coverSameStore    = "already uses the same personal store"
	coverSolo         = "is already a solo setup"
	// The words for a setup deleted for good (past tense: it is gone).
	purgedSameIdentity = "had the same identity"
	purgedSameStore    = "used the same personal store"
	purgedSolo         = "was a solo setup"
)

// purgedLine is the Covered line of a proposal a deleted setup covers:
// "personal (deleted for good) had the same identity; …". The way back is
// removing its trash.purged entry, so the line names it.
func purgedLine(p purgedSetup, why string) string {
	return p.Name + " (deleted for good) " + why + "; it is not offered again (remove its trash.purged entry in config.yaml to import it)"
}

// purgedWithOrg is the first listed deleted member/admin setup of org (ok
// false when none).
func purgedWithOrg(purged []purgedSetup, org string) (purgedSetup, bool) {
	for _, p := range purged {
		if p.Org == org && p.Kind != onboard.KindSolo {
			return p, true
		}
	}
	return purgedSetup{}, false
}

// purgedSoloCovering is soloCovering for deleted solo setups, by
// fingerprint since the entry keeps neither value: the same identity key;
// else, unless both name an identity and they differ (another person's
// old file is a new import even beside the old store), the same personal
// store; else any deleted solo setup when the proposal has neither an
// identity nor a store. shared is config.yaml's personal.store, which a
// proposal without its own store would use.
func purgedSoloCovering(purged []purgedSetup, store, shared, identityKey string) (who purgedSetup, why string, ok bool) {
	idSHA := onboard.IdentityFingerprint(identityKey)
	stSHA := onboard.StoreFingerprint(firstNonEmpty(store, shared))
	for _, p := range purged {
		if p.Kind != onboard.KindSolo {
			continue
		}
		switch {
		case idSHA != "" && p.IdentitySHA == idSHA:
			return p, purgedSameIdentity, true
		case idSHA != "" && p.IdentitySHA != "":
			continue // two identities, two people
		case stSHA != "" && p.StoreSHA == stSHA:
			return p, purgedSameStore, true
		case idSHA == "" && stSHA == "":
			return p, purgedSolo, true
		}
	}
	return purgedSetup{}, "", false
}

// orgOfStores is the org one of the repo's stores belongs to ("" none).
func orgOfStores(orgs []orgFact, stores []RepoStore) string {
	for _, s := range stores {
		for _, o := range orgs {
			for _, u := range o.stores {
				if sameURL(u.value, s.URL) {
					return o.name.value
				}
			}
		}
	}
	return ""
}

// sameURL compares store urls ignoring a trailing .git and slash.
func sameURL(a, b string) bool {
	n := func(s string) string {
		return strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(s), "/"), ".git")
	}
	return a != "" && n(a) == n(b)
}

// hostAlias is the ssh Host of a store url when it is an alias, not a
// real host name (git@github-acme:o/r → github-acme); "" otherwise.
func hostAlias(url string) string {
	var host string
	switch {
	case strings.HasPrefix(url, "ssh://"):
		rest := strings.TrimPrefix(url, "ssh://")
		host, _, _ = strings.Cut(rest, "/")
		if _, h, ok := strings.Cut(host, "@"); ok {
			host = h
		}
		host, _, _ = strings.Cut(host, ":")
	case strings.Contains(url, "://"):
		return ""
	default:
		h, _, ok := strings.Cut(url, ":")
		if !ok {
			return ""
		}
		if _, after, at := strings.Cut(h, "@"); at {
			h = after
		}
		host = h
	}
	if host == "" || strings.Contains(host, ".") || host == "localhost" || strings.ContainsAny(host, "/\\") {
		return ""
	}
	return host
}

// setupNameFor makes a valid setup name of an org name.
func setupNameFor(org string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(org) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-_")
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		s = "org-" + s
	}
	if len(s) > onboard.SetupNameMaxLen {
		s = strings.TrimRight(s[:onboard.SetupNameMaxLen], "-_")
	}
	return s
}

// uniqueName is base, or base-2, base-3 … when taken.
func uniqueName(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		suffix := "-" + strconv.Itoa(i)
		n := base
		if len(n)+len(suffix) > onboard.SetupNameMaxLen {
			n = n[:onboard.SetupNameMaxLen-len(suffix)]
		}
		if !taken[n+suffix] {
			return n + suffix
		}
	}
}

// nameNote explains a renamed proposal: the name it would have had is
// taken by another setup (one in the trash says so, since the trash still
// holds the name), so it is proposed under a free one.
func nameNote(base, got string, trashed bool) string {
	switch {
	case base == got:
		return ""
	case trashed:
		return base + " is taken by a setup in the trash"
	}
	return base + " is taken by an existing setup"
}

// firstLine is the first set line of an identity block.
func firstLine(i identityFact) int {
	for _, l := range []located{i.key, i.name, i.handle} {
		if l.line > 0 {
			return l.line
		}
	}
	return 0
}
