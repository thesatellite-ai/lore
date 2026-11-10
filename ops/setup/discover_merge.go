package setup

// discover_merge.go — the pure half of setup.discover: facts + the alias
// table in, account cards and per-field candidates out. No I/O, so every
// rule here is table-tested (discover_test.go).
//
// Linking a git identity (global or includeIf) to a gh account, strongest
// first:
//  1. its email is one of the account's GitHub emails (API, public, noreply)
//  2. its email's local part is a dash-separated word of exactly one login
//     ("work@example.org" ↔ "octo-work"): shown as a guess
//  3. (global only) nothing else matched and the account is gh's active one

import (
	"path"
	"slices"
	"strconv"
	"strings"
)

// Scores (0–100). The page preselects the top one per field; the values
// order the sources by how sure each is that the value is "yours".
const (
	scoreTop      = 100 // gh's active login; the primary GitHub email
	scoreHigh     = 90  // a gh login; git's global identity; a lore store in use
	scoreStrong   = 85  // the public email; an includeIf identity
	scoreGood     = 80  // a verified GitHub email; an alias that logs in; a store of a repo lore has not initialised
	scoreFair     = 70  // a key that logs in alone; a guessed link; a repo named like a store
	scoreWeak     = 60  // a GitHub repo whose name only contains a store word
	scoreLow      = 40  // the noreply email; an alias not tested
	scoreBroken   = 10  // an alias whose key file is missing
	scoreExtraSrc = 2   // added per further source of the same value
	scoreActive   = 5   // added to an alias or key that logs in as gh's active login
	scoreMax      = 100
	setupPersonal = "personal"
)

// Fact field names (DiscoveredAccount.Facts[].Field).
const (
	factName   = "name"
	factEmail  = "email"
	factAlias  = "alias"
	factKey    = "key"
	factFolder = "folder"
	factLogin  = "login"
)

// candSet accumulates candidates of one field, deduplicated by value
// (emails and logins compared case-insensitively).
type candSet struct {
	fold bool
	// key, when set, is the identity two values share ("" = the value
	// itself): store urls compare by owner/repo, so the ssh-alias spelling
	// lore uses and GitHub's git@github.com spelling are one candidate
	// (the first spelling added is kept).
	key   func(string) string
	items []Candidate
}

// same reports whether a and b are one candidate value.
func (c *candSet) same(a, b string) bool {
	if a == b || (c.fold && strings.EqualFold(a, b)) {
		return true
	}
	if c.key != nil {
		ka, kb := c.key(a), c.key(b)
		return ka != "" && ka == kb
	}
	return false
}

func (c *candSet) add(value string, score int, src Source, accounts ...string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for i := range c.items {
		it := &c.items[i]
		if c.same(it.Value, value) {
			if !slices.Contains(it.Sources, src) {
				it.Sources = append(it.Sources, src)
				it.Score = min(scoreMax, max(it.Score, score)+scoreExtraSrc)
			}
			for _, a := range accounts {
				if a != "" && !slices.Contains(it.Accounts, a) {
					it.Accounts = append(it.Accounts, a)
				}
			}
			return
		}
	}
	cand := Candidate{Value: value, Sources: []Source{src}, Score: score}
	for _, a := range accounts {
		if a != "" {
			cand.Accounts = append(cand.Accounts, a)
		}
	}
	c.items = append(c.items, cand)
}

// sorted is the candidates best first (score, then value), never nil.
func (c *candSet) sorted() []Candidate {
	out := append([]Candidate{}, c.items...)
	slices.SortStableFunc(out, func(a, b Candidate) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return strings.Compare(a.Value, b.Value)
	})
	return out
}

// identityRef is a git identity being linked to accounts.
type identityRef struct {
	name, email string
	include     *GitIncludeFact // nil for the global identity
}

func (r identityRef) source() Source {
	if r.include == nil {
		return Source{Kind: SrcGitGlobal, Label: "git config --global"}
	}
	return Source{Kind: SrcGitInclude, Label: "gitconfig includeIf", Detail: r.include.Condition + " → " + r.include.Path}
}

