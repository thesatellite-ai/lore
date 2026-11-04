// Package identity answers "who is writing?" for the 0.1.18 core (layer L3,
// an adapter: it reads the environment, ~/.lore files and `git config`).
//
// Ported from the v0.1.17 chain (saas/pkg/aicoder/identity/resolve.go,
// org.go, noorg.go) without importing it, with one change made by CFG9:
// the persisted identity lives in ~/.lore/config.yaml (`identity:` at the
// top level, `orgs[].identity` per org), never in ~/.lore/identity.yaml
// (that file is read as a last fallback until `lore migrate run` moves it).
//
// Resolution, first answer wins (CONFIG_GUIDE 5.1):
//
//  1. Inputs.FlagActor, then LORE_ACTOR (inside an org only agent:<label>
//     actors are accepted; a human key there is ignored with a note);
//  2. the resolved org entry's identity (Inputs.Org);
//  3. the top-level `identity:` of config.yaml;
//  4. the v0.1.17 ~/.lore/identity.yaml, until migrated;
//  5. git config user.email, $USER@host, the machine id, the persisted
//     random ~/.lore/identity file, and last an ephemeral key (noted).
//
// When orgs exist but none resolved, every candidate equal to an org's key
// is skipped (F10 rule 6: a personal repo is never stamped with the work
// identity). The actor id is ActorID: the v0.1.17 deterministic id of the
// stable key, so both versions name a person with the same act_ id.
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Environment variables read by the chain.
const (
	// EnvActor names the acting agent for this session (agent:<label>).
	EnvActor = "LORE_ACTOR"
	// EnvSession is the agent session id (`lore agent start`).
	EnvSession = "LORE_SESSION"
)

// Actor kinds, the prefix of a stable key ("human:alice@x").
const (
	KindHuman = "human"
	KindAgent = "agent"
	KindAuto  = "auto"
)

// knownKinds are the stable-key prefixes that name an actors.kind value
// (dbent/schema/enums.go ActorKind); anything else reads as human.
var knownKinds = []string{KindHuman, KindAgent, "hook", "plugin", "cron", "system"}

// kindSep separates a stable key's kind from its value.
const kindSep = ":"

// Files under the lore home.
const (
	// LegacyFile is v0.1.17's identity file, migrated into config.yaml.
	LegacyFile = "identity.yaml"
	// SaltFile is the persisted random identity (step 5, last stable one).
	SaltFile = "identity"
	// saltHexLen is how many hex characters of the salt form the key.
	saltHexLen = 12
	// saltBytes is the random bytes written to SaltFile.
	saltBytes = 16
	// ephemeralBytes is the random bytes of the last-resort key.
	ephemeralBytes = 6
	// fileMode / dirMode keep identity files private to the user.
	fileMode = 0o600
	dirMode  = 0o700
)

// machineIDFiles are read in order for the machine-id step (Linux).
var machineIDFiles = []string{"/etc/machine-id", "/var/lib/dbus/machine-id"}

// Step names which step answered, for whoami and doctor.
type Step string

// The steps.
const (
	StepFlag      Step = "--actor"
	StepEnv       Step = EnvActor
	StepOrg       Step = "org entry (config.yaml)"
	StepTop       Step = "top-level identity (config.yaml)"
	StepLegacy    Step = "~/.lore/identity.yaml"
	StepGit       Step = "git config user.email"
	StepOSUser    Step = "OS user"
	StepMachine   Step = "machine-id"
	StepSalt      Step = "persisted salt"
	StepEphemeral Step = "ephemeral salt"
)

// Entry is one identity block of config.yaml (`identity:` or
// `orgs[].identity`), the v0.1.17 lorecfg.OrgIdentity shape.
type Entry struct {
	Key    string `yaml:"key,omitempty"`
	Name   string `yaml:"name,omitempty"`
	Handle string `yaml:"handle,omitempty"`
}

