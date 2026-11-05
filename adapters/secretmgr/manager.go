package secretmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Locator names one entry: the path part of manager://path, the "#…"
// fragment (the key inside a vault or JSON secret) and the manager's own
// options (region, project). It never holds a value.
type Locator struct {
	Path     string
	Fragment string
	Options  []Option
}

// Option is one manager option (name=value) of a reference.
type Option struct {
	Name  string
	Value string
}

// Opt returns option name's value, "" when absent.
func (l Locator) Opt(name string) string {
	for _, o := range l.Options {
		if o.Name == name {
			return o.Value
		}
	}
	return ""
}

// Manager is one place secrets are kept (10.2). There is no listing method
// on purpose: lore never asks a vault for its entry names (SQ6 dropped).
type Manager interface {
	Name() string                        // the URI scheme ("gopass", "aws-secrets")
	ValidateLocator(l Locator) error     // syntax only; no I/O
	Available(ctx context.Context) error // CLI present
	Reveal(ctx context.Context, l Locator) (Secret, error)
}

// Manager scheme names (BATCH 9, SQ14).
const (
	Gopass    = "gopass"
	OnePass   = "op"
	Bitwarden = "bitwarden"
	Vault     = "vault"
	AWS       = "aws-secrets"
	GCP       = "gcp-secrets"
	Azure     = "azure-keyvault"
	Env       = "env"
)

// Timeouts (section 6: secrets.manager_timeout default 5 s, max 30 s).
const (
	DefaultTimeout = 5 * time.Second
	MaxTimeout     = 30 * time.Second
	// waitDelay bounds how long a killed CLI's pipes may stay open.
	waitDelay = time.Second
)

// ErrMissing marks a reveal whose manager answered "no such entry" (verify:
// missing, not "not checked").
var ErrMissing = errors.New("secretmgr: no such entry")

// Options configures the built-in managers. Zero values are the defaults.
type Options struct {
	// Timeout per call (DefaultTimeout when 0; capped at MaxTimeout).
	Timeout time.Duration
	// Getenv reads the env manager's variables (os.Getenv when nil).
	Getenv func(string) string
	// LookPath finds a CLI (exec.LookPath when nil; it honours PATHEXT).
	LookPath func(string) (string, error)
}

// Set is the managers by scheme.
type Set map[string]Manager

// Get returns the manager of scheme.
func (s Set) Get(scheme string) (Manager, bool) {
	m, ok := s[scheme]
	return m, ok
}

