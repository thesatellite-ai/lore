package onboard

// here.go — the current folder's context for guided setup (fix-all item 3):
// `lore setup`, `lore setup list` and the page's Home banner all say, for
// the folder lore was started in, which org, store and identity it uses and
// which setup covers it, or offer a setup prefilled from the repo.
//
//	HereFacts (wire gathers: git, .lore/, org resolution, stores.yaml,
//	           identity chain, 3.3 active setup) ─▶ ResolveHere(setups) ─▶ Here
//	Here ─▶ HereLine (the one CLI line) / JSON (the page banner)
//
// Pure: no I/O. Every fact carries where it came from, so a front end can
// explain it (the page's popover) without asking again.

import (
	"path/filepath"
	"strconv"
	"strings"
)

// HereState is the closed set of answers about the current folder.
type HereState string

// The four states, in the order ResolveHere tests them.
const (
	// HereNotGit: the folder is in no git repo (the page shows nothing).
	HereNotGit HereState = "not_git"
	// HereNotLore: a git repo `lore init` has not run in (no
	// .lore/data/_meta.json); the page shows nothing, the this_folder step
	// offers `lore init`.
	HereNotLore HereState = "not_lore"
	// HereMatch: a setup covers the repo (Here.Setup, Here.Why).
	HereMatch HereState = "match"
	// HereNoMatch: the repo uses lore but no setup covers it; Proposal is
	// the setup to create from what the repo uses.
	HereNoMatch HereState = "no_match"
)

// Why values ResolveHere reads (the ops/setup Why* words, ONB 3.3; the
// logic layer cannot import ops, so the two it decides on are named here).
const (
	hereWhyDefault = "default"
	hereWhyNone    = "none"
)

// identityKeyHuman prefixes a person's stable key ("human:<email>").
const identityKeyHuman = "human:"

// defaultSoloName is the name offered for a solo setup made from a repo
// with no org, when no setup has it yet (the page's first-setup name).
const defaultSoloName = "personal"

// HereFacts is what wire found about the folder lore runs in. Every
// *Source field is a short phrase saying where its fact came from.
type HereFacts struct {
	// Dir is the folder lore was started in (always set; Root may be "").
	Dir string
	// Git: the folder is inside a git work tree; Root is its toplevel.
	Git  bool
	Root string
	// Lore: Root holds .lore/data/_meta.json (`lore init` ran).
	Lore bool
	// UserHome turns Root into a ~ glob for the proposal ("" keeps it absolute).
	UserHome string
	// Org is the org the repo resolves to with no setup hint ("" none);
	// OrgSource is the rule that decided ("own: pattern github.com/acme/*").
	Org, OrgSource string
	// Stores are the names in .lore/stores.yaml, in precedence order.
	Stores []string
	// StoresSource names the file the store names came from.
	StoresSource string
	// IdentityName and IdentityKey are who a write here is stamped as;
	// IdentitySource is the identity chain step that answered.
	IdentityName, IdentityKey, IdentitySource string
	// GitEmail is `git config user.email` in Root (includeIf applied).
	GitEmail string
	// Remote is the origin url, credentials stripped ("" none).
	Remote string
	// Pointers are the tool pointer files in Root that point at
	// .lore/LORE.md (slash paths, e.g. "AGENTS.md"); empty when none.
	Pointers []string
	// ActiveName, ActiveWhy and ActiveDetail are the 3.3 resolution for
	// the folder (ActiveWhy one of the ops/setup Why* words);
	// ActiveError is its refusal text (E_SETUP_AMBIGUOUS, …), "" when none.
	ActiveName, ActiveWhy, ActiveDetail, ActiveError string
}

// HereProposal is the setup ResolveHere offers to create from a repo no
// setup covers: what the person would otherwise type, all still editable.
type HereProposal struct {
	Kind          Kind     `json:"kind"`
	Name          string   `json:"name"`
	Org           string   `json:"org,omitempty"`
	IdentityName  string   `json:"identity_name,omitempty"`
	IdentityEmail string   `json:"identity_email,omitempty"`
	Folders       []string `json:"folders"`
}

// Entry is the setups[] entry the proposal creates (setup.import's input).
// Only a solo setup carries an identity: member and admin read theirs from
// orgs[<org>].identity (3.2), so it is left off them.
func (p HereProposal) Entry() SetupEntry {
	e := SetupEntry{Name: p.Name, Kind: p.Kind, Org: p.Org, Folders: append([]string{}, p.Folders...)}
	if p.Kind == KindSolo {
		e.Org = ""
		if p.IdentityEmail != "" {
			e.Identity = SetupIdentity{Key: identityKeyHuman + p.IdentityEmail, Name: p.IdentityName}
		}
	}
	return e
}

