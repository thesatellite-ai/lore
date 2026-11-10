package wire

// setupaliases.go — setup.aliases' Prober side (opssetup.GitHubLister,
// ONB 4.3): the ssh config's github.com aliases, each alias's key file,
// .pub fingerprint and the user ssh -T greets, gh's logins from hosts.yml,
// and with Keys every private key tested alone. Every ssh -T runs in
// parallel through the App's one limiter with aliasProbeTimeout; the
// answer is kept for opsdoctor.CacheTTL (memory only), so the page's step
// and the github step's validation messages do not probe twice in a row.
//
//	GitHubAliases ─▶ aliasCache.get (hit) ─▶ answer
//	              └▶ ReadSSHConfig ─▶ per alias (‖): IsFile / KeyNearby / KeyFingerprint(.pub) / SSHAuthWithin ─▶ put

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// aliasProbeTimeout bounds one ssh -T of an alias or key (the run, not
// the wait for a limiter slot). GitHub greets in about a second; an alias
// silent for 3 s is nearly always a dead HostName or a blocked port, and
// waiting longer only held the GitHub step and Doctor (5 s before
// 2026-11-06: github.key_matches took 6 s on one hanging alias).
const aliasProbeTimeout = 3 * time.Second

// keyMatchNoFixWait is the longest github.key_matches waits, from its
// start, for the other aliases when none known so far logs in as the
// account (a cold cache): one probe's full time limit plus a slot wait,
// so answers that arrive within the probe timeout are used rather than
// reported missing. When a fix is already known it does not wait at all
// (a cold step check stays near one ssh round trip, 2026-11-06); the
// aliases still probing are named and a re-run fills them in.
const keyMatchNoFixWait = aliasProbeTimeout + time.Second

// aliasTimeoutTTL is how long an alias or key that did not answer is
// remembered as silent. A Doctor or step re-run then answers at once
// ("did not answer in 3 s") instead of waiting on the dead alias again,
// while one that comes back is asked again soon (answers that arrived
// are kept for the full opsdoctor.CacheTTL).
const aliasTimeoutTTL = 20 * time.Second

// aliasBatchTimeout bounds a whole table, slot waits included (keys mode
// may queue many probes behind the limiter).
const aliasBatchTimeout = 60 * time.Second

var _ opssetup.GitHubLister = setupProber{}

// GitHubAliases implements opssetup.GitHubLister.
func (sp setupProber) GitHubAliases(ctx context.Context, in opssetup.AliasesIn) (opssetup.AliasesOut, error) {
	var cache *aliasCache
	if sp.a != nil {
		cache = sp.a.aliasCache
	}
	// One probe at a time per mode: the page's table and the github
	// step's check open together; the second waits and reads the cache.
	unlock := cache.lock(in.Keys)
	defer unlock()
	if !in.Fresh {
		if out, ok := cache.get(in.Keys); ok {
			return out, nil
		}
	}
	out, err := sp.githubAliases(ctx, in.Keys, in.Fresh)
	if err == nil && ctx.Err() == nil {
		cache.put(in.Keys, out)
	}
	return out, err
}