// Org is one org entry as far as identity cares.
type Org struct {
	Name     string `yaml:"name"`
	Identity Entry  `yaml:"identity,omitempty"`
}

// Config is the part of ~/.lore/config.yaml identity reads. Unknown keys are
// ignored here: the file holds many settings this package does not own.
type Config struct {
	Identity Entry `yaml:"identity,omitempty"`
	Orgs     []Org `yaml:"orgs,omitempty"`
}

// Resolved is who is writing.
type Resolved struct {
	// StableKey is the canonical identity ("human:alice@example.com").
	StableKey string
	// DisplayName is shown in output and stored on the actor row.
	DisplayName string
	// Kind is the actors.kind value.
	Kind string
	// Step is the step that answered.
	Step Step
	// Org is the org entry the identity belongs to ("" outside an org).
	Org string
	// Handle is the roster handle, display only.
	Handle string
	// Notes are one-line warnings worth printing (never fatal).
	Notes []string
}

// Stable reports whether the next run on this machine gets the same key.
func (r Resolved) Stable() bool { return r.Step != StepEphemeral && r.StableKey != "" }

// commitEmailDomain is the non-routable domain of a commit e-mail made for
// a key that holds no address (agent:<label>, a machine key).
const commitEmailDomain = "lore.invalid"

// CommitSignature is the name and e-mail that sign a store commit as this
// identity when git has none of its own (FLAG4): the display name (the
// key's value when unset) and the key's e-mail for a human:<email> key,
// else the key made into a non-routable address (agent:ci becomes
// agent.ci@lore.invalid, as v0.1.17's signatureEmail did). Both are ""
// for an empty key.
func (r Resolved) CommitSignature() (name, email string) {
	if r.StableKey == "" {
		return "", ""
	}
	kind, value, ok := strings.Cut(r.StableKey, kindSep)
	if !ok {
		kind, value = KindHuman, r.StableKey
	}
	name = r.DisplayName
	if name == "" {
		name = value
	}
	if kind == KindHuman && strings.Contains(value, "@") {
		return name, value
	}
	return name, strings.ReplaceAll(r.StableKey, kindSep, ".") + "@" + commitEmailDomain
}

// Inputs is everything Resolve reads; every field is a seam.
type Inputs struct {
	// Home is the lore home (~/.lore). Required.
	Home string
	// FlagActor is --actor, when a command offers one.
	FlagActor string
	// Getenv reads LORE_ACTOR, USER, USERNAME (os.Getenv when nil).
	Getenv func(string) string
	// Config is config.yaml (ReadConfig); zero means none.
	Config Config
	// Org is the org entry resolved for this repo; nil means none.
	Org *Org
	// GitEmail returns `git config user.email` run in the repo ("" when
	// unset); nil runs git in Dir.
	GitEmail func(ctx context.Context) string
	// Dir is the folder git runs in when GitEmail is nil.
	Dir string
}

// ErrOrgIdentityMissing is returned when the resolved org entry has no
// identity key.
var ErrOrgIdentityMissing = errors.New("identity: the resolved org entry has no identity")