// Names lists the schemes, sorted.
func (s Set) Names() []string {
	out := make([]string, 0, len(s))
	for n := range s {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// Builtin returns every built-in manager configured by o.
func Builtin(o Options) Set {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	o.Timeout = min(o.Timeout, MaxTimeout)
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	r := runner{o: o}
	out := Set{}
	for _, m := range []Manager{
		cliManager{name: Gopass, bin: "gopass", r: r, args: gopassArgs},
		cliManager{name: OnePass, bin: "op", r: r, args: opArgs},
		bitwarden{r: r},
		cliManager{name: Vault, bin: "vault", r: r, args: vaultArgs, needFragment: true},
		cliManager{name: AWS, bin: "aws", r: r, args: awsArgs, jsonKey: true},
		cliManager{name: GCP, bin: "gcloud", r: r, args: gcpArgs, jsonKey: true},
		cliManager{name: Azure, bin: "az", r: r, args: azureArgs, twoParts: true},
		envManager{get: o.Getenv},
	} {
		out[m.Name()] = m
	}
	return out
}

// unavailable is E_SECRET_UNAVAILABLE with the manager's one-line reason.
// It never carries stderr or the value.
func unavailable(manager, reason string) error {
	return errs.New(errs.VaultEntryUnavailable, manager+": "+reason).
		WithHint("check that " + manager + " is installed, unlocked and logged in on this machine")
}

// missing is E_SECRET_UNAVAILABLE for an entry the manager does not have.
func missing(manager, path string) error {
	return errs.Wrap(errs.VaultEntryUnavailable, manager+" has no entry "+path+" on this machine", ErrMissing).
		WithHint("the reference names its holder; ask them, or point it elsewhere with lore secrets move / rename-entry")
}

// runner runs one manager CLI with the timeout.
type runner struct{ o Options }

// notFoundWords are the phrases a manager's stderr uses for a missing entry.
// stderr is read only for this classification and then dropped (section 6).
var notFoundWords = []string{"not found", "no such", "does not exist", "isn't an item", "resourcenotfound", "not_found", "secretnotfound", "is not in the password store", "entry is not"}

// run executes bin args and returns stdout. stdin is closed, stderr is
// classified and dropped; on timeout the process is killed.
func (r runner) run(ctx context.Context, manager, bin, path string, args []string) ([]byte, error) {
	exe, err := r.o.LookPath(bin)
	if err != nil {
		return nil, unavailable(manager, bin+" not found on PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, r.o.Timeout)
	defer cancel()
	// Built directly (as adapters/gitcommit does): exe is the absolute path
	// LookPath resolved for a fixed program name, every argument is its own
	// argv entry (no shell parses it), and runKillable kills it on timeout.
	cmd := &exec.Cmd{Path: exe, Args: append([]string{exe}, args...), WaitDelay: waitDelay}
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err = runKillable(ctx, cmd)
	if ctx.Err() != nil {
		return nil, unavailable(manager, "timed out after "+r.o.Timeout.String())
	}
	if err != nil {
		low := strings.ToLower(stderr.String())
		for _, w := range notFoundWords {
			if strings.Contains(low, w) {
				return nil, missing(manager, path)
			}
		}
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return nil, unavailable(manager, bin+" failed (exit "+strconv.Itoa(code)+"; its message is not shown)")
	}
	return trimNewline(out.Bytes()), nil
}

// runKillable starts cmd and kills it when ctx ends first.
func runKillable(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = cmd.Process.Kill() // the process may have just exited; Wait reports it
		<-done
		return ctx.Err()
	}
}

// trimNewline drops one trailing line break (what most CLIs add).
func trimNewline(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte("\n"))
	return bytes.TrimSuffix(b, []byte("\r"))
}

// cliManager is a manager that shells out to one vendor CLI.
type cliManager struct {
	name, bin    string
	r            runner
	args         func(Locator) []string
	needFragment bool // vault: the key is the fragment
	jsonKey      bool // the fragment picks a key of a JSON secret
	twoParts     bool // azure: <vault>/<name>
}

// Name implements Manager.
func (m cliManager) Name() string { return m.name }

// ValidateLocator implements Manager (syntax only).
func (m cliManager) ValidateLocator(l Locator) error {
	p := strings.Trim(l.Path, "/")
	switch {
	case p == "":
		return errs.New(errs.RefSyntax, m.name+": empty entry name")
	case m.needFragment && l.Fragment == "":
		return errs.New(errs.RefSyntax, m.name+": name the key as #key (vault://<mount>/<path>#<key>)")
	case m.twoParts && strings.Count(p, "/") != 1:
		return errs.New(errs.RefSyntax, m.name+": want <vault>/<secret name>")
	case strings.HasPrefix(p, "-"):
		// a locator is an argument: never let it read as a flag
		return errs.New(errs.RefSyntax, m.name+": an entry name may not start with -")
	}
	return nil
}

// Available implements Manager: the CLI is on PATH.
func (m cliManager) Available(_ context.Context) error {
	if _, err := m.r.o.LookPath(m.bin); err != nil {
		return unavailable(m.name, m.bin+" not found on PATH")
	}
	return nil
}

// Reveal implements Manager: one named entry into a Secret.
func (m cliManager) Reveal(ctx context.Context, l Locator) (Secret, error) {
	if err := m.ValidateLocator(l); err != nil {
		return Secret{}, err
	}
	out, err := m.r.run(ctx, m.name, m.bin, l.Path, m.args(l))
	if err != nil {
		return Secret{}, err
	}
	if m.jsonKey && l.Fragment != "" {
		return pickKey(m.name, l, out)
	}
	return NewSecret(out), nil
}

// pickKey reads key l.Fragment of a JSON secret (aws, gcp: "the JSON key is
// picked inside lore").
func pickKey(manager string, l Locator, out []byte) (Secret, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		return Secret{}, unavailable(manager, "the secret is not a JSON object, so #"+l.Fragment+" cannot be read")
	}
	raw, ok := obj[l.Fragment]
	if !ok {
		return Secret{}, missing(manager, l.Path+"#"+l.Fragment)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return NewSecret(raw), nil // a number or bool: its JSON text
	}
	return NewSecret([]byte(s)), nil
}

func gopassArgs(l Locator) []string { return []string{"show", "-o", strings.Trim(l.Path, "/")} }

func opArgs(l Locator) []string { return []string{"read", "op://" + strings.Trim(l.Path, "/")} }

func vaultArgs(l Locator) []string {
	return []string{"kv", "get", "-field=" + l.Fragment, strings.Trim(l.Path, "/")}
}

func awsArgs(l Locator) []string {
	a := []string{"secretsmanager", "get-secret-value", "--secret-id", strings.Trim(l.Path, "/"), "--query", "SecretString", "--output", "text"}
	if r := l.Opt("region"); r != "" {
		a = append(a, "--region", r)
	}
	return a
}

func gcpArgs(l Locator) []string {
	a := []string{"secrets", "versions", "access", "latest", "--secret=" + strings.Trim(l.Path, "/")}
	if p := l.Opt("project"); p != "" {
		a = append(a, "--project="+p)
	}
	return a
}

func azureArgs(l Locator) []string {
	v, n, _ := strings.Cut(strings.Trim(l.Path, "/"), "/")
	return []string{"keyvault", "secret", "show", "--vault-name", v, "--name", n, "--query", "value", "-o", "tsv"}
}

// bitwarden: bitwarden://<item>[/<field>]; password, username, totp, notes
// and uri are `bw get <field>`, any other field is a custom field of
// `bw get item`.
type bitwarden struct{ r runner }

// bwFields are the fields `bw get` reads directly.
var bwFields = []string{"password", "username", "totp", "notes", "uri"}

const bwBin = "bw"

// Name implements Manager.
func (bitwarden) Name() string { return Bitwarden }

// ValidateLocator implements Manager (syntax only).
func (b bitwarden) ValidateLocator(l Locator) error {
	return cliManager{name: Bitwarden}.ValidateLocator(l)
}

// Available implements Manager: bw is on PATH.
func (b bitwarden) Available(ctx context.Context) error {
	return cliManager{name: Bitwarden, bin: bwBin, r: b.r}.Available(ctx)
}

// Reveal implements Manager: one item field into a Secret.
func (b bitwarden) Reveal(ctx context.Context, l Locator) (Secret, error) {
	if err := b.ValidateLocator(l); err != nil {
		return Secret{}, err
	}
	p := strings.Trim(l.Path, "/")
	item, field, hasField := strings.Cut(p, "/")
	if !hasField {
		field = "password"
	}
	if slices.Contains(bwFields, field) {
		out, err := b.r.run(ctx, Bitwarden, bwBin, p, []string{"get", field, item})
		if err != nil {
			return Secret{}, err
		}
		return NewSecret(out), nil
	}
	out, err := b.r.run(ctx, Bitwarden, bwBin, p, []string{"get", "item", item})
	if err != nil {
		return Secret{}, err
	}
	var it struct {
		Fields []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(out, &it); err != nil {
		return Secret{}, unavailable(Bitwarden, "bw get item did not answer JSON")
	}
	for _, f := range it.Fields {
		if f.Name == field {
			return NewSecret([]byte(f.Value)), nil
		}
	}
	return Secret{}, missing(Bitwarden, p)
}

// envManager reads one variable of lore's own environment.
type envManager struct{ get func(string) string }

// Name implements Manager.
func (envManager) Name() string { return Env }

// ValidateLocator implements Manager: a variable name.
func (envManager) ValidateLocator(l Locator) error {
	p := strings.Trim(l.Path, "/")
	if p == "" || strings.ContainsAny(p, "/= ") {
		return errs.New(errs.RefSyntax, "env: want env://<VARIABLE_NAME>")
	}
	return nil
}

// Available implements Manager: the environment is always there.
func (envManager) Available(context.Context) error { return nil }

// Reveal implements Manager: the one named variable.
func (m envManager) Reveal(_ context.Context, l Locator) (Secret, error) {
	if err := m.ValidateLocator(l); err != nil {
		return Secret{}, err
	}
	p := strings.Trim(l.Path, "/")
	v := m.get(p)
	if v == "" {
		return Secret{}, missing(Env, p)
	}
	return NewSecret([]byte(v)), nil
}
