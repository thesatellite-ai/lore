package wire

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/yamlerr"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// F17's warn rule under STORE TARGETS (PL-PORT-1). v0.1.17 asked before an
// `--everywhere` personal note that names a known org was written, because
// that is how one company's note ends up in another company's repos.
// STORE TARGETS removed `--everywhere`: `--to me` is the personal store,
// one place shown in all your repos, so the same question now guards every
// `--to me` write that lands where all repos read it: the personal store
// root (0.1.18 `personal.store: file://…`, and every store after
// `lore migrate run`) or v0.1.17's everywhere/ folder (`me/everywhere`
// until the migration). Before the migration a plain `--to me` in an org's
// repo still files into that org's folder (shown only in its repos), so it
// is not asked, exactly as in v0.1.17.
//
// The text is read for this one check only: placement never depends on it
// ("by where you are, never by guessing from the text", LORE_TEAM_SPEC F17).

// personalEverywhereTarget is `me/everywhere`, the v0.1.17 folder shown in
// every repo (attached under that name until `lore migrate run`).
const personalEverywhereTarget = attach.ReservedMe + targetSep + personalEverywhereV017

// confirmPersonalMentions refuses (E_NEEDS_CONFIRM) a write to a personal
// place every repo reads whose text names a known org or repo owner, unless
// the caller said yes. Operations without a `to` input, and every other
// target, pass untouched.
func (a *App) confirmPersonalMentions(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) error {
	if c.Yes {
		return nil
	}
	in, err := e.Decode(d)
	if err != nil {
		return nil // the engine's Run reports the decode error itself
	}
	to := ""
	var texts []string
	for _, tv := range in.Texts() {
		if tv.Field == engine.InputTarget {
			to = tv.Text
			continue
		}
		texts = append(texts, tv.Text)
	}
	shared, err := a.personalSharedTarget(ctx, to)
	if err != nil || !shared {
		return err
	}
	names, err := knownOrgNames(a.opts.Home)
	if err != nil {
		return err
	}
	found := mentions(strings.Join(texts, "\n"), names)
	if len(found) == 0 {
		return nil
	}
	q := "this note mentions " + strings.Join(found, ", ") + ", a known org or repo, and --to " + to +
		" is shown in all your repos; save it there anyway?"
	return errs.New(errs.NeedsConfirm, q).WithQuestion(q).
		WithHint("rerun with --yes to keep it in your personal store, or --to the org's store")
}

// personalSharedTarget reports whether to names a personal place every repo
// reads: `me` attached at the store root (or at v0.1.17's everywhere/ when no
// org applies), `me/.`, or `me/everywhere`.
func (a *App) personalSharedTarget(ctx context.Context, to string) (bool, error) {
	switch to {
	case attach.ReservedMe + targetSep + rowfs.FolderRoot, personalEverywhereTarget:
		return true, nil
	case attach.ReservedMe:
	default:
		return false, nil
	}
	mc, err := readMachineConfig(a.opts.Home)
	if err != nil {
		return false, err
	}
	if mc.personalURL() != "" {
		return true, nil // the 0.1.18 personal store: `me` is its root
	}
	local, err := localPersonalStores(ctx, a.opts)
	if err != nil {
		return false, err
	}
	for _, s := range local {
		if string(s.Name) == attach.ReservedMe {
			return s.Folder == personalEverywhereV017 || s.Folder == "" || s.Folder == rowfs.FolderRoot, nil
		}
	}
	return false, nil // no personal store: the engine answers the unknown target
}

// orgPatterns is the part of config.yaml the known-names list reads (org
// entries and their own:/repos: patterns, the v0.1.17 lorecfg shape).
type orgPatterns struct {
	Orgs []struct {
		Name  string   `yaml:"name"`
		Own   []string `yaml:"own,omitempty"`
		Repos []string `yaml:"repos,omitempty"`
	} `yaml:"orgs,omitempty"`
}

// knownOrgNames lists every org entry name of config.yaml and every literal
// repo owner and owner/name of the entries' own: and repos: patterns
// (v0.1.17 personalplace.KnownNames). Wildcard segments are skipped, and a
// bare repo name is never a name on its own ("app", "api" are too common a
// word to be a signal). A missing config.yaml lists nothing.
func knownOrgNames(home string) ([]string, error) {
	b, err := readConfigRaw(home)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	var c orgPatterns
	if err = yamlerr.Decode(b, &c, yamlerr.Lenient); err != nil {
		return nil, configYAMLErr(err)
	}
	set := map[string]bool{}
	for _, o := range c.Orgs {
		if n := strings.TrimSpace(o.Name); n != "" {
			set[n] = true
		}
		for _, pat := range append(slices.Clone(o.Own), o.Repos...) {
			owner, name := patternOwner(pat)
			if owner == "" {
				continue
			}
			set[owner] = true
			if name != "" {
				set[owner+"/"+name] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	slices.Sort(out)
	return out, nil
}

// patternOwner returns the literal owner and repo name of a repo pattern
// ("github.com/betalabs/*" → "betalabs", ""; "git@github.com:acme/app.git" →
// "acme", "app"). A wildcard segment is returned empty.
func patternOwner(pat string) (owner, name string) {
	p := strings.TrimSpace(pat)
	if i := strings.Index(p, "://"); i >= 0 {
		p = p[i+len("://"):]
	} else if at := strings.Index(p, "@"); at >= 0 && strings.Contains(p[at:], ":") {
		p = strings.Replace(p[at+1:], ":", "/", 1) // scp form git@host:owner/repo
	}
	if at := strings.LastIndex(p, "@"); at >= 0 && at < strings.Index(p+"/", "/") {
		p = p[at+1:] // user@host
	}
	segs := strings.Split(strings.Trim(p, "/"), "/")
	if len(segs) < 2 || isWildSeg(segs[1]) {
		return "", ""
	}
	owner = strings.ToLower(segs[1])
	if len(segs) >= 3 && !isWildSeg(segs[2]) {
		name = strings.ToLower(strings.TrimSuffix(segs[2], ".git"))
	}
	return owner, name
}

// isWildSeg reports a pattern segment holding a glob character.
func isWildSeg(s string) bool { return s == "" || strings.ContainsAny(s, "*?[") }

// mentions returns the names that appear in text as whole words,
// case-insensitively, sorted and deduplicated: "betalabs" matches
// "Betalabs's invoices" but not "betalabstools" (v0.1.17
// personalplace.Mentions).
func mentions(text string, names []string) []string {
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
	slices.Sort(out)
	return out
}