// emailsOf is every GitHub email of acct, lower-cased.
func emailsOf(acct GHAccountFacts) []string {
	var out []string
	for _, e := range acct.Emails {
		out = append(out, strings.ToLower(e.Email))
	}
	if acct.Public != "" {
		out = append(out, strings.ToLower(acct.Public))
	}
	if n := noreply(acct); n != "" {
		out = append(out, strings.ToLower(n))
	}
	return out
}

// noreply is GitHub's noreply address for acct ("" without an id).
func noreply(acct GHAccountFacts) string {
	if acct.ID == 0 {
		return ""
	}
	return strconv.FormatInt(acct.ID, 10) + "+" + acct.Login + "@users.noreply.github.com"
}

// linkIdentity returns the login ref belongs to and whether the link is a
// guess ("" when none).
func linkIdentity(ref identityRef, gh []GHAccountFacts) (login string, guess bool) {
	email := strings.ToLower(ref.email)
	if email == "" {
		return "", false
	}
	for _, a := range gh {
		if slices.Contains(emailsOf(a), email) {
			return a.Login, false
		}
	}
	// The email's local part is one dash-separated word of exactly one login.
	local, _, _ := strings.Cut(email, "@")
	match := ""
	for _, a := range gh {
		words := strings.Split(strings.ToLower(a.Login), "-")
		if local != "" && (local == strings.ToLower(a.Login) || slices.Contains(words, local)) {
			if match != "" {
				match = ""
				break
			}
			match = a.Login
		}
	}
	if match != "" {
		return match, true
	}
	if ref.include == nil {
		for _, a := range gh {
			if a.Active {
				return a.Login, true
			}
		}
	}
	return "", false
}

// GitconfigEmailsOf is the includeIf identity emails linked to login by
// the gitconfig alone (linkIdentity without GitHub's email lists: an email
// whose local part is a dash-separated word of exactly that login among
// logins). They are the emails an account card carries from the gitconfig
// when the API could not list the account's own (github.identity_email
// names them instead of saying it knows nothing, polish m6). The global
// identity is left out: without the API lists it would link to gh's
// active login by default, which is a guess, not the card's evidence.
func GitconfigEmailsOf(login string, logins []string, incs []GitIncludeFact) []string {
	gh := make([]GHAccountFacts, 0, len(logins))
	for _, l := range logins {
		gh = append(gh, GHAccountFacts{Login: l})
	}
	var out []string
	for i := range incs {
		r := identityRef{email: incs[i].Email, include: &incs[i]}
		if l, _ := linkIdentity(r, gh); l != "" && strings.EqualFold(l, login) && !containsFold(out, incs[i].Email) {
			out = append(out, incs[i].Email)
		}
	}
	return out
}

