package wire

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// secret_rules field names (schema.Builtin).
const (
	ruleFieldID       = "rule_id"
	ruleFieldRegex    = "regex"
	ruleFieldKeywords = "keywords"
	ruleFieldEntropy  = "entropy"
	ruleFieldState    = "state"
)

// liveScanner is the App's scanner: the gitleaks defaults plus the live
// secret_rules rows of every attached store (spec 4.3), rebuilt when those
// rows change. Why: rules are settings rows (SETTINGS ARE DATA; D2/D9:
// users fully control secret rules), and a long-lived App (`lore api`)
// must apply a rule added while it runs to the next write; a scanner
// built once at open would keep scanning with the old set. Refresh (called
// by the engine before each write's scan, engine.RefreshingScanner) reads
// the rule rows and rebuilds only when their ids or HLCs differ, so an
// unchanged set costs one backend read and no regex compile.
//
// Rows are read from the backends, not the engine: the engine cannot
// exist before its scanner. A malformed rule row fails the open or the
// write (E_CONFIG_INVALID, naming the rule id), never silently drops a
// rule. Safe for concurrent use.
//
// Rows of every store are merged into one row per rule_id (resolveRules,
// OV45): personal off/changed rows never reach the shared scanner (the
// engine's rules.IsOff(id, own) applies a personal off to the person's
// own captures, BATCH 8), and when team stores disagree "on" wins in the
// scanner (the engine still applies an off to writes).
type liveScanner struct {
	backends map[store.Source]store.Backend
	// folders is each attached store's folder (the settings level, O1);
	// a store without one counts as a store root. own makes this the
	// person's own scanner (OutputScanner): personal offs apply too.
	folders map[store.Source]string
	own     bool
	// repoDir is the repo top whose .gitleaks.toml contributes allow
	// patterns and stopwords (D8) while secrets.use_repo_gitleaks is on;
	// "" reads no file. policy reads the effective policy (set once the
	// engine exists, usePolicy); nil means the key's default (true).
	repoDir string
	policy  func() (policy.Set, error)
	mu      sync.Mutex
	key     string // the rule rows' ids and HLCs the current scanner was built from
	cur     *gitleaks.Scanner
	// sessions reads the raw tool session ids known on this machine
	// (OV11, AGENT_IDENTITY 9 / OQ14); nil: no session-id rule. ids is
	// the set read at the last Refresh.
	sessions ToolSessionSource
	ids      []string
}

// ToolSessionSource is an optional capability of the repo audit sink
// (Options.Audit): the raw tool session ids this machine knows (the old
// binary's lore.db sessions table, machine-local). The scanner refuses
// any of them in row text at write time (RuleSessionID), so a session id
// never reaches a row file and git, not only at commit and push.
type ToolSessionSource interface {
	ToolSessionIDs(ctx context.Context) ([]string, error)
}

// RuleSessionID is scan.RuleSessionID, kept here for wire's callers. It
// goes through secrets.on_detect like any secret finding (DECISION
// P2-CTX-2); the engine skips it on schema LoreRecord fields (BUG-T7).
const RuleSessionID = scan.RuleSessionID

// minSessionIDLen is the shortest session id the rule looks for: a
// shorter one would match ordinary text (same floor as the leak check).
const minSessionIDLen = 8

// withToolSessions makes the scanner refuse known tool session ids when
// src (the repo audit sink) offers them; any other value adds nothing.
func withToolSessions(src engine.AuditSink) scannerOption {
	return func(s *liveScanner) {
		if ts, ok := src.(ToolSessionSource); ok {
			s.sessions = ts
		}
	}
}

var _ engine.RefreshingScanner = (*liveScanner)(nil)

// scannerOption configures newScanner.
type scannerOption func(*liveScanner)

// withRepoGitleaks makes the scanner read <dir>/.gitleaks.toml (D8).
func withRepoGitleaks(dir string) scannerOption {
	return func(s *liveScanner) { s.repoDir = dir }
}

// withFolders gives the scanner each store's folder, so rows of two team
// stores resolve nearest-first (D9).
func withFolders(f map[store.Source]string) scannerOption {
	return func(s *liveScanner) { s.folders = f }
}

