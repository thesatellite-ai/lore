package reveal

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/secretmgr"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/store"
)

// Auditor is the Env upgrade that records secret.* events (wire.App has
// it). A reveal without it is refused: a reveal is never unaudited.
type Auditor interface {
	AuditSecret(ctx context.Context, action engine.Action, s engine.SecretAudit) error
}

// ManagerSource is an optional Env upgrade giving the managers (wire.App
// passes secrets.manager_timeout; tests hand in fakes). Without it the
// built-in managers with their default timeout are used.
type ManagerSource interface {
	SecretManagers(ctx context.Context) (secretmgr.Set, error)
}

// Managers is the manager set of an env (built-ins with the default
// timeout when it has none).
func Managers(ctx context.Context, env ManagerSource) (secretmgr.Set, error) {
	if env != nil {
		return env.SecretManagers(ctx)
	}
	return secretmgr.Builtin(secretmgr.Options{}), nil
}

// Target is one manager target of a secret reference.
type Target struct {
	// Ref is the target as text (manager://locator?options#fragment).
	Ref     string
	Manager string
	Loc     secretmgr.Locator
}

// Of turns a parsed target into a Target; ok is false for the fixed
// targets (unstored, redacted), which have nothing to reveal.
func Of(t refs.SecretTarget) (Target, bool) {
	if t.Fixed != "" || t.Manager == "" {
		return Target{}, false
	}
	l := secretmgr.Locator{Path: t.Locator, Fragment: t.Fragment}
	for _, op := range t.Options {
		if op.Name != refs.OptHolder && op.Name != refs.OptLabel {
			l.Options = append(l.Options, secretmgr.Option{Name: op.Name, Value: op.Value})
		}
	}
	return Target{Ref: TargetText(t), Manager: t.Manager, Loc: l}, true
}

// TargetText is a target as text: the fixed word, or
// manager://locator?options#fragment with each part escaped.
func TargetText(t refs.SecretTarget) string {
	if t.Fixed != "" {
		return t.Fixed
	}
	segs := strings.Split(t.Locator, "/")
	for i, s := range segs {
		segs[i] = strings.ReplaceAll(url.PathEscape(s), ":", "%3A")
	}
	var b strings.Builder
	b.WriteString(t.Manager + "://" + strings.Join(segs, "/"))
	for i, o := range t.Options {
		if i == 0 {
			b.WriteByte('?')
		} else {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(o.Name) + "=" + url.QueryEscape(o.Value))
	}
	if t.Fragment != "" {
		b.WriteString("#" + url.PathEscape(t.Fragment))
	}
	return b.String()
}

// Gate is how a reveal was allowed.
type Gate struct {
	Confirmed bool
	Forced    bool
	By        engine.ForcedBy
}

// Warning is the W_REVEAL_FORCED line a forced reveal prints (2.7).
const Warning = "W_REVEAL_FORCED: revealed without a person's answer; recorded in the audit log as forced"

// Ask settles SQ10: --yes (a person said yes) passes; --force, or an agent
// under secrets.agent_reveal: allow, passes as forced; anyone else gets
// E_NEEDS_CONFIRM with the question (the CLI asks a terminal and re-runs
// with yes). No session-wide yes: the question is per command. groups
// holds each reference's alternates; pol is nil when the env has none.
func Ask(ctx context.Context, pol bulk.Policier, groups [][]Target, force bool) (Gate, error) {
	if engine.Confirmed(ctx) {
		return Gate{Confirmed: true}, nil
	}
	if force {
		return Gate{Forced: true, By: engine.ForcedFlag}, nil
	}
	agent := engine.AgentOf(ctx) != ""
	if agent && pol != nil {
		set, err := pol.Policy(ctx, store.SourceRepo)
		if err != nil {
			return Gate{}, err
		}
		if e, found := set.Get(policy.KeyAgentReveal); found && e.Value != nil && e.Value.String() == string(policy.AgentRevealAllow) {
			return Gate{Forced: true, By: engine.ForcedPolicy}, nil
		}
	}
	var names []string
	for _, g := range groups {
		names = append(names, g[0].Manager+" ("+g[0].Loc.Path+")")
	}
	q := fmt.Sprintf("lore will read %d secret(s) from %s and print it in clear. This is recorded in the audit log.", len(groups), strings.Join(names, ", "))
	if agent {
		q += " The value will be printed to this agent's session."
	}
	return Gate{}, errs.New(errs.NeedsConfirm, q+" Continue?").WithHint("ask the person, then rerun with --yes (an agent alone: --force, audited as FORCED)")
}

// One is one revealed reference: which target answered, and the value
// (printed only because the caller asked with --reveal).
type One struct {
	Ref      string `json:"ref"`
	Answered string `json:"answered"`
	Value    string `json:"value"`
}

// Group tries a reference's alternates in order; the first that answers
// is returned. Every attempt is audited without its value. command names
// the surface in the audit record ("secrets show", "show"); rowID is ""
// for a bare reference.
func Group(ctx context.Context, set secretmgr.Set, a Auditor, grp []Target, g Gate, command, rowID string) (One, error) {
	var last error
	for _, t := range grp {
		au := engine.SecretAudit{Ref: t.Ref, Command: command, Forced: g.Forced, ForcedBy: g.By}
		if id, perr := ids.ParseAny(rowID); perr == nil {
			au.Row = id
		}
		m, ok := set.Get(t.Manager)
		if !ok {
			last = errs.New(errs.RefScheme, "no secret manager \""+t.Manager+"\"")
			continue
		}
		s, err := m.Reveal(ctx, t.Loc)
		if err != nil {
			last = err
			au.Result = string(errs.CodeOf(err))
			if aerr := a.AuditSecret(ctx, engine.ActionSecretRevealFailed, au); aerr != nil {
				return One{}, aerr
			}
			continue
		}
		if aerr := a.AuditSecret(ctx, engine.ActionSecretRevealed, au); aerr != nil {
			return One{}, aerr
		}
		return One{Ref: grp[0].Ref, Answered: t.Ref, Value: expose(s)}, nil
	}
	return One{}, last
}

// expose is the ONLY caller of Secret.Expose in lore's source (10.2; a
// source test pins it): the --reveal printer.
func expose(s secretmgr.Secret) string { return s.Expose() }