// MergeDiscovery builds the cards and candidates (pure: exported for wire's token-leak test and other surfaces).
func MergeDiscovery(f DiscoverFacts, t AliasesOut) DiscoverOut {
	out := DiscoverOut{GitGlobal: f.GitGlobal, GitIncludes: f.GitIncludes, Notes: f.Notes}
	var name, email, user, handle, alias, key, store, folder candSet
	email.fold, user.fold, handle.fold = true, true, true
	store.key = StoreKey

	// gh logins: one card each.
	cards := make([]DiscoveredAccount, len(f.GH))
	byLogin := map[string]int{}
	for i, a := range f.GH {
		byLogin[strings.ToLower(a.Login)] = i
		c := DiscoveredAccount{Login: a.Login, Active: a.Active, Name: a.Name, Avatar: a.Avatar,
			NoreplyEmail: noreply(a), EmailScope: a.EmailScope, Status: a.Status, Detail: a.Detail}
		if c.EmailScope == "" {
			c.EmailScope = EmailScopeUnknown
		}
		loginSrc := Source{Kind: SrcGHLogin, Label: "gh login", Detail: a.Login}
		loginScore := scoreHigh
		if a.Active {
			loginSrc = Source{Kind: SrcGHActive, Label: "gh login, active", Detail: a.Login}
			loginScore = scoreTop
		}
		user.add(a.Login, loginScore, loginSrc, a.Login)
		handle.add(a.Login, loginScore, loginSrc, a.Login)
		c.Facts = append(c.Facts, Fact{Field: factLogin, Value: a.Login, Source: loginSrc})
		if a.Name != "" {
			src := Source{Kind: SrcGitHubProfile, Label: "GitHub profile", Detail: a.Login}
			name.add(a.Name, scoreHigh, src, a.Login)
			c.Facts = append(c.Facts, Fact{Field: factName, Value: a.Name, Source: src})
		}
		// Emails: primary first, then verified, the public one, noreply last.
		var ems []string
		for _, e := range a.Emails {
			src := Source{Kind: SrcGitHubEmail, Label: "GitHub email", Detail: a.Login}
			sc := scoreGood
			if e.Primary {
				src.Label, sc = "GitHub primary email", scoreTop
			} else if !e.Verified {
				src.Label, sc = "GitHub email (unverified)", scoreWeak
			}
			email.add(e.Email, sc, src, a.Login)
			ems = append(ems, e.Email)
			c.Facts = append(c.Facts, Fact{Field: factEmail, Value: e.Email, Source: src})
		}
		if a.Public != "" {
			src := Source{Kind: SrcGitHubPublic, Label: "GitHub public email", Detail: a.Login}
			email.add(a.Public, scoreStrong, src, a.Login)
			ems = append(ems, a.Public)
			c.Facts = append(c.Facts, Fact{Field: factEmail, Value: a.Public, Source: src})
		}
		if c.NoreplyEmail != "" {
			src := Source{Kind: SrcGitHubNoreply, Label: "GitHub noreply address", Detail: a.Login}
			email.add(c.NoreplyEmail, scoreLow, src, a.Login)
		}
		if a.EmailScope == EmailScopeMissing {
			c.RefreshCommand = refreshCommand(a.Login, f.GH)
		}
		c.Emails = ems
		cards[i] = c
	}

	// git identities, linked to cards.
	refs := []identityRef{{name: f.GitGlobal.Name, email: f.GitGlobal.Email}}
	for i := range f.GitIncludes {
		inc := &f.GitIncludes[i]
		refs = append(refs, identityRef{name: inc.Name, email: inc.Email, include: inc})
	}
	for _, r := range refs {
		if r.name == "" && r.email == "" {
			continue
		}
		login, guess := linkIdentity(r, f.GH)
		src := r.source()
		sc := scoreHigh
		if r.include != nil {
			sc = scoreStrong
		}
		email.add(r.email, sc, src, login)
		name.add(r.name, sc-scoreExtraSrc*5, src, login)
		if r.include != nil && r.include.Folder != "" {
			folder.add(r.include.Folder, scoreHigh, src, login)
		}
		i, ok := byLogin[strings.ToLower(login)]
		if !ok {
			continue
		}
		c := &cards[i]
		if guess {
			src.Label += " (matched by name)"
		}
		if r.email != "" {
			c.Facts = append(c.Facts, Fact{Field: factEmail, Value: r.email, Source: src})
			if !containsFold(c.Emails, r.email) {
				c.Emails = append(c.Emails, r.email)
			}
		}
		if c.Name == "" && r.name != "" {
			c.Name = r.name
			c.Facts = append(c.Facts, Fact{Field: factName, Value: r.name, Source: src})
		}
		if r.include != nil && r.include.Folder != "" {
			c.Folders = append(c.Folders, FolderHint{Folder: r.include.Folder, From: src.Detail})
			c.Facts = append(c.Facts, Fact{Field: factFolder, Value: r.include.Folder, Source: src})
		}
	}

	// ssh aliases and keys; those of gh's active login rank first.
	activeBonus := func(login string) int {
		for _, a := range f.GH {
			if a.Active && strings.EqualFold(a.Login, login) {
				return scoreActive
			}
		}
		return 0
	}
	for _, a := range t.Aliases {
		k := a.PubKey
		if k == "" {
			k = a.IdentityFile
		}
		src := Source{Kind: SrcSSHAlias, Label: "ssh alias", Detail: a.Alias}
		switch {
		case a.KeyMissing:
			src.Label = "ssh alias, key file missing"
			alias.add(a.Alias, scoreBroken, src, a.LogsInAs)
		case a.LogsInAs != "":
			src.Label = "ssh alias, logs in as " + a.LogsInAs
			sc := scoreGood + activeBonus(a.LogsInAs)
			alias.add(a.Alias, sc, src, a.LogsInAs)
			user.add(a.LogsInAs, scoreFair, src, a.LogsInAs)
			if !a.KeyMissing && k != "" {
				key.add(k, sc, Source{Kind: SrcSSHAlias, Label: "key of alias " + a.Alias + ", logs in as " + a.LogsInAs, Detail: a.Alias}, a.LogsInAs)
			}
		default:
			alias.add(a.Alias, scoreLow, src)
		}
		if i, ok := byLogin[strings.ToLower(a.LogsInAs)]; ok || a.KeyMissing {
			r := AccountRoute{Alias: a.Alias, Key: k, KeyMissing: a.KeyMissing, OK: a.LogsInAs != "" && !a.KeyMissing}
			if ok {
				cards[i].Routes = append(cards[i].Routes, r)
			}
		}
	}
	for _, kr := range t.Keys {
		k := kr.PubKey
		if k == "" {
			k = kr.Path
		}
		if kr.LogsInAs == "" {
			key.add(k, scoreLow, Source{Kind: SrcSSHKey, Label: "key file (logs in as nobody)", Detail: kr.Path})
			continue
		}
		key.add(k, scoreFair+activeBonus(kr.LogsInAs), Source{Kind: SrcSSHKey, Label: "key alone logs in as " + kr.LogsInAs, Detail: kr.Path}, kr.LogsInAs)
		if i, ok := byLogin[strings.ToLower(kr.LogsInAs)]; ok {
			cards[i].Routes = append(cards[i].Routes, AccountRoute{Key: k, OK: true})
		}
	}

	// Store urls: the ones lore already uses first (their spelling, often
	// an ssh alias, is kept when a GitHub repo is the same owner/repo),
	// then each account's store-like repos (storeRepoScore).
	for _, s := range f.KnownStores {
		label, sc := "lore setup "+s.Detail, scoreHigh
		switch s.Kind {
		case SrcLoreOrg:
			label = "lore org " + s.Detail
		case SrcLoreRepo:
			label, sc = "stores.yaml of "+s.Detail, scoreStrong
		case SrcRepoScan:
			label, sc = "stores.yaml of "+s.Detail+", lore not run there yet", scoreGood
		}
		store.add(s.URL, sc, Source{Kind: s.Kind, Label: label, Detail: s.Detail})
	}
	for _, a := range f.GH {
		for _, r := range a.Repos {
			sc, ok := StoreLikeRepo(r.FullName)
			if !ok {
				continue
			}
			label := "GitHub repo of " + a.Login
			if r.Private {
				label += " (private)"
			}
			store.add(r.SSHURL, sc, Source{Kind: SrcGitHubRepo, Label: label, Detail: r.FullName}, a.Login)
		}
	}

	// Card summaries: best email, route, setup name.
	taken := append([]string{}, f.SetupNames...)
	for i := range cards {
		finishCard(&cards[i], &taken)
		cards[i].UsedBy = f.SetupUsers[strings.ToLower(cards[i].Login)]
	}
	out.Accounts = cards
	out.Fields = DiscoverFields{Name: name.sorted(), Email: email.sorted(), Username: user.sorted(), Handle: handle.sorted(),
		Alias: alias.sorted(), Key: key.sorted(), StoreURL: store.sorted(), Folder: folder.sorted()}
	return out
}