// githubAliases probes the table now (fresh: no per-alias answer reused).
func (sp setupProber) githubAliases(ctx context.Context, keys, fresh bool) (opssetup.AliasesOut, error) {
	ctx, cancel := context.WithTimeout(ctx, aliasBatchTimeout)
	defer cancel()
	cfg, err := sp.p.ReadSSHConfig(ctx)
	if err != nil {
		return opssetup.AliasesOut{}, err
	}
	aliases := githubAliasList(cfg)
	out := opssetup.AliasesOut{Aliases: make([]opssetup.GitHubAlias, len(aliases)), GH: []opssetup.GHLogin{}}
	var files []string
	if keys {
		var extra []string
		for _, a := range aliases {
			extra = append(extra, a.IdentityFiles...)
		}
		files = sp.p.PrivateKeyFiles(extra)
	}
	// A background discovery shows "Testing ssh aliases and keys (3 of 9)".
	var (
		progMu sync.Mutex
		tested int
	)
	total := len(aliases) + len(files)
	report := func(step int) {
		progMu.Lock()
		defer progMu.Unlock()
		tested += step
		opssetup.ReportDiscoverStep(ctx, opssetup.DiscoverStep{ID: opssetup.StepSSH, Label: "Testing ssh aliases and keys", Done: tested, Total: total, Finished: tested == total})
	}
	report(0)
	var wg sync.WaitGroup
	for i, a := range aliases {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out.Aliases[i] = sp.aliasRow(ctx, a, fresh)
			report(1)
		}()
	}
	if keys {
		out.Keys = make([]opssetup.GitHubKey, len(files))
		for i, f := range files {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out.Keys[i] = sp.keyRow(ctx, f)
				report(1)
			}()
		}
	}
	for _, a := range sp.p.GHLogins().Accounts {
		out.GH = append(out.GH, opssetup.GHLogin{User: a.User, Active: a.Active})
	}
	wg.Wait()
	return out, nil
}

// githubAliasList is the config's github.com aliases plus github.com
// itself (its effective settings) when no Host block names it.
func githubAliasList(cfg probe.SSHConfig) []probe.SSHAlias {
	aliases := cfg.GitHubAliases()
	if !slices.ContainsFunc(aliases, func(a probe.SSHAlias) bool { return a.Alias == probe.GitHubHost }) {
		eff := cfg.Effective(probe.GitHubHost)
		eff.Alias = probe.GitHubHost
		aliases = append(aliases, eff)
	}
	return aliases
}

// aliasRow is one alias: its key file facts and who ssh -T greets.
// fresh skips the per-alias answers kept by earlier probes.
func (sp setupProber) aliasRow(ctx context.Context, a probe.SSHAlias, fresh bool) opssetup.GitHubAlias {
	row := sp.aliasFacts(ctx, a)
	r := sp.aliasProbe(ctx, a.Alias, fresh)
	row.LogsInAs, row.Status, row.Detail = r.User, string(r.Class), probe.Sanitize(r.Detail)
	return row
}

var _ opssetup.KnownAliasLister = setupProber{}

// GitHubAliasesKnown implements opssetup.KnownAliasLister: the full
// table when one is kept, else every alias's file facts at once with the
// ssh answers that arrive within q.Wait (all aliases probed in parallel,
// the same shared probes key_matches uses); the rest are pending, with
// status opssetup.StatusChecking. Never waits on a hung alias past q.Wait.
func (sp setupProber) GitHubAliasesKnown(ctx context.Context, q opssetup.AliasQuery) (opssetup.AliasesOut, []string, error) {
	var cache *aliasCache
	if sp.a != nil {
		cache = sp.a.aliasCache
	}
	if out, ok := cache.get(false); ok {
		return out, nil, nil
	}
	cfg, err := sp.p.ReadSSHConfig(ctx)
	if err != nil {
		return opssetup.AliasesOut{}, nil, err
	}
	aliases := githubAliasList(cfg)
	// File facts first: their ssh-keygen calls take limiter slots, which
	// the ssh probes below would otherwise hold for a second or more.
	out := opssetup.AliasesOut{Aliases: make([]opssetup.GitHubAlias, 0, len(aliases)), GH: []opssetup.GHLogin{}}
	for _, a := range aliases {
		out.Aliases = append(out.Aliases, sp.aliasFacts(ctx, a))
	}
	sweep := sp.newSweep(ctx, cfg, probe.GitHubHost)
	if q.Wait > 0 {
		sweep.start(sweep.order...)
	} else {
		// No wait: answers already kept only. Nothing starts, so the
		// step's own checks, which run next, get the probe slots.
		for _, name := range sweep.order {
			var cache *aliasCache
			if sp.a != nil {
				cache = sp.a.aliasCache
			}
			if r, ok := sp.cachedAlias(cache, name); ok {
				sweep.set(name, r)
			}
		}
	}
	got, pending := sweep.until(ctx, time.Now().Add(q.Wait), func(got map[string]probe.SSHAuthResult) bool {
		if q.User == "" {
			return false
		}
		for _, r := range got {
			if strings.EqualFold(r.User, q.User) {
				return true
			}
		}
		return false
	})
	for i := range out.Aliases {
		row := &out.Aliases[i]
		if r, ok := got[row.Alias]; ok && (r.Class != probe.ClassTimeout || realTimeout(r)) {
			row.LogsInAs, row.Status, row.Detail = r.User, string(r.Class), probe.Sanitize(r.Detail)
		} else {
			row.Status = opssetup.StatusChecking
			if !slices.Contains(pending, row.Alias) {
				pending = append(slices.Clip(pending), row.Alias)
			}
		}
	}
	for _, a := range sp.p.GHLogins().Accounts {
		out.GH = append(out.GH, opssetup.GHLogin{User: a.User, Active: a.Active})
	}
	return out, pending, nil
}