// Resolve runs the chain. It fails only inside an org whose entry has no
// key (fail closed: an org repo is never stamped with a guessed identity).
func Resolve(ctx context.Context, in Inputs) (Resolved, error) {
	getenv := in.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if in.Org != nil {
		return resolveOrg(in, getenv)
	}
	orgKeys := map[string]bool{}
	for _, o := range in.Config.Orgs {
		if k := strings.TrimSpace(o.Identity.Key); k != "" {
			orgKeys[k] = true
		}
	}
	var skipped []Step
	take := func(r Resolved) bool {
		if orgKeys[r.StableKey] {
			skipped = append(skipped, r.Step)
			return false
		}
		return true
	}
	for _, c := range []struct {
		v    string
		step Step
	}{{in.FlagActor, StepFlag}, {getenv(EnvActor), StepEnv}} {
		if v := strings.TrimSpace(c.v); v != "" {
			if r := fromKey(v, "", c.step); take(r) {
				return r, nil
			}
		}
	}
	if top := in.Config.Identity; strings.TrimSpace(top.Key) != "" {
		r := fromKey(strings.TrimSpace(top.Key), top.Name, StepTop)
		r.Handle = top.Handle
		return r, nil
	}
	for _, try := range []func() (Resolved, bool){
		func() (Resolved, bool) { return legacyFile(in.Home) },
		func() (Resolved, bool) { return gitEmail(ctx, in) },
		func() (Resolved, bool) { return osUser(getenv) },
		machineID,
	} {
		if r, ok := try(); ok && take(r) {
			return withSkipNote(r, skipped), nil
		}
	}
	if r, ok := persistedSalt(in.Home); ok {
		return withSkipNote(r, skipped), nil
	}
	rnd := randHex(ephemeralBytes)
	r := Resolved{StableKey: KindAuto + kindSep + "ephemeral-" + rnd, DisplayName: "ephemeral-" + rnd, Kind: KindAuto, Step: StepEphemeral,
		Notes: []string{"no stable identity found; set one with `lore identity set` (identity: in ~/.lore/config.yaml)"}}
	return withSkipNote(r, skipped), nil
}

// resolveOrg is the identity inside a resolved org (v0.1.17 org.go): an
// agent actor from --actor/LORE_ACTOR, else the entry's key.
func resolveOrg(in Inputs, getenv func(string) string) (Resolved, error) {
	var notes []string
	actor, step := strings.TrimSpace(in.FlagActor), StepFlag
	if actor == "" {
		actor, step = strings.TrimSpace(getenv(EnvActor)), StepEnv
	}
	if actor != "" {
		if kindOf(actor) == KindAgent && len(actor) > len(KindAgent+kindSep) {
			r := fromKey(actor, "", step)
			r.Org = in.Org.Name
			return r, nil
		}
		notes = append(notes, string(step)+" ignored: in "+in.Org.Name+" only agent:<label> actors are accepted; you act as the org identity")
	}
	key := strings.TrimSpace(in.Org.Identity.Key)
	if key == "" {
		return Resolved{}, errs.Wrap(errs.IdentityFailed, "identity: org "+in.Org.Name+" has no identity", ErrOrgIdentityMissing).
			WithHint("run `lore identity set --org " + in.Org.Name + " --key human:<your email>`")
	}
	r := fromKey(key, in.Org.Identity.Name, StepOrg)
	r.Kind, r.Org, r.Handle, r.Notes = KindHuman, in.Org.Name, in.Org.Identity.Handle, notes
	return r, nil
}

// withSkipNote adds the one note naming the sources skipped because they
// equal an org identity (never the key itself).
func withSkipNote(r Resolved, skipped []Step) Resolved {
	if len(skipped) == 0 {
		return r
	}
	names := make([]string, len(skipped))
	for i, s := range skipped {
		names[i] = string(s)
	}
	r.Notes = append(r.Notes, "skipped "+strings.Join(names, " and ")+" (an org identity); set a top-level identity: in ~/.lore/config.yaml")
	return r
}

// fromKey builds a Resolved from a stable key.
func fromKey(key, display string, step Step) Resolved {
	if display = strings.TrimSpace(display); display == "" {
		display = displayOf(key)
	}
	return Resolved{StableKey: key, DisplayName: display, Kind: kindOf(key), Step: step}
}

// displayOf turns "human:alice@example.com" into "alice".
func displayOf(key string) string {
	_, v, ok := strings.Cut(key, kindSep)
	if !ok {
		return key
	}
	if at := strings.IndexByte(v, '@'); at > 0 {
		return v[:at]
	}
	return v
}

// kindOf is the actors.kind of a stable key (unknown or anon: human).
func kindOf(key string) string {
	k, _, ok := strings.Cut(key, kindSep)
	if !ok {
		return KindHuman
	}
	for _, known := range knownKinds {
		if k == known {
			return k
		}
	}
	if k == KindAuto {
		return KindAuto
	}
	return KindHuman
}