// finishCard picks the card's best email, alias, key and setup name.
func finishCard(c *DiscoveredAccount, taken *[]string) {
	if len(c.Emails) > 0 {
		c.Email, c.Emails = c.Emails[0], c.Emails[1:]
	} else if c.NoreplyEmail != "" {
		c.Email = c.NoreplyEmail
	}
	// Prefer an alias route that logs in, then any key route.
	for _, r := range c.Routes {
		if r.OK && r.Alias != "" {
			c.Alias, c.Key = r.Alias, r.Key
			break
		}
	}
	if c.Key == "" {
		for _, r := range c.Routes {
			if r.OK && r.Key != "" {
				c.Key = r.Key
				break
			}
		}
	}
	if c.Alias != "" {
		c.Facts = append(c.Facts, Fact{Field: factAlias, Value: c.Alias, Source: Source{Kind: SrcSSHAlias, Label: "ssh -T through this alias logs in as " + c.Login, Detail: c.Alias}})
	}
	if c.Key != "" {
		c.Facts = append(c.Facts, Fact{Field: factKey, Value: c.Key, Source: Source{Kind: SrcSSHKey, Label: "this key logs in as " + c.Login, Detail: c.Key}})
	}
	base := c.Login
	switch {
	case len(c.Folders) > 0:
		if b := folderBase(c.Folders[0].Folder); b != "" {
			base = b
		}
	case c.Active:
		base = setupPersonal
	}
	c.SetupName = uniqueName(base, taken)
}

