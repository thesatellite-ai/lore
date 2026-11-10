package onboard

// github.go — the GitHub step's route decision and actionable field
// messages (ONB 4.3). The step asks only for the account; ChooseRoute
// decides how git reaches GitHub as it (an ssh route that logs in as it,
// else https with its gh login), and the key follows from the alias. Validation itself is pure and knows no
// aliases: the setup ops fill AliasLogins from the cached ssh -T table
// when they have it, and these texts say what to pick instead of
// "required".

import (
	"strings"
)

// AliasLogin is one ssh host alias of github.com and the GitHub user its
// ssh -T greeted ("" when it gave no answer).
type AliasLogin struct {
	Alias    string
	LogsInAs string
}

// AliasesFor lists the aliases of logins that log in as user (case-insensitive), in order.
func AliasesFor(user string, logins []AliasLogin) []string {
	var out []string
	for _, l := range logins {
		if user != "" && strings.EqualFold(l.LogsInAs, user) {
			out = append(out, l.Alias)
		}
	}
	return out
}

// AliasMismatchMessage is the host_alias field error when the picked alias
// logs in as another user than the account (the ssh_user check would fail
// the same way, later and over the network).
func AliasMismatchMessage(user, alias, got string, logins []AliasLogin) string {
	msg := alias + " logs in as " + got + ", not " + user
	if match := AliasesFor(user, logins); len(match) > 0 {
		return msg + "; pick " + match[0] + ", which logs in as " + user
	}
	return msg + "; pick the " + got + " account, or add a Host block for " + user + "'s key to ~/.ssh/config"
}

// KeyMatchIn is what github.key_matches decides on (ONB 4.3): the
// setup's account, route and recorded key, and what the probes saw.
// Paths are home-expanded; the display forms are the ~ ones.
type KeyMatchIn struct {
	User  string
	Alias string // "github.com" when the setup has no alias
	// Route is the setup's (RouteHTTPS: GHHasLogin decides).
	Route      GitRoute
	GHHasLogin bool
	// Key is the recorded key file as written (~/.ssh/x or ~/.ssh/x.pub,
	// "" = the alias's own key); KeyFile is the key itself (no .pub),
	// expanded, and KeyExists whether it is a file.
	Key       string
	KeyFile   string
	KeyExists bool
	// Fingerprint is the one recorded at save; FingerprintNow the route
	// key's fingerprint now ("" when either is unknown: not compared).
	Fingerprint    string
	FingerprintNow string
	// AliasKeys are the alias's IdentityFiles, expanded (empty: ssh's
	// default keys, so the key cannot be compared); AliasKeyShown is the
	// first as written.
	AliasKeys     []string
	AliasKeyShown string
	// KeyLogin / AliasLogin are the users ssh -T greeted with the key
	// alone (-i, IdentitiesOnly, no agent) and through the alias; "" when
	// it gave no answer (KeyAnswered / AliasAnswered false) or refused.
	KeyLogin      string
	KeyAnswered   bool
	AliasLogin    string
	AliasAnswered bool
}

// KeyMatch decides github.key_matches: the route git uses for this setup
// logs in as the setup's account. https: the account has a gh login. ssh
// with a recorded key: the key is the one the alias uses and logs in as
// the account. ssh without one: the alias itself logs in as the account.
// A recorded fingerprint that no longer matches the route's key fails (the
// key was swapped since the save). A key or alias nobody could ask about
// (no answer) is a warning, never a failure: the network or a passphrase
// is not a wrong setup.
func KeyMatch(in KeyMatchIn) (Status, string) {
	if in.Route == RouteHTTPS {
		if in.GHHasLogin {
			return StatusOK, "git connects over https with " + in.User + "'s gh login"
		}
		return StatusFail, "this setup connects over https with " + in.User + "'s gh login, but gh has no login for " + in.User + "; run gh auth login and log in as " + in.User
	}
	if in.Fingerprint != "" && in.FingerprintNow != "" && in.Fingerprint != in.FingerprintNow {
		return StatusFail, "the route's key is now " + in.FingerprintNow + ", not " + in.Fingerprint + " (saved with this setup); run the GitHub step again to record the key in use"
	}
	aliasSays := func() string {
		s := in.Alias + " uses " + in.AliasKeyShown
		if in.AliasLogin != "" {
			s += ", which logs in as " + in.AliasLogin
		}
		return s
	}
	if strings.TrimSpace(in.Key) == "" {
		switch {
		case in.AliasLogin != "" && strings.EqualFold(in.AliasLogin, in.User):
			return StatusOK, in.Alias + " logs in as " + in.AliasLogin
		case in.AliasLogin != "":
			return StatusFail, in.Alias + " logs in as " + in.AliasLogin + ", but this setup's account is " + in.User
		case in.AliasAnswered:
			return StatusFail, "GitHub refuses the key " + in.Alias + " offers; add it at https://github.com/settings/keys while logged in as " + in.User
		}
		return StatusWarn, in.Alias + " gave no answer (network or passphrase); cannot tell which account it logs in as"
	}
	keyName := strings.TrimSuffix(in.Key, PubKeySuffix)
	if !in.KeyExists {
		return StatusFail, "key file missing: " + keyName
	}
	keySays := func() string {
		switch {
		case in.KeyLogin != "":
			return keyName + " logs in as " + in.KeyLogin
		case in.KeyAnswered:
			return "GitHub refuses " + keyName
		}
		return keyName + " gave no answer"
	}
	if len(in.AliasKeys) > 0 && !slicesContain(in.AliasKeys, in.KeyFile) {
		if in.KeyLogin != "" && !strings.EqualFold(in.KeyLogin, in.User) {
			return StatusFail, keySays() + ", but this setup's account is " + in.User + "; " + aliasSays()
		}
		return StatusFail, "this setup records " + in.Key + " but " + aliasSays() + "; git over " + in.Alias + " never uses the recorded key"
	}
	switch {
	case in.KeyLogin != "" && !strings.EqualFold(in.KeyLogin, in.User):
		return StatusFail, keySays() + ", but this setup's account is " + in.User
	case in.KeyLogin == "" && in.KeyAnswered:
		return StatusFail, keySays() + "; add " + keyName + ".pub at https://github.com/settings/keys while logged in as " + in.User
	case in.KeyLogin == "":
		return StatusWarn, keySays() + " (network or passphrase); cannot tell which account it logs in as"
	}
	return StatusOK, keySays()
}

