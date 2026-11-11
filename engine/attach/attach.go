// Package attach reads a repo's .lore/stores.yaml (STORE_SERVER_SPEC 7.1)
// and opens every store it lists through the backends the binary
// registered (store.Openers), giving the engine its attached stores
// (STORES DIRECT: a repo attaches any number of stores itself, no org
// needed).
//
// Rules enforced: names are snake_case, unique and not reserved (repo, me,
// team, company: CFG2, STORE TARGETS); a url must name a backend scheme
// (E_STORE_SCHEME); a credential in a url is cleaned on read (D24, SECRETS
// 14.2 row 16; NormalizeURL); unknown keys are refused, so a typo never
// silently drops read_only.
//
// Layer L4 (beside the engine): imports store and engine only.
package attach

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/yamlerr"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// FileName is the file's path inside a repo.
const FileName = ".lore/stores.yaml"

// Reserved names, never allowed as a store name (spec 7.1).
const (
	ReservedRepo    = "repo"
	ReservedMe      = "me"
	ReservedTeam    = "team"
	ReservedCompany = "company"
)

// Reserved lists the reserved names.
func Reserved() []string { return []string{ReservedRepo, ReservedMe, ReservedTeam, ReservedCompany} }

// Store is one entry of stores.yaml.
type Store struct {
	Name store.Source `yaml:"name"`
	// URL is the store's location: git@host:path (scp form), ssh://,
	// https://, http:// or file://. Never echoed in an error (it may hold a
	// token).
	URL string `yaml:"url"`
	// Folder is the optional project folder inside the store to link.
	Folder   string `yaml:"folder,omitempty"`
	ReadOnly bool   `yaml:"read_only,omitempty"`
	// Branch pins the store branch (a staging branch, v0.1.17's org entry
	// `branch:`); empty follows the store's default branch. Only a git
	// store has branches (a file:// store with a branch is E_STORE_SCHEME).
	Branch string `yaml:"branch,omitempty"`
}

// Config is the whole file. Order matters: it is the stores' precedence
// (BATCH 15), and the personal store comes after all of them.
type Config struct {
	Stores []Store `yaml:"stores"`
}

// Parse reads stores.yaml. Every problem is reported at once
// (E_CONFIG_INVALID), naming the entry by position and name.
func Parse(b []byte) (Config, error) {
	var c Config
	// Strict: every key of stores.yaml is lore's, so a typo is refused with
	// its line and key path (yamlerr), never yaml's Go type names; an empty
	// file lists no stores.
	if err := yamlerr.Decode(b, &c, yamlerr.Strict); err != nil {
		return Config{}, errs.Wrap(errs.ConfigInvalid, "attach: "+FileName+" is not valid: "+err.Error(), err)
	}
	var problems []string
	seen := map[store.Source]bool{}
	for i, s := range c.Stores {
		at := "store " + strconv.Itoa(i+1) + " (" + string(s.Name) + ")"
		base, folder, isFolder := strings.Cut(string(s.Name), "/")
		switch {
		case base == ReservedMe:
			// `me` and every `me/<folder>` is the personal store, which
			// lives only in the machine config: never in a committed file
			// (spec 7.1, CFG2), so a teammate never attaches your store.
			problems = append(problems, at+": "+ReservedMe+" is your personal store and is never named in "+FileName)
		case isFolder && (!row.ValidName(base) || folder == "" || folder != s.Folder):
			// A recorded `--to <store>/<folder>` target (wire): the name is
			// "<store>/<folder>" and its folder key repeats the folder.
			problems = append(problems, at+": a <store>/<folder> name needs a snake_case store and a matching folder key")
		case !isFolder && !row.ValidName(string(s.Name)):
			problems = append(problems, at+": the name must be snake_case")
		case slices.Contains(Reserved(), base) && !isFolder:
			problems = append(problems, at+": "+string(s.Name)+" is a reserved name")
		case seen[s.Name]:
			problems = append(problems, at+": the name is used twice")
		}
		seen[s.Name] = true
		if s.URL == "" {
			problems = append(problems, at+": no url")
		}
	}
	if len(problems) > 0 {
		return Config{}, errs.New(errs.ConfigInvalid, "attach: "+FileName+": "+strings.Join(problems, "; "))
	}
	return c, nil
}

