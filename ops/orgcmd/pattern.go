// pattern.go — the refusal of an own/repos pattern given on the command
// line (org create --own/--repos, org config set --key own|repos). A
// pattern with no host gets the pattern it most likely meant and the same
// command with it, so the hint runs where printed (bug hunt H-112: the
// error only said "needs a host").

package orgcmd

import (
	"strings"

	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
)

// patternHint is the generic hint for a pattern Compile refuses for a
// reason other than a missing host.
const patternHint = "write host/owner/repo with ** as a whole segment and * as a segment or inside one, like github.com/agency/client-*"

// checkPatterns refuses the first of vals repourl.Compile refuses, as
// E_INVALID_INPUT with what (the command and flag) in front. For a
// pattern without a host the hint is rerun(bad, suggested): the caller's
// command with the suggestion in place of the bad value, its owner taken
// from known (the command's other patterns) when one names it.
func checkPatterns(what string, vals, known []string, rerun func(bad, fixed string) string) error {
	for _, p := range vals {
		_, err := repourl.Compile(p)
		if err == nil {
			continue
		}
		e := errs.Wrap(errs.InvalidInput, what+": "+strings.TrimPrefix(err.Error(), "repourl: "), err)
		if h := repourl.AsHostless(err); h != nil {
			fixed := repourl.SuggestWith(h.Suggest, known)
			return errs.Wrap(errs.InvalidInput, what+": pattern "+strconvQuote(p)+" "+strings.Replace(h.Why(), h.Suggest, fixed, 1), err).
				WithHint(rerun(p, fixed))
		}
		return e.WithHint(patternHint)
	}
	return nil
}

// replaced is vals with every bad entry swapped for fixed.
func replaced(vals []string, bad, fixed string) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		if v == bad {
			v = fixed
		}
		out[i] = v
	}
	return out
}

// flagArgs is one `--flag 'value'` per value, quoted for a POSIX shell.
func flagArgs(flag string, vals []string) string {
	var b strings.Builder
	for _, v := range vals {
		b.WriteString(" --" + flag + " " + shellQuote(v))
	}
	return b.String()
}

// shellQuote quotes s for a POSIX shell when it holds anything but plain
// word characters (a pattern's `*`, a name's space).
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.@:/+=") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// CheckCreatePatterns refuses an org create whose --own or --repos holds
// a pattern repourl.Compile refuses, before the confirmation is shown and
// before the store is reached (the entry's own validation came only after
// the store was cloned).
func CheckCreatePatterns(in CreateIn) error {
	all := append(append([]string{}, in.Own...), in.Repos...)
	if err := checkPatterns("org create --own", in.Own, all, func(bad, fixed string) string {
		return createRerun(in, replaced(in.Own, bad, fixed), in.Repos)
	}); err != nil {
		return err
	}
	return checkPatterns("org create --repos", in.Repos, all, func(bad, fixed string) string {
		return createRerun(in, in.Own, replaced(in.Repos, bad, fixed))
	})
}

// createRerun is the org create the person typed with own and repos as
// given, confirmed with --yes.
func createRerun(in CreateIn, own, repos []string) string {
	cmd := "lore org create --org " + shellQuote(in.Org) + " --store " + shellQuote(in.Store)
	if in.OrgFolder != "" && in.OrgFolder != DefaultOrgFolder {
		cmd += " --org-folder " + shellQuote(in.OrgFolder)
	}
	cmd += flagArgs("own", own) + flagArgs("repos", repos)
	for _, f := range [][2]string{{"key", in.Key}, {"name", in.Name}, {"handle", in.Handle}} {
		if f[1] != "" {
			cmd += " --" + f[0] + " " + shellQuote(f[1])
		}
	}
	return cmd + " --yes"
}