// aliasFacts is one alias's file facts (no network): its key file, a
// key nearby when it is missing, the .pub and fingerprint.
func (sp setupProber) aliasFacts(ctx context.Context, a probe.SSHAlias) opssetup.GitHubAlias {
	row := opssetup.GitHubAlias{Alias: a.Alias, IdentitiesOnly: a.IdentitiesOnly}
	if len(a.IdentityFiles) > 0 {
		row.IdentityFile = a.IdentityFiles[0]
		if !sp.p.IsFile(row.IdentityFile) {
			row.KeyMissing = true
			if n := sp.p.KeyNearby(row.IdentityFile); n != "" {
				row.KeyNearby = sp.tildePath(n)
			}
		} else {
			row.PubKey, row.Fingerprint, row.NoPub = sp.pubOf(ctx, row.IdentityFile)
		}
	}
	return row
}

// keyRow is one private key tested alone against github.com.
func (sp setupProber) keyRow(ctx context.Context, path string) opssetup.GitHubKey {
	row := opssetup.GitHubKey{Path: sp.tildePath(path)}
	row.PubKey, row.Fingerprint, _ = sp.pubOf(ctx, path)
	r := sp.keyLogin(ctx, path)
	row.LogsInAs, row.Status, row.Detail = r.User, string(r.Class), probe.Sanitize(r.Detail)
	return row
}

// pubOf is key + .pub (~ form) when the .pub exists (noPub when not), and
// the key's fingerprint either way: from the .pub, else ssh-keygen -lf on
// the key file itself (owner 2026-11-07: a .pub is never required; lore
// never reads the key's content).
func (sp setupProber) pubOf(ctx context.Context, key string) (pub, fingerprint string, noPub bool) {
	full := sp.p.ExpandPath(key) + onboard.PubKeySuffix
	k := sp.p.KeyFileFingerprint(ctx, key)
	if !sp.p.IsFile(full) {
		return "", k.Fingerprint, true
	}
	return sp.tildePath(full), k.Fingerprint, false
}

// aliasCache keeps setup.aliases answers (with and without keys) for
// opsdoctor.CacheTTL. Memory only, gone with the process.
type aliasCache struct {
	ttl time.Duration
	now func() time.Time

	mu sync.Mutex
	m  map[bool]aliasHit
	// keys keeps ssh -T -i KEY answers by key file (github.key_matches).
	keys map[string]keyHit
	// flight serialises the probes of one mode (false: aliases, true: keys).
	flight [2]sync.Mutex
	// inflight is the per-alias probe running now (aliasProbe).
	inflight map[string]*aliasFlight
	// life ends at close: every probe's context is detached from its
	// request but never from life, so close ends each ssh -T (and its
	// process group, adapters/probe runKillable) instead of leaving it to
	// outlive the App. bg counts the sweep goroutines close waits for;
	// closed (under mu) refuses new ones.
	life   context.Context
	end    context.CancelFunc
	bg     sync.WaitGroup
	closed bool
}