// Here is the answer about the current folder (the JSON the page reads).
type Here struct {
	State HereState `json:"state"`
	// Dir is the folder lore was started in, whatever the state, so the
	// this_folder step can name it even outside a git repo.
	Dir  string `json:"dir,omitempty"`
	Root string `json:"root,omitempty"`
	// Glob is the folder rule that covers Root (root/** with home as ~),
	// what "Use this setup for this folder" adds; "" outside a git repo.
	Glob string `json:"glob,omitempty"`
	// Pointers are the tool pointer files in Root that point at
	// .lore/LORE.md (HereFacts.Pointers); [] when none or not a lore repo.
	Pointers []string `json:"pointers"`
	// The repo's facts and their sources (empty unless the repo uses lore).
	Org            string   `json:"org,omitempty"`
	OrgSource      string   `json:"org_source,omitempty"`
	Stores         []string `json:"stores"`
	StoresSource   string   `json:"stores_source,omitempty"`
	IdentityName   string   `json:"identity_name,omitempty"`
	IdentityKey    string   `json:"identity_key,omitempty"`
	IdentitySource string   `json:"identity_source,omitempty"`
	GitEmail       string   `json:"git_email,omitempty"`
	Remote         string   `json:"remote,omitempty"`
	// Setup, Why and Detail: the matching setup and the 3.3 step that
	// chose it (State match only).
	Setup  string `json:"setup,omitempty"`
	Why    string `json:"why,omitempty"`
	Detail string `json:"detail,omitempty"`
	// ClientOrg is the org a not_lore repo is a client repo of (a hosted
	// remote the org claims), "" otherwise. Why: there the step is the org
	// link, never lore init, which writes lore's files into the client's
	// repository (I-13).
	ClientOrg string `json:"client_org,omitempty"`
	// Note explains a no_match that is not simply "nothing covers it"
	// (a refused resolution, a default setup of another org).
	Note string `json:"note,omitempty"`
	// Proposal is set with State no_match (zero, and left out of the
	// JSON, otherwise; op output fields are values, never pointers).
	Proposal HereProposal `json:"proposal,omitempty,omitzero"`
}

// ResolveHere decides what the current folder is relative to setups.
//
// A setup matches when the 3.3 resolution chose it for a reason about this
// folder (flag, env, repo pin, folder glob, org). A default setup is only a
// fallback: it matches when its org agrees with the repo's (both none, or
// the same org); a default setup of another org leaves the repo uncovered.
// A refused resolution (two setups tie) is no_match with the refusal as
// Note, since writes there are refused until it is fixed.
func ResolveHere(f HereFacts, setups []SetupEntry) Here {
	h := Here{Dir: f.Dir, Root: f.Root, Stores: []string{}, Pointers: []string{}}
	switch {
	case !f.Git:
		h.State, h.Root = HereNotGit, ""
		return h
	case !f.Lore:
		h.State, h.Glob = HereNotLore, folderGlob(f.Root, f.UserHome)
		return h
	}
	h.Glob = folderGlob(f.Root, f.UserHome)
	h.Pointers = append(h.Pointers, f.Pointers...)
	h.Org, h.OrgSource = f.Org, f.OrgSource
	h.Stores = append(h.Stores, f.Stores...)
	h.StoresSource = f.StoresSource
	h.IdentityName, h.IdentityKey, h.IdentitySource = f.IdentityName, f.IdentityKey, f.IdentitySource
	h.GitEmail, h.Remote = f.GitEmail, f.Remote

	if f.ActiveError == "" && f.ActiveName != "" && f.ActiveWhy != hereWhyNone {
		s, ok := setupNamed(setups, f.ActiveName)
		if ok && (f.ActiveWhy != hereWhyDefault || s.Org == f.Org) {
			h.State, h.Setup, h.Why, h.Detail = HereMatch, s.Name, f.ActiveWhy, f.ActiveDetail
			return h
		}
		if ok {
			h.Note = "the default setup " + s.Name + " is for " + orgPhrase(s.Org) + ", this repo is " + orgPhrase(f.Org)
		}
	}
	if f.ActiveError != "" {
		h.Note = f.ActiveError
	}
	h.State = HereNoMatch
	h.Proposal = propose(f, setups)
	return h
}

// propose is the setup to create from the repo's facts.
func propose(f HereFacts, setups []SetupEntry) HereProposal {
	p := HereProposal{Kind: KindSolo, Org: f.Org, Folders: []string{folderGlob(f.Root, f.UserHome)}}
	base := defaultSoloName
	if f.Org != "" {
		// An org on this machine with no setup: the person is in it
		// (member); admin is a choice they make on the kind step.
		p.Kind, base = KindMember, f.Org
	} else if r := filepath.Base(f.Root); f.Root != "" && setupTaken(setups, base) {
		base = r
	}
	p.Name = freeName(setups, base)
	p.IdentityName = f.IdentityName
	p.IdentityEmail = emailOf(f.IdentityKey)
	if p.IdentityEmail == "" {
		p.IdentityEmail = f.GitEmail
	}
	return p
}