// folderBase is the last element of a gitdir folder ("~/work/acme/" → "acme").
func folderBase(f string) string {
	f = strings.TrimRight(strings.TrimSuffix(f, "**"), "/")
	b := strings.ToLower(path.Base(f))
	if b == "." || b == "/" || b == "~" || b == "" {
		return ""
	}
	return b
}

// uniqueName is base, or base-2, base-3 … when taken; records the choice.
func uniqueName(base string, taken *[]string) string {
	n := base
	for i := 2; slices.Contains(*taken, n); i++ {
		n = base + "-" + strconv.Itoa(i)
	}
	*taken = append(*taken, n)
	return n
}

func containsFold(list []string, v string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.EqualFold(s, v) })
}

// refreshCommand is the shell line that adds user:email to login's gh
// token. gh refreshes only the active account, so an inactive one is
// switched to first and back after.
func refreshCommand(login string, gh []GHAccountFacts) string {
	const refresh = "gh auth refresh -h github.com -s user:email"
	active := ""
	for _, a := range gh {
		if a.Active {
			active = a.Login
		}
	}
	if active == "" || strings.EqualFold(active, login) {
		return refresh
	}
	return "gh auth switch -h github.com -u " + login + " && " + refresh + " && gh auth switch -h github.com -u " + active
}

// storeWords are the repo-name words that suggest a lore store (owner
// 2026-11-06: "repos named like lore/notes/store first").
var storeWords = []string{"lore", "notes", "store"}

// storeHintWords only hint at one (ranked below storeWords).
var storeHintWords = []string{"memory", "brain", "knowledge"}

// StoreLikeRepo scores a GitHub repo ("owner/name" or "name") as a store
// url suggestion, ok false when its name says nothing about a store:
//
//	lore, notes, store, *-lore, lore-*, *-notes …   scoreFair (a word of the name is a store word)
//	lorekit, team-memory, my-brain …                scoreWeak (contains a store or hint word)
//
// wire uses it to pick which of an account's repos to keep before the
// per-account cap, MergeDiscovery to rank them, so both agree.
func StoreLikeRepo(fullName string) (int, bool) {
	name := strings.ToLower(path.Base(strings.TrimSuffix(fullName, ".git")))
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	for _, w := range storeWords {
		if slices.Contains(words, w) {
			return scoreFair, true
		}
	}
	for _, w := range append(append([]string{}, storeWords...), storeHintWords...) {
		if strings.Contains(name, w) {
			return scoreWeak, true
		}
	}
	return 0, false
}

// StoreKey is the identity of a git store url for de-duplication: the
// lower-cased path without .git ("acme/hub"), whatever the host or ssh
// alias in front ("git@github-work:acme/hub.git", "ssh://git@github.com/acme/hub",
// "https://github.com/Acme/hub.git" are one key). The host is left out on
// purpose: an ssh alias stands for github.com and lore cannot tell which
// host an alias is without ssh -G, so two hosts with the same owner/repo
// path merge (the first spelling wins, every source is kept). "" when url
// has no owner/repo path (a local folder keeps its own value).
func StoreKey(url string) string {
	u := strings.TrimSpace(url)
	switch {
	case strings.Contains(u, "://"):
		_, rest, _ := strings.Cut(u, "://")
		_, p, ok := strings.Cut(rest, "/")
		if !ok || strings.HasPrefix(u, "file://") {
			return ""
		}
		u = p
	case strings.Contains(u, ":") && !strings.HasPrefix(u, "/"):
		_, u, _ = strings.Cut(u, ":")
	default:
		return ""
	}
	u = strings.ToLower(strings.Trim(strings.TrimSuffix(strings.TrimRight(u, "/"), ".git"), "/"))
	if strings.Count(u, "/") < 1 {
		return ""
	}
	return u
}
