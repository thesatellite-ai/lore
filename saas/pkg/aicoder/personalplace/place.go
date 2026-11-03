// Package personalplace decides which personal-store folder a `--to me`
// note is filed in (LORE_TEAM_SPEC F17, D11, TEAM-021), and parses the
// `--to` flag every write command shares (§1 Flags, F14).
//
// The rule is "by where you are, never by guessing from the text": in a
// repo that resolved to org X the note goes to the personal folder X/ and
// loads only in X's repos; with no org it goes to everywhere/; --everywhere
// files it under everywhere/ on purpose. The text is read for one thing
// only: an --everywhere note that names a known org, project folder or repo
// owner asks for confirmation, because that is how one company's note ends
// up in another company's repos.
//
// Pure functions only; the CLI supplies the inputs and does the prompt.
package personalplace

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/repourl"
)

// Errors, so the CLI can map them without matching text.
var (
	// ErrEverywhereNeedsConfirm: an --everywhere note names a known org or
	// repo and there is no TTY to ask and no --yes.
	ErrEverywhereNeedsConfirm = errors.New("personalplace: --everywhere note names a known org or repo; confirm with --yes")
	// ErrEverywhereWithoutMe: --everywhere only makes sense with --to me.
	ErrEverywhereWithoutMe = errors.New("personalplace: --everywhere needs --to me")
	// ErrOtherOrg: --to me/<org> or --to <org> named an org that did not
	// resolve for this repo (orgs never mix, TEAM-009).
	ErrOtherOrg = errors.New("personalplace: target names another org than the one this repo resolved to")
)

// Inputs for Place.
type Inputs struct {
	// Org is the resolved org entry name; "" when no org applies.
	Org string
	// Everywhere is the --everywhere flag.
	Everywhere bool
	// Text is the note (title and body) — read only for the confirm check.
	Text string
	// KnownNames are the names that make an --everywhere note suspicious
	// (KnownNames(cfg) plus linked project folders once stores exist).
	KnownNames []string
	// TTY: a person can be asked. Yes: --yes was passed.
	TTY, Yes bool
}

// Placement is the decision.
type Placement struct {
	// Folder is the personal-store folder: an org name or
	// lorecfg.PersonalEverywhere.
	Folder string
	// Reason is the one line `lore explain` and the write message print.
	Reason string
	// NeedsConfirm: ask the person before writing (TTY and no --yes).
	NeedsConfirm bool
	// Mentions are the known names found in an --everywhere note.
	Mentions []string
}

// Place runs F17.
func Place(in Inputs) (Placement, error) {
	if !in.Everywhere {
		if in.Org != "" {
			return Placement{Folder: in.Org,
				Reason: fmt.Sprintf("personal %s/ (loads only in %s repos)", in.Org, in.Org)}, nil
		}
		return Placement{Folder: lorecfg.PersonalEverywhere,
			Reason: fmt.Sprintf("personal %s/ (no org applies here)", lorecfg.PersonalEverywhere)}, nil
	}
	p := Placement{Folder: lorecfg.PersonalEverywhere,
		Reason:   fmt.Sprintf("personal %s/ (--everywhere: loads in every repo)", lorecfg.PersonalEverywhere),
		Mentions: Mentions(in.Text, in.KnownNames)}
	if len(p.Mentions) == 0 || in.Yes {
		return p, nil
	}
	if !in.TTY {
		return p, fmt.Errorf("%w (it mentions %s)", ErrEverywhereNeedsConfirm, strings.Join(p.Mentions, ", "))
	}
	p.NeedsConfirm = true
	return p, nil
}

// Mentions returns the known names that appear in text as whole words,
// case-insensitively, sorted and deduplicated. "acme" matches "Acme's
// invoices" but not "acmetools".
func Mentions(text string, names []string) []string {
	lower := strings.ToLower(text)
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || seen[n] {
			continue
		}
		re := regexp.MustCompile(`(^|[^a-z0-9])` + regexp.QuoteMeta(n) + `($|[^a-z0-9])`)
		if re.MatchString(lower) {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// KnownNames lists every org entry name and every literal repo owner and
// owner/name in the entries' own: and repos: patterns. Wildcard segments
// are skipped; bare repo names are not included on their own (too common a
// word — "app", "api" — to be a signal).
func KnownNames(cfg *lorecfg.Config) []string {
	set := map[string]bool{}
	for _, o := range cfg.Orgs {
		set[o.Name] = true
		for _, pat := range append(append([]string{}, o.Own...), o.Repos...) {
			cp, err := repourl.Compile(pat)
			if err != nil {
				continue
			}
			segs := strings.Split(cp.String(), "/")[1:] // drop the host
			if len(segs) >= 1 && !isWild(segs[0]) {
				set[segs[0]] = true
			}
			if len(segs) >= 2 && !isWild(segs[0]) && !isWild(segs[1]) {
				set[segs[0]+"/"+segs[1]] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func isWild(s string) bool { return s == repourl.WildSegment || s == repourl.WildAny }