// folderGlob is root/** with the user's home written as ~.
func folderGlob(root, home string) string {
	if root == "" {
		return ""
	}
	if home != "" {
		if rel, err := filepath.Rel(home, root); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if rel == "." {
				return "~/**"
			}
			return "~/" + filepath.ToSlash(rel) + "/**"
		}
	}
	return filepath.ToSlash(root) + "/**"
}

// emailOf is the email of a "human:<email>" key, "" for any other key.
func emailOf(key string) string {
	e, ok := strings.CutPrefix(key, identityKeyHuman)
	if !ok || !strings.Contains(e, "@") {
		return ""
	}
	return e
}

// freeName is base, or base-2, base-3, … when a setup already has it.
func freeName(setups []SetupEntry, base string) string {
	name := base
	for i := 2; setupTaken(setups, name); i++ {
		name = base + "-" + strconv.Itoa(i)
	}
	return name
}

func setupTaken(setups []SetupEntry, name string) bool {
	_, ok := setupNamed(setups, name)
	return ok
}

func setupNamed(setups []SetupEntry, name string) (SetupEntry, bool) {
	for _, s := range setups {
		if s.Name == name {
			return s, true
		}
	}
	return SetupEntry{}, false
}

// orgPhrase is "org acme" or "no org".
func orgPhrase(org string) string {
	if org == "" {
		return "no org"
	}
	return "org " + org
}

// HereLinePrefix starts the one CLI line about the current folder.
const HereLinePrefix = "This folder: "

// HereLine is the one line `lore setup` and `lore setup list` print about
// the current folder, e.g. "This folder: org acme, store team_notes,
// written as Pat Example — matches setup work (folder ~/code/acme/**)".
func HereLine(h Here) string {
	switch h.State {
	case HereNotGit:
		return HereLinePrefix + "not a git repo"
	case HereNotLore:
		if h.ClientOrg != "" {
			return HereLinePrefix + "a client repo of " + h.ClientOrg + " (lore keeps its notes in " + h.ClientOrg + "'s store: lore org link --org " + h.ClientOrg + " --project <folder>; not lore init, which writes into the client's repository)"
		}
		return HereLinePrefix + "a git repo lore is not set up in (lore init sets it up)"
	}
	facts := orgPhrase(h.Org) + ", " + storePhrase(h.Stores) + ", written as " + writerOf(h)
	if h.State == HereMatch {
		why := h.Why
		if h.Detail != "" {
			why += " " + h.Detail
		}
		return HereLinePrefix + facts + " — matches setup " + h.Setup + " (" + why + ")"
	}
	line := HereLinePrefix + facts + " — no setup matches"
	if h.Note != "" {
		line += " (" + h.Note + ")"
	}
	if h.Proposal.Name != "" {
		line += "; lore setup offers a " + string(h.Proposal.Kind) + " setup " + h.Proposal.Name + " from this repo"
	}
	return line
}

// storePhrase is "store a", "stores a, b" or "no attached store".
func storePhrase(stores []string) string {
	switch len(stores) {
	case 0:
		return "no attached store"
	case 1:
		return "store " + stores[0]
	}
	return "stores " + strings.Join(stores, ", ")
}

// writerOf is the identity's name, else its key, else "unknown".
func writerOf(h Here) string {
	switch {
	case h.IdentityName != "" && h.IdentityKey != "":
		if e := emailOf(h.IdentityKey); e != "" {
			return h.IdentityName + " <" + e + ">"
		}
		return h.IdentityName
	case h.IdentityName != "":
		return h.IdentityName
	case h.IdentityKey != "":
		return h.IdentityKey
	}
	return "an unknown identity"
}

// HereStates lists the four states in the order ResolveHere tests them.
func HereStates() []HereState { return []HereState{HereNotGit, HereNotLore, HereMatch, HereNoMatch} }

// EnumValues lists every state (op.Enumerated).
func (HereState) EnumValues() []string { return enumStrings(HereStates()) }

// ForJoin is h for the folder `lore setup join` started in: a no_match
// folder is offered the member setup setup of org (org "" until the
// org's store is read) instead of the solo or other setup ResolveHere
// guessed, since the person asked to join that org. The folders and
// identity facts stay; any other state is returned as is.
func (h Here) ForJoin(setup, org string) Here {
	if h.State != HereNoMatch || setup == "" {
		return h
	}
	h.Proposal.Kind, h.Proposal.Name, h.Proposal.Org = KindMember, setup, org
	return h
}