// close ends every probe still running (a slow alias a check left
// probing) and returns once each has exited and its goroutine is done.
// Nil-safe and idempotent; App.Close calls it. Before it, a CLI run that
// returned while an alias was still answering left that ssh behind.
func (c *aliasCache) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.end()
	c.bg.Wait()
}

// goBg runs fn on its own goroutine, counted for close; false (fn not run)
// once the cache is closed. A nil cache (a bare prober) just runs it.
func (c *aliasCache) goBg(fn func()) bool {
	if c == nil {
		go fn()
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.bg.Add(1)
	go func() {
		defer c.bg.Done()
		fn()
	}()
	return true
}

// lock takes mode keys' probe lock; nil-safe (a bare prober never waits).
func (c *aliasCache) lock(keys bool) func() {
	if c == nil {
		return func() {}
	}
	i := 0
	if keys {
		i = 1
	}
	c.flight[i].Lock()
	return c.flight[i].Unlock
}

type aliasHit struct {
	at  time.Time
	out opssetup.AliasesOut
}

func newAliasCache(now func() time.Time) *aliasCache {
	if now == nil {
		now = time.Now
	}
	life, end := context.WithCancel(context.Background())
	return &aliasCache{ttl: opsdoctor.CacheTTL, now: now, m: map[bool]aliasHit{}, keys: map[string]keyHit{}, inflight: map[string]*aliasFlight{}, life: life, end: end}
}

// get is the kept answer; a keys answer also serves a plain request.
func (c *aliasCache) get(keys bool) (opssetup.AliasesOut, bool) {
	if c == nil {
		return opssetup.AliasesOut{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range []bool{keys, true} {
		if h, ok := c.m[k]; ok && c.now().Sub(h.at) < c.ttl {
			if !keys {
				h.out.Keys = nil
			}
			return h.out, true
		}
	}
	return opssetup.AliasesOut{}, false
}

func (c *aliasCache) put(keys bool, out opssetup.AliasesOut) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[keys] = aliasHit{at: c.now(), out: out}
}

// keyMatches is github.key_matches (ONB 4.3): git's route for this setup
// logs in as the setup's account (https: gh has a login for it; ssh: the
// alias, or the recorded key, logs in as it and the recorded fingerprint
// still matches). In the step (answers given) only the answers count, so
// a key left from an earlier save does not judge a new pick; doctor reads
// the saved entry. The fix names an alias that logs in as the account
// (from the cached table).
func (sp setupProber) keyMatches(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	g := r.Setup.GitHub
	user, alias, key, route, fp := g.User, g.HostAlias, g.SSHKey, g.Route, g.KeyFingerprint
	if r.Answers != nil {
		user = strings.TrimSpace(r.Answers[onboard.QIDGitHubAccount].Text)
		alias = strings.TrimSpace(r.Answers[onboard.QIDHostAlias].Text)
		key = strings.TrimSpace(r.Answers[onboard.QIDSSHKey].Text)
		route = onboard.GitRoute(strings.TrimSpace(r.Answers[onboard.QIDRoute].Text))
		fp = "" // recorded at apply, from these answers
	}
	if alias == "" {
		alias = probe.GitHubHost
	}
	v := map[string]string{onboard.VarUser: user, onboard.VarAlias: alias, onboard.VarSSHKey: key}
	if user == "" {
		return skip(skipNoUser)
	}
	if route == onboard.RouteHTTPS {
		has := slices.ContainsFunc(sp.p.GHLogins().Accounts, func(a probe.GHAccount) bool { return strings.EqualFold(a.User, user) })
		st, detail := onboard.KeyMatch(onboard.KeyMatchIn{User: user, Route: route, GHHasLogin: has})
		return result(st, detail, v)
	}
	cfg, err := sp.p.ReadSSHConfig(ctx)
	if err != nil {
		return result(onboard.StatusError, err.Error(), v)
	}
	in := onboard.KeyMatchIn{User: user, Alias: alias, Key: key, Fingerprint: fp}
	if key != "" {
		in.KeyFile = sp.p.ExpandPath(strings.TrimSuffix(key, onboard.PubKeySuffix))
		in.KeyExists = sp.p.IsFile(in.KeyFile)
	}
	eff := cfg.Effective(alias)
	if len(eff.IdentityFiles) > 0 {
		in.AliasKeyShown = eff.IdentityFiles[0]
		for _, f := range eff.IdentityFiles {
			in.AliasKeys = append(in.AliasKeys, sp.p.ExpandPath(f))
		}
	}
	if fp != "" {
		switch {
		case in.KeyExists:
			in.FingerprintNow = sp.p.KeyFileFingerprint(ctx, in.KeyFile).Fingerprint
		case key == "" && len(in.AliasKeys) > 0 && sp.p.IsFile(in.AliasKeys[0]):
			in.FingerprintNow = sp.p.KeyFileFingerprint(ctx, in.AliasKeys[0]).Fingerprint
		}
	}
	// The key alone and the check's own alias first (cached answers at
	// once); the other aliases only on a fail, to name the fix. A fix
	// already known (cached, or the own alias logs in as the account) is
	// used at once and the rest keep probing in the background, so a re-run
	// can name a better one; with none known, wait up to keyMatchNoFixWait.
	start := time.Now()
	sweep := sp.newSweep(ctx, cfg, alias)
	sweep.start(alias)
	if key != "" && in.KeyExists {
		k := sp.keyLogin(ctx, in.KeyFile)
		in.KeyLogin, in.KeyAnswered = k.User, answered(k.Class)
	}
	a := sweep.wait(ctx, alias)
	in.AliasLogin, in.AliasAnswered = a.User, answered(a.Class)
	st, detail := onboard.KeyMatch(in)
	if st != onboard.StatusFail {
		return result(st, detail, v)
	}
	sweep.start(sweep.order...)
	got, pending := sweep.snapshot()
	fix, found := fixAlias(sweep.order, got, user)
	if !found && len(pending) > 0 {
		got, pending = sweep.until(ctx, start.Add(keyMatchNoFixWait), func(got map[string]probe.SSHAuthResult) bool {
			_, ok := fixAlias(sweep.order, got, user)
			return ok
		})
		fix, found = fixAlias(sweep.order, got, user)
	}
	if found {
		v[onboard.VarFixAlias] = fix
		// The setup's own alias logs in as the account and only the
		// recorded key is wrong: the fix drops that key (the alias's own
		// key decides), which is what github.ssh_user's Run it does;
		// recording the alias's .pub instead
		// pinned a second key path the person never picked.
		dropKey := key != "" && fix == alias
		if pub := sp.aliasPub(cfg, fix); pub != "" && !dropKey {
			v[onboard.VarFixKey] = pub
			v[onboard.VarFixVariant] = string(onboard.FixKeyPair)
		} else {
			v[onboard.VarFixVariant] = string(onboard.FixKeyAliasOnly)
		}
	}
	if !found || fix == probe.GitHubHost {
		// A better (named) alias may be among the silent ones: say which.
		detail += silentNote(sweep.order, got, pending)
	}
	return result(st, detail, v)
}

// silentNote names the aliases the fix could not hear from: those still
// probing (a re-run fills them in from the cache) and those that timed
// out. "" when every alias answered.
func silentNote(order []string, got map[string]probe.SSHAuthResult, pending []string) string {
	var timedOut []string
	for _, name := range order {
		r, ok := got[name]
		switch {
		case ok && realTimeout(r):
			timedOut = append(timedOut, name)
		case ok && r.Class == probe.ClassTimeout:
			// gave up before its limit (never a verdict): still unknown
			pending = append(slices.Clip(pending), name)
		}
	}
	note := ""
	if len(timedOut) > 0 {
		note += "; " + strings.Join(timedOut, ", ") + " did not answer in " + aliasProbeTimeout.String()
	}
	if len(pending) > 0 {
		note += "; still asking " + strings.Join(pending, ", ") + " (run the check again to fill in)"
	}
	return note
}

// fixAlias is the alias the key_matches fix names: one that logs in as
// user, a named alias before plain github.com, in config order.
func fixAlias(order []string, got map[string]probe.SSHAuthResult, user string) (string, bool) {
	best := ""
	for _, name := range order {
		r, ok := got[name]
		if !ok || !strings.EqualFold(r.User, user) {
			continue
		}
		if best == "" || best == probe.GitHubHost {
			best = name
		}
	}
	return best, best != ""
}

// aliasPub is the alias's first IdentityFile + .pub in ~ form, "" when
// the alias sets no key or the .pub is not there.
func (sp setupProber) aliasPub(cfg probe.SSHConfig, alias string) string {
	eff := cfg.Effective(alias)
	if len(eff.IdentityFiles) == 0 {
		return ""
	}
	full := sp.p.ExpandPath(eff.IdentityFiles[0]) + onboard.PubKeySuffix
	if !sp.p.IsFile(full) {
		return ""
	}
	return sp.tildePath(full)
}

// answered: GitHub gave a verdict (greeted, or refused the key).
func answered(c probe.Class) bool { return c == probe.ClassOK || c == probe.ClassAuthDenied }

// aliasSweep is the aliases of one check, probed in parallel (newSweep,
// then start for the ones the check needs); answers arrive as each ssh -T
// ends. The probes outlive the request that started them (bounded by
// aliasBatchTimeout), so a slow alias still lands in the cache.
type aliasSweep struct {
	order []string // config order, github.com included
	sp    setupProber
	bg    context.Context
	mu    sync.Mutex
	got   map[string]probe.SSHAuthResult
	// started: probes asked for (start); the rest are pending, not probed.
	started map[string]bool
	// changed gets a token after every answer (buffer 1: a waiter wakes
	// at least once per batch of answers).
	changed chan struct{}
}

// newSweep is a sweep over cfg's aliases (plus own) with nothing started:
// start probes the ones asked for, so a check can probe the alias it
// needs first and the rest only when it needs them.
func (sp setupProber) newSweep(ctx context.Context, cfg probe.SSHConfig, own string) *aliasSweep {
	s := &aliasSweep{got: map[string]probe.SSHAuthResult{}, started: map[string]bool{}, changed: make(chan struct{}, 1), sp: sp}
	for _, a := range githubAliasList(cfg) {
		s.order = append(s.order, a.Alias)
	}
	if !slices.Contains(s.order, own) {
		s.order = append(s.order, own)
	}
	// Detached from the caller's end (a CLI run may return first), never
	// from its values: the trace and the check label still apply. The
	// timer frees the context at the bound; probes end long before.
	var cache *aliasCache
	if sp.a != nil {
		cache = sp.a.aliasCache
	}
	s.bg = cache.detached(ctx)
	return s
}

// start probes names not started yet. An answer already kept (per alias,
// or a full table) is taken at once, before start returns, so a snapshot
// right after start sees every cached answer; the rest run in parallel.
func (s *aliasSweep) start(names ...string) {
	var cache *aliasCache
	if s.sp.a != nil {
		cache = s.sp.a.aliasCache
	}
	for _, name := range names {
		s.mu.Lock()
		if s.started[name] {
			s.mu.Unlock()
			continue
		}
		s.started[name] = true
		s.mu.Unlock()
		if r, ok := s.sp.cachedAlias(cache, name); ok {
			s.set(name, r)
			continue
		}
		if !cache.goBg(func() { s.set(name, s.sp.aliasLogin(s.bg, name)) }) {
			// The App is closing: no new ssh; the alias stays unanswered.
			s.set(name, probe.SSHAuthResult{Outcome: probe.Outcome{Class: probe.ClassTimeout}, Host: name})
		}
	}
}

// set records one answer and wakes a waiter.
func (s *aliasSweep) set(name string, r probe.SSHAuthResult) {
	s.mu.Lock()
	s.got[name] = r
	s.mu.Unlock()
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// snapshot is a copy of the answers so far and the aliases still pending.
func (s *aliasSweep) snapshot() (map[string]probe.SSHAuthResult, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	got := make(map[string]probe.SSHAuthResult, len(s.got))
	var pending []string
	for _, name := range s.order {
		if r, ok := s.got[name]; ok {
			got[name] = r
		} else {
			pending = append(pending, name)
		}
	}
	return got, pending
}

// wait is alias's answer, waiting for it (its probe is bounded by
// aliasProbeTimeout); ctx ending first gives a timeout result.
func (s *aliasSweep) wait(ctx context.Context, alias string) probe.SSHAuthResult {
	for {
		got, _ := s.snapshot()
		if r, ok := got[alias]; ok {
			return r
		}
		select {
		case <-s.changed:
		case <-ctx.Done():
			return probe.SSHAuthResult{Outcome: probe.Outcome{Class: probe.ClassTimeout}, Host: alias}
		}
	}
}

// until waits for answers until every alias answered, enough(answers)
// holds, the deadline passes or ctx ends; it returns the answers and the
// aliases that had not answered by then.
func (s *aliasSweep) until(ctx context.Context, deadline time.Time, enough func(map[string]probe.SSHAuthResult) bool) (map[string]probe.SSHAuthResult, []string) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		got, pending := s.snapshot()
		if len(pending) == 0 || enough(got) {
			return got, pending
		}
		select {
		case <-s.changed:
		case <-timer.C:
			return s.snapshot()
		case <-ctx.Done():
			return s.snapshot()
		}
	}
}

// aliasLogin is ssh -T through one alias (github.com: no alias), kept for
// the cache TTL; a fresh alias table answers it without a probe.
func (sp setupProber) aliasLogin(ctx context.Context, alias string) probe.SSHAuthResult {
	var cache *aliasCache
	if sp.a != nil {
		cache = sp.a.aliasCache
	}
	if r, ok := sp.cachedAlias(cache, alias); ok {
		return r
	}
	return sp.aliasProbe(ctx, alias, false)
}

// cachedAlias is alias's kept answer: a row of a kept full table (not a
// timeout row), else its per-alias answer. No probe.
func (sp setupProber) cachedAlias(cache *aliasCache, alias string) (probe.SSHAuthResult, bool) {
	if t, ok := cache.get(false); ok {
		for _, a := range t.Aliases {
			// A table row that did not answer is asked again below (its
			// per-alias answer, kept or in flight, says whether it timed out).
			if a.Alias == alias && a.Status != string(probe.ClassTimeout) {
				return probe.SSHAuthResult{Outcome: probe.Outcome{Class: probe.Class(a.Status)}, Host: alias, User: a.LogsInAs}, true
			}
		}
	}
	return cache.getKey(aliasKey(alias))
}

// aliasKey is alias's key in the per-alias answers.
func aliasKey(alias string) string { return "alias\x00" + alias }

// aliasProbe is one alias's ssh -T, shared: a kept answer (unless fresh),
// else the probe already running for it (a slow alias left running by an
// earlier check), else a new probe. A timeout is kept for aliasTimeoutTTL
// only (getKey).
func (sp setupProber) aliasProbe(ctx context.Context, alias string, fresh bool) probe.SSHAuthResult {
	var cache *aliasCache
	if sp.a != nil {
		cache = sp.a.aliasCache
	}
	k := aliasKey(alias)
	if !fresh {
		if r, ok := cache.getKey(k); ok {
			return r
		}
	}
	return cache.flight1(ctx, k, func() probe.SSHAuthResult {
		route := probe.Route{HostAlias: alias}
		if alias == probe.GitHubHost {
			route = probe.Route{}
		}
		r := sp.p.SSHAuthWithin(cache.detached(ctx), route, aliasProbeTimeout)
		cache.putKeyChecked(k, r)
		return r
	})
}

// detached is ctx without its cancel (values kept: trace, label), bounded
// by aliasBatchTimeout. A shared or cached probe must not end with the
// request that happened to start it: when the page dropped its
// setup.aliases call (a re-render), every alias came back as "timeout" in
// 0 s, was kept, and github.key_matches then said all aliases "did not
// answer in 3s" and lost its runnable fix (repair, 2026-11-06). The probe
// itself is still bounded by aliasProbeTimeout. It does end with the App
// (aliasCache.close): a probe outliving the process that started it was an
// orphan ssh. Nil-safe: a bare prober's probes end at the bound only.
func (c *aliasCache) detached(ctx context.Context) context.Context {
	d, cancel := context.WithTimeout(context.WithoutCancel(ctx), aliasBatchTimeout)
	// The timer frees the context at the bound; the probe ends long before.
	context.AfterFunc(d, cancel)
	if c != nil {
		stop := context.AfterFunc(c.life, cancel)
		context.AfterFunc(d, func() { stop() })
	}
	return d
}

// realTimeout: r is a timeout that ran its whole time limit (the alias
// truly did not answer), not a caller that gave up or a slot wait.
func realTimeout(r probe.SSHAuthResult) bool {
	return r.Class == probe.ClassTimeout && r.Took >= aliasProbeTimeout
}

// putKeyChecked keeps r unless it is a timeout that did not run its full
// limit: such a "timeout" says nothing about the alias.
func (c *aliasCache) putKeyChecked(k string, r probe.SSHAuthResult) {
	if r.Class == probe.ClassTimeout && !realTimeout(r) {
		return
	}
	c.putKey(k, r)
}

// flight1 runs probe once per key at a time: a second caller waits for the
// running probe's answer (or its own ctx). Nil-safe: a bare prober runs it.
func (c *aliasCache) flight1(ctx context.Context, key string, probeFn func() probe.SSHAuthResult) probe.SSHAuthResult {
	if c == nil {
		return probeFn()
	}
	c.mu.Lock()
	f, running := c.inflight[key]
	if !running {
		f = &aliasFlight{done: make(chan struct{})}
		c.inflight[key] = f
	}
	c.mu.Unlock()
	if !running {
		f.r = probeFn()
		c.mu.Lock()
		delete(c.inflight, key)
		c.mu.Unlock()
		close(f.done)
		return f.r
	}
	select {
	case <-f.done:
		return f.r
	case <-ctx.Done():
		return probe.SSHAuthResult{Outcome: probe.Outcome{Class: probe.ClassTimeout}}
	}
}

// aliasFlight is one running probe; r is set before done closes.
type aliasFlight struct {
	done chan struct{}
	r    probe.SSHAuthResult
}

// keyLogin is ssh -T with keyFile alone, kept for the cache TTL per key.
func (sp setupProber) keyLogin(ctx context.Context, keyFile string) probe.SSHAuthResult {
	var cache *aliasCache
	if sp.a != nil {
		cache = sp.a.aliasCache
	}
	if r, ok := cache.getKey(keyFile); ok {
		return r
	}
	r := sp.p.SSHAuthWithin(cache.detached(ctx), probe.Route{SSHKey: keyFile, NoAgent: true}, aliasProbeTimeout)
	cache.putKeyChecked(keyFile, r)
	return r
}

// keyHit is one kept per-key answer.
type keyHit struct {
	at time.Time
	r  probe.SSHAuthResult
}

func (c *aliasCache) getKey(k string) (probe.SSHAuthResult, bool) {
	if c == nil {
		return probe.SSHAuthResult{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.keys[k]
	ttl := c.ttl
	if h.r.Class == probe.ClassTimeout {
		ttl = min(ttl, aliasTimeoutTTL)
	}
	if !ok || c.now().Sub(h.at) >= ttl {
		return probe.SSHAuthResult{}, false
	}
	return h.r, true
}

func (c *aliasCache) putKey(k string, r probe.SSHAuthResult) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys[k] = keyHit{at: c.now(), r: r}
}
