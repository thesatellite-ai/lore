// Package attach reads a repo's .lore/stores.yaml (STORE_SERVER_SPEC 7.1)
// and opens every store it lists through the backends the binary
// registered (store.Openers), giving the engine its attached stores.
//
// Rules enforced: names are snake_case, unique and not reserved (repo, me,
// team, company: CFG2, STORE TARGETS); a url must name a backend scheme
// (E_STORE_SCHEME) and may not carry a password (E_URL_CREDENTIALS: no
// credentials in urls, D24); unknown keys are refused, so a typo never
// silently drops read_only.
//
// Layer L4 (beside the engine): imports store and engine only.
package attach

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/engine"
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
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) { // an empty file lists no stores
		return Config{}, errs.Wrap(errs.ConfigInvalid, "attach: "+FileName+" is not valid", err)
	}
	var problems []string
	seen := map[store.Source]bool{}
	for i, s := range c.Stores {
		at := "store " + strconv.Itoa(i+1) + " (" + string(s.Name) + ")"
		base, folder, isFolder := strings.Cut(string(s.Name), "/")
		switch {
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
// password (ssh keeps its login user, SQ13). The url never appears in an
// error.
func NormalizeURL(raw string) (string, error) {
	if m := scpForm.FindStringSubmatch(raw); m != nil {
		raw = "ssh://" + m[1] + "@" + m[2] + "/" + m[3]
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return "", errs.New(errs.StoreScheme, "attach: a store url has no scheme")
	}
	if _, has := u.User.Password(); has {
		return "", errs.New(errs.URLUserinfo, "attach: a store url holds a password").
			WithHint("remove the credential from stores.yaml and use your git credential helper")
	}
	return u.String(), nil
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
		u, err := NormalizeURL(s.URL)
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