// legacyFile reads v0.1.17's ~/.lore/identity.yaml.
func legacyFile(home string) (Resolved, bool) {
	e, ok, err := ReadLegacy(home)
	if err != nil || !ok {
		return Resolved{}, false
	}
	return fromKey(e.Key, e.Name, StepLegacy), true
}

// legacyWire is the v0.1.17 identity.yaml shape.
type legacyWire struct {
	DisplayName string `yaml:"display_name"`
	StableKey   string `yaml:"stable_key"`
}

// ReadLegacy reads <home>/identity.yaml (v0.1.17) as an Entry; ok=false when
// the file is absent or names nobody.
func ReadLegacy(home string) (Entry, bool, error) {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(home, LegacyFile)))
	if errors.Is(err, fs.ErrNotExist) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, errs.Wrap(errs.Internal, "identity: read "+LegacyFile, err)
	}
	var w legacyWire
	if err := yaml.Unmarshal(b, &w); err != nil {
		return Entry{}, false, errs.Wrap(errs.ConfigInvalid, "identity: "+LegacyFile+" is not valid YAML", err)
	}
	key, name := strings.TrimSpace(w.StableKey), strings.TrimSpace(w.DisplayName)
	if key == "" && name != "" {
		key = KindHuman + kindSep + name
	}
	if key == "" {
		return Entry{}, false, nil
	}
	return Entry{Key: key, Name: name}, true, nil
}

// gitEmail is `git config user.email`.
func gitEmail(ctx context.Context, in Inputs) (Resolved, bool) {
	var email string
	if in.GitEmail != nil {
		email = in.GitEmail(ctx)
	} else {
		cmd := exec.CommandContext(ctx, "git", "config", "user.email")
		cmd.Dir = in.Dir
		out, err := cmd.Output()
		if err != nil {
			return Resolved{}, false
		}
		email = string(out)
	}
	if email = strings.TrimSpace(email); email == "" {
		return Resolved{}, false
	}
	return fromKey(KindHuman+kindSep+email, "", StepGit), true
}

// osUser is human:$USER@<hostname>.
func osUser(getenv func(string) string) (Resolved, bool) {
	user := getenv("USER")
	if user == "" {
		user = getenv("USERNAME")
	}
	if user == "" {
		return Resolved{}, false
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	r := fromKey(KindHuman+kindSep+user+"@"+host, user, StepOSUser)
	return r, true
}

// machineID hashes the Linux machine id into auto:<12 hex>.
func machineID() (Resolved, bool) {
	for _, p := range machineIDFiles {
		b, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			continue
		}
		if s := strings.TrimSpace(string(b)); s != "" {
			sum := sha256.Sum256([]byte(s))
			h := hex.EncodeToString(sum[:])[:saltHexLen]
			return Resolved{StableKey: KindAuto + kindSep + h, DisplayName: h, Kind: KindAuto, Step: StepMachine}, true
		}
	}
	return Resolved{}, false
}

// persistedSalt reads (or creates once) <home>/identity.
func persistedSalt(home string) (Resolved, bool) {
	path := filepath.Join(home, SaltFile)
	s := ""
	if b, err := os.ReadFile(filepath.Clean(path)); err == nil {
		s = strings.TrimSpace(string(b))
	}
	if len(s) < saltHexLen {
		if err := os.MkdirAll(home, dirMode); err != nil {
			return Resolved{}, false
		}
		s = randHex(saltBytes)
		if err := os.WriteFile(path, []byte(s+"\n"), fileMode); err != nil {
			return Resolved{}, false
		}
	}
	h := s[:saltHexLen]
	return Resolved{StableKey: KindAuto + kindSep + h, DisplayName: h, Kind: KindAuto, Step: StepSalt}, true
}

// randHex returns 2*n hex characters of crypto randomness.
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand.Read never fails on supported platforms
	return hex.EncodeToString(b)
}