// withOwnRules makes this the person's own scanner: a personal off or
// changed row applies (BATCH 8: own captures only).
func withOwnRules() scannerOption {
	return func(s *liveScanner) { s.own = true }
}

// repoGitleaksFile is the repo file gitleaks itself reads (D8).
const repoGitleaksFile = ".gitleaks.toml"

// newScanner builds the live scanner over backends and its first rule set.
// It is the one scanner constructor of the core (D4): writes, hooks and
// lore check leaks all scan through it.
func newScanner(ctx context.Context, backends map[store.Source]store.Backend, opts ...scannerOption) (*liveScanner, error) {
	s := &liveScanner{backends: backends}
	for _, o := range opts {
		o(s)
	}
	if err := s.Refresh(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Refresh implements engine.RefreshingScanner.
func (s *liveScanner) Refresh(ctx context.Context) error {
	if err := s.refreshSessions(ctx); err != nil {
		return err
	}
	entries, key, err := ruleRows(ctx, s.backends, s.folders)
	if err != nil {
		return err
	}
	allow, allowKey, err := s.repoAllow()
	if err != nil {
		return err
	}
	noProse, err := s.proseFilterOff()
	if err != nil {
		return err
	}
	key += "\n#repo-gitleaks:" + allowKey + "\n#prose-filter-off:" + strconv.FormatBool(noProse)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur != nil && key == s.key {
		return nil
	}
	base, err := gitleaks.Defaults()
	if err != nil {
		return err
	}
	rules, err := gitleaks.Rules(base, resolveRules(entries, base, s.own))
	if err != nil {
		return err
	}
	next, err := gitleaks.NewWith(rules, gitleaks.Options{RepoAllow: allow, NoProseFilter: noProse})
	if err != nil {
		return err
	}
	s.cur, s.key = next, key
	return nil
}

// usePolicy late-binds the policy reader (the engine is built after its
// scanner); the next Refresh honours secrets.use_repo_gitleaks.
func (s *liveScanner) usePolicy(fn func() (policy.Set, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy = fn
}

// proseFilterOff reports whether policy secrets.password_pair_prose_filter
// is off (SECRETS 3.4: off keeps every password_pair match, stricter); no
// policy reader yet, or the key unset, means the default (filter on).
func (s *liveScanner) proseFilterOff() (bool, error) {
	s.mu.Lock()
	pol := s.policy
	s.mu.Unlock()
	if pol == nil {
		return false, nil
	}
	set, err := pol()
	if err != nil {
		return false, err
	}
	if e, ok := set.Get(policy.KeyPasswordPairProseFilter); ok {
		if b, isBool := e.Value.(value.Bool); isBool && !b.Bool() {
			return true, nil
		}
	}
	return false, nil
}

// repoAllow reads the repo .gitleaks.toml allow when the policy allows it
// (D8, default on), with a key that changes with the file's content or
// the switch. A missing file is no allow; a bad file fails closed
// (E_CONFIG_INVALID).
func (s *liveScanner) repoAllow() (gitleaks.RepoAllow, string, error) {
	s.mu.Lock()
	dir, pol := s.repoDir, s.policy
	s.mu.Unlock()
	if dir == "" {
		return gitleaks.RepoAllow{}, "", nil
	}
	if pol != nil {
		set, err := pol()
		if err != nil {
			return gitleaks.RepoAllow{}, "", err
		}
		if e, ok := set.Get(policy.KeyUseRepoGitleaks); ok {
			if b, isBool := e.Value.(value.Bool); isBool && !b.Bool() {
				return gitleaks.RepoAllow{}, "off", nil
			}
		}
	}
	data, err := os.ReadFile(filepath.Clean(filepath.Join(dir, repoGitleaksFile)))
	if errors.Is(err, fs.ErrNotExist) {
		return gitleaks.RepoAllow{}, "", nil
	}
	if err != nil {
		return gitleaks.RepoAllow{}, "", errs.Wrap(errs.ConfigInvalid, "wire: read "+repoGitleaksFile, err)
	}
	allow, err := gitleaks.ParseRepoGitleaks(data)
	if err != nil {
		return gitleaks.RepoAllow{}, "", err
	}
	sum := sha256.Sum256(data)
	return allow, hex.EncodeToString(sum[:]), nil
}

// scanner is the current rule set's scanner.
func (s *liveScanner) scanner() *gitleaks.Scanner {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// Scan implements scan.Scanner.
func (s *liveScanner) Scan(field, text string) []scan.Finding {
	return s.ScanReport(field, text).Findings
}

// ScanReport implements scan.Reporter (the engine surfaces a degraded scan).
func (s *liveScanner) ScanReport(field, text string) scan.Report {
	rep := s.scanner().ScanReport(field, text)
	rep.Findings = append(rep.Findings, s.sessionHits(field, text)...)
	return rep
}

// refreshSessions re-reads the known tool session ids (a new session may
// have started since the last write of a long-lived App). A read error
// fails the write closed.
func (s *liveScanner) refreshSessions(ctx context.Context) error {
	if s.sessions == nil {
		return nil
	}
	got, err := s.sessions.ToolSessionIDs(ctx)
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: read the known tool session ids", err)
	}
	var keep []string
	for _, id := range got {
		if len(id) >= minSessionIDLen {
			keep = append(keep, id)
		}
	}
	s.mu.Lock()
	s.ids = keep
	s.mu.Unlock()
	return nil
}

// sessionHits finds every known tool session id in text, masked like any
// secret finding (the id itself is never printed).
func (s *liveScanner) sessionHits(field, text string) []scan.Finding {
	s.mu.Lock()
	ids := s.ids
	s.mu.Unlock()
	var out []scan.Finding
	for _, id := range ids {
		for from := 0; ; {
			i := strings.Index(text[from:], id)
			if i < 0 {
				break
			}
			off := from + i
			out = append(out, scan.Finding{RuleID: RuleSessionID, Field: field, Line: strings.Count(text[:off], "\n") + 1,
				Offset: off, Length: len(id), Preview: scan.Mask(id), MatchHash: scan.HashMatch(id)})
			from = off + len(id)
		}
	}
	return out
}

// ruleEntry is one live secret_rules row and the level of its store.
type ruleEntry struct {
	store store.Source
	level policy.Level
	row   gitleaks.RuleRow
}

// ruleRows reads the live secret_rules rows of every backend, with a key
// naming each row's store, id and HLC (sorted), which changes whenever a
// rule is added, edited or deleted.
func ruleRows(ctx context.Context, backends map[store.Source]store.Backend, folders map[store.Source]string) ([]ruleEntry, string, error) {
	var out []ruleEntry
	var keys []string
	for name, b := range backends {
		got, _, readErr := b.Read(ctx, "")
		if readErr != nil {
			return nil, "", readErr
		}
		level := ruleLevel(name, folders[name])
		for _, sr := range got {
			if sr.Table != row.Table(schema.TableSecretRules) {
				continue
			}
			keys = append(keys, string(name)+"/"+sr.ID.String()+"@"+sr.HLC.String())
			if sr.Deleted {
				continue
			}
			r, decErr := sr.Decode()
			if decErr != nil {
				return nil, "", decErr
			}
			rr, ruleErr := ruleRow(r)
			if ruleErr != nil {
				return nil, "", ruleErr
			}
			out = append(out, ruleEntry{store: name, level: level, row: rr})
		}
	}
	slices.Sort(keys)
	return out, strings.Join(keys, "\n"), nil
}

// ruleLevel is the settings level of a store's rows (O1): the repo, the
// personal store, else levelOf by the store's folder.
func ruleLevel(name store.Source, folder string) policy.Level {
	if name == store.SourceRepo {
		return policy.LevelRepo
	}
	return levelOf(attach.Opened{Attached: engine.Attached{Name: name}, Folder: folder})
}

// resolveRules merges the rows of every store into at most one row per
// rule_id, the input gitleaks.Rules accepts (it refuses two rows with one
// id). Per rule_id:
//
//	own && a personal row is off        → off      (BATCH 8, own captures)
//	a team row is not off               → the nearest such team row
//	                                       ("on" wins in the scanner, OV45;
//	                                       nearest change wins, D9)
//	every team row is off               → off
//	personal rows only                  → own: the nearest personal row;
//	                                      shared: only a rule it ADDS (no
//	                                      default has the id, has a regex,
//	                                      not off), never an off or change
//
// The shared scanner (writes, hooks, export, lore check leaks) therefore
// never drops a rule for a personal off: the engine's rules.IsOff(id,
// own) applies that off to the person's own writes only.
func resolveRules(entries []ruleEntry, base []scan.Rule, own bool) []gitleaks.RuleRow {
	known := map[string]bool{}
	for _, b := range base {
		known[b.ID] = true
	}
	order := policy.Levels() // nearest first
	slices.SortStableFunc(entries, func(a, b ruleEntry) int {
		if c := strings.Compare(a.row.RuleID, b.row.RuleID); c != 0 {
			return c
		}
		if c := slices.Index(order, a.level) - slices.Index(order, b.level); c != 0 {
			return c
		}
		return strings.Compare(string(a.store), string(b.store)) // same level: stable by name
	})
	var out []gitleaks.RuleRow
	for start := 0; start < len(entries); {
		end := start + 1
		for end < len(entries) && entries[end].row.RuleID == entries[start].row.RuleID {
			end++
		}
		if r, ok := resolveOne(entries[start:end], known, own); ok {
			out = append(out, r)
		}
		start = end
	}
	return out
}

// resolveOne picks the row of one rule_id; group is sorted nearest first.
func resolveOne(group []ruleEntry, known map[string]bool, own bool) (gitleaks.RuleRow, bool) {
	var teamOff, firstPersonal *ruleEntry
	for i := range group {
		e := &group[i]
		personal := e.level == policy.LevelPersonal
		switch {
		case personal && own && e.row.State == schema.SecretRuleOff:
			return e.row, true
		case personal:
			if firstPersonal == nil {
				firstPersonal = e
			}
		case e.row.State != schema.SecretRuleOff:
			return e.row, true // nearest team row that keeps the rule: on wins
		case teamOff == nil:
			teamOff = e
		}
	}
	switch {
	case teamOff != nil:
		return teamOff.row, true
	case firstPersonal == nil:
		return gitleaks.RuleRow{}, false
	case own:
		return firstPersonal.row, true
	}
	p := firstPersonal.row
	if known[p.RuleID] || p.State == schema.SecretRuleOff || p.Regex == "" {
		return gitleaks.RuleRow{}, false // a personal off/change never reaches the shared scanner
	}
	return p, true
}

// ruleRow maps one secret_rules row to the scanner's input.
func ruleRow(r row.Row) (gitleaks.RuleRow, error) {
	rr := gitleaks.RuleRow{
		RuleID: textField(r, ruleFieldID), Regex: textField(r, ruleFieldRegex), State: textField(r, ruleFieldState),
	}
	// any form: a List, or a JSON array as a row file may decode
	rr.Keywords = value.ItemStrings(r.Fields[ruleFieldKeywords])
	if n, ok := r.Fields[ruleFieldEntropy].(value.Number); ok {
		f, err := n.Float64()
		if err != nil {
			return gitleaks.RuleRow{}, errs.Wrap(errs.ConfigInvalid, "wire: secret rule "+rr.RuleID+": bad entropy", err)
		}
		rr.Entropy = f
	}
	return rr, nil
}

// textField returns a field's text ("" when absent).
func textField(r row.Row, name string) string {
	if v, ok := r.Fields[name]; ok && v.Kind() == value.KindText {
		return v.String()
	}
	return ""
}

// bindEngine makes the scanner read secrets.use_repo_gitleaks from the
// repo store's effective policy (the level the repo .gitleaks.toml
// belongs to).
func (s *liveScanner) bindEngine(eng *engine.Engine) {
	s.usePolicy(func() (policy.Set, error) { return eng.Policy(store.SourceRepo) })
}