func slicesContain(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// RouteAlias is one ssh route ChooseRoute may pick: an alias of
// github.com (or github.com itself) with what its ssh -T answered.
type RouteAlias struct {
	Alias string
	// KeyFile is the alias's first IdentityFile as written ("" = ssh's
	// default keys); KeyMissing: it is set but no file is there.
	KeyFile    string
	KeyMissing bool
	// LogsInAs is the user ssh -T greeted ("" = refused or no answer);
	// Pending: no answer yet (the probe is still running).
	LogsInAs string
	Pending  bool
}

// RouteChoice is how lore reaches GitHub as one account (ONB 4.3, owner
// 2026-11-07: the person picks only the account).
type RouteChoice struct {
	// Route is RouteSSH or RouteHTTPS; "" when no route works.
	Route GitRoute `json:"route,omitempty"`
	// Alias and KeyFile are the ssh route (github.com = no alias).
	Alias   string `json:"alias,omitempty"`
	KeyFile string `json:"key_file,omitempty"`
	// Line is the one read-only sentence the page shows; Fix says what to
	// do when no route works (Route ""); Pending names the aliases
	// that had not answered yet.
	Line    string   `json:"line"`
	Fix     string   `json:"fix,omitempty"`
	Pending []string `json:"pending,omitempty"`
}

// Found reports a usable route.
func (c RouteChoice) Found() bool { return c.Route != "" }

// ChooseRoute decides how git reaches GitHub as user: (1) an ssh route
// that logs in as user, a named alias before plain github.com (whose
// default key may change under it), in config order; (2) else https with
// user's gh login; (3) else no route, with the fix. While no route is
// known yet and some aliases have not answered, it says so instead of
// falling back to https (the slow alias may be the right one).
func ChooseRoute(user string, aliases []RouteAlias, ghLogins []string) RouteChoice {
	user = strings.TrimSpace(user)
	if user == "" {
		return RouteChoice{Line: "pick the GitHub account first", Fix: "pick the GitHub account first"}
	}
	var best *RouteAlias
	var pending []string
	for i := range aliases {
		a := &aliases[i]
		if a.Pending {
			pending = append(pending, a.Alias)
			continue
		}
		if a.KeyMissing || !strings.EqualFold(a.LogsInAs, user) {
			continue
		}
		if best == nil || (best.Alias == GitHubHostName && a.Alias != GitHubHostName) {
			best = a
		}
	}
	if best != nil {
		return RouteChoice{Route: RouteSSH, Alias: best.Alias, KeyFile: best.KeyFile, Line: sshRouteLine(user, *best)}
	}
	if len(pending) > 0 {
		return RouteChoice{Pending: pending,
			Line: "still asking " + strings.Join(pending, ", ") + " which account it logs in as",
			Fix:  "still checking which ssh route logs in as " + user + " (no answer yet from " + strings.Join(pending, ", ") + "); check again in a moment"}
	}
	for _, g := range ghLogins {
		if strings.EqualFold(g, user) {
			return RouteChoice{Route: RouteHTTPS,
				Line: "lore will connect as " + user + " over https with " + user + "'s gh login (no ssh route logs in as " + user + ")"}
		}
	}
	fix := "nothing on this machine logs in to GitHub as " + user + ": run gh auth login and log in as " + user +
		", or add a Host block for " + user + "'s key to ~/.ssh/config (HostName github.com, IdentityFile <that key>, IdentitiesOnly yes)"
	return RouteChoice{Line: "no way to reach GitHub as " + user + " yet", Fix: fix}
}

// GitHubHostName is the plain host: no alias, ssh's own config decides.
const GitHubHostName = "github.com"

// sshRouteLine is the read-only line of an ssh route.
func sshRouteLine(user string, a RouteAlias) string {
	via := GitHubHostName
	if a.Alias != GitHubHostName {
		via = "alias " + a.Alias
	}
	if a.KeyFile != "" {
		via += ", key " + KeyName(a.KeyFile)
	}
	return "lore will connect as " + user + " via your ssh config (" + via + ")"
}

// KeyName is a key path's file name without .pub ("~/.ssh/id_x.pub" → "id_x").
func KeyName(path string) string {
	base := path
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, PubKeySuffix)
}