// scpForm is git's "user@host:path" shorthand, which net/url cannot parse.
var scpForm = regexp.MustCompile(`^([A-Za-z0-9._-]+)@([A-Za-z0-9.-]+):([^/].*)$`)

// NormalizeURL turns the scp form into ssh:// and refuses a url carrying a
// credential: a password, or a token-shaped user name (OV4: https://<token>@host
// is caught too; repourl.Strip decides, D24/SQ13). An ssh login user is
// kept. It is the strict form for a url a person is adding (lore store add,
// wire's canonical form); a url read back from stores.yaml goes through
// CleanURL instead. The url never appears in an error.
func NormalizeURL(raw string) (string, error) {
	u, err := parseStoreURL(raw)
	if err != nil {
		return "", err
	}
	_, removed, err := repourl.Strip(u.String())
	if err != nil || removed.Warn() {
		return "", errs.New(errs.URLUserinfo, "attach: a store url holds a credential").
			WithHint("remove the credential from stores.yaml and use your git credential helper")
	}
	return u.String(), nil
}

// CleanURL is the read form (SECRETS 14.2 row 16): a hand-edited
// stores.yaml url with a credential is cleaned on read, not refused, so a
// teammate's checkout still opens; the commit hook keeps refusing the
// committed file. A value Strip cannot read safely is refused.
func CleanURL(raw string) (string, error) {
	u, err := parseStoreURL(raw)
	if err != nil {
		return "", err
	}
	clean, _, err := repourl.Strip(u.String())
	if err != nil {
		return "", errs.New(errs.URLUserinfo, "attach: a store url cannot be read safely (it may hold a credential)").
			WithHint("remove the credential from stores.yaml and use your git credential helper")
	}
	return clean, nil
}

// parseStoreURL turns the scp form into ssh:// and parses the url.
func parseStoreURL(raw string) (*url.URL, error) {
	if m := scpForm.FindStringSubmatch(raw); m != nil {
		raw = "ssh://" + m[1] + "@" + m[2] + "/" + m[3]
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return nil, errs.New(errs.StoreScheme, "attach: a store url has no scheme")
	}
	// file://C:/x (a Windows path written after file://) is the drive path
	// file:///C:/x; read as is, "C:" was a host and the drive was lost from
	// every later use of the cleaned url.
	fileuri.FoldDriveHost(u)
	return u, nil
}

// Opened is one opened store: what the engine attaches, plus the linked
// project folder (the "linked store folder" policy level, O1).
type Opened struct {
	engine.Attached
	Folder string
}

// Open opens every store of c in order through openers. On any failure the
// stores opened so far are closed and nothing is returned, so a half-open
// set never reaches the engine.
func Open(ctx context.Context, c Config, openers *store.Openers, opts store.OpenOptions) ([]Opened, error) {
	var out []Opened
	fail := func(err error) ([]Opened, error) {
		for _, a := range out {
			_ = a.Backend.Close() // best effort: the open error is what the caller needs
		}
		return nil, err
	}
	for _, s := range c.Stores {
		u, err := CleanURL(s.URL) // read form: cleaned, not refused (14.2 row 16)
		if err != nil {
			return fail(storeErr(s.Name, err))
		}
		o := opts
		o.ReadOnly = s.ReadOnly
		b, err := openers.Open(ctx, u, o)
		if err != nil {
			return fail(storeErr(s.Name, err))
		}
		out = append(out, Opened{Attached: engine.Attached{Name: s.Name, Backend: b, ReadOnly: s.ReadOnly}, Folder: s.Folder})
	}
	return out, nil
}

// storeErr is the error of a store that could not be attached: the message
// names the store and the reason, and keeps the cause's hint. The reason is
// the cause's own lore message when it has one; a foreign error (driver,
// git, os text that may hold a path or a url) is named only by its code, so
// no credential can leak through it.
func storeErr(name store.Source, err error) error {
	code := errs.CodeOf(err)
	reason := string(code)
	var le *errs.Error
	if errors.As(err, &le) && le.Message != "" {
		reason = le.Message
	}
	out := errs.Wrap(code, "attach: store "+string(name)+" could not be opened: "+reason, err)
	if le != nil && le.Hint != "" {
		out = out.WithHint(le.Hint)
	}
	return out
}
