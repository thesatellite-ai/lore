package settings

// policy_question.go — the confirm question of policy set and policy unset
// (devco D-603). The generic question was "policy set?": it named neither
// the place the row is written to, nor the value it replaces, nor who it
// reaches, so a person or an agent could not decide from it, and the
// refusal gave no command to run once they had. The question now reads
//
//	policy set context.budget.internal in store acme/web: 8000 (default) -> 22000;
//	it applies to every repo linked to acme/web; run
//	`lore policy set --key context.budget.internal --value 22000 --to acme/web --yes`

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/store"
)

// WriteChecker is the Env upgrade that says whether a write to a store
// would go through (engine.CheckWritable).
type WriteChecker interface {
	CheckWritable(ctx context.Context, name store.Source) error
}

// checkWritable is the refusal a write to target would get, before the
// question is asked: in a repo linked to a store folder the default
// --to repo is refused, and asking first printed a --yes command that then
// failed the same way (devco D-603).
func (o ops) checkWritable(ctx context.Context, target store.Source) error {
	if w, ok := upgrade.As[WriteChecker](o.env); ok {
		return w.CheckWritable(ctx, target)
	}
	return nil
}

// checkPolicyWrite is every refusal a policy write to target gets before
// anything is asked or written: the place must take writes
// (checkWritable), and company ground (the org folder, a store root, every
// project folder of the company store, devco D-681) takes a policy from an
// admin of the org only (devco D-637: the roster's
// role rules; requirePolicyAdmin, every key). line is the command the
// admin runs.
func (o ops) checkPolicyWrite(ctx context.Context, target store.Source, key policy.Key, line string) error {
	if err := o.checkWritable(ctx, target); err != nil {
		return err
	}
	return o.requirePolicyAdmin(ctx, target, key, line)
}

// QuestionRun opens the last part of a policy question, the exact
// command that answers it with --yes: a command that runs a policy write
// for the person (branch share) asks the same question and puts its own
// command there.
const QuestionRun = "; run `"

// policySetQuestion is policy set's confirm question; "" (the generic
// question) for an unknown key, which the operation itself refuses.
func (o ops) policySetQuestion(ctx context.Context, in PolicySetIn) (string, error) {
	d, ok := policy.Lookup(policy.Key(in.Key))
	if !ok {
		return "", nil
	}
	target := targetOf(in.To)
	if err := o.checkPolicyWrite(ctx, target, d.Key, policyCommand("set", in.Key, in.Value, o.adminTo(ctx, target, in.To))); err != nil {
		return "", err
	}
	old, err := o.policyNow(ctx, d.Key)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("policy set %s in %s: %s -> %s; it applies to %s"+QuestionRun+"%s`",
		in.Key, o.policyPlace(target), old, in.Value, policyReach(storeLevel(o.env, target), o.placeLabel(target)), policyCommand("set", in.Key, in.Value, in.To)), nil
}

// policyUnsetQuestion is policy unset's confirm question.
func (o ops) policyUnsetQuestion(ctx context.Context, in PolicyUnsetIn) (string, error) {
	d, ok := policy.Lookup(policy.Key(in.Key))
	if !ok {
		return "", nil
	}
	target := targetOf(in.To)
	if err := o.checkPolicyWrite(ctx, target, d.Key, policyCommand("unset", in.Key, "", o.adminTo(ctx, target, in.To))); err != nil {
		return "", err
	}
	old, err := o.policyNow(ctx, d.Key)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("policy unset %s in %s (now %s; the next level up or the default applies); it applies to %s"+QuestionRun+"%s`",
		in.Key, o.policyPlace(target), old, policyReach(storeLevel(o.env, target), o.placeLabel(target)), policyCommand("unset", in.Key, "", in.To)), nil
}

// policyNow is key's effective value here and where it comes from:
// "8000 (default)" or "16000 (set in store acme/web)".
func (o ops) policyNow(ctx context.Context, key policy.Key) (string, error) {
	entries, where, err := o.policyEntries(ctx)
	if err != nil {
		return "", err
	}
	set, err := policy.Resolve(entries)
	if err != nil {
		return "", err
	}
	eff, _ := set.Get(key)
	if eff.IsDefault || eff.Source.IsZero() {
		return orNone(display(eff.Value)) + " (" + sourceDefault + ")", nil
	}
	return display(eff.Value) + " (set in " + o.policyPlace(where[eff.Source]) + ")", nil
}

// policyPlace names where a policies row is written: the repo store is the
// repo's own .lore/ folder (committed with the code), any other place is a
// store or a folder of one, by its <store>/<folder> label.
func (o ops) policyPlace(src store.Source) string {
	if src == "" || src == store.SourceRepo {
		return "this repo's .lore/ (committed with the repo)"
	}
	return "store " + o.placeLabel(src)
}

// placeLabel is src as search and the lists name it: "acme/web" for the
// store acme attached at its folder web (the Env's store.SourceDescriber),
// else the name (devco D-600: one label for one place).
func (o ops) placeLabel(src store.Source) string {
	d, ok := upgrade.As[store.SourceDescriber](o.env)
	if !ok {
		return string(src)
	}
	ref := d.DescribeSource(src)
	if ref.Folder != "" && !strings.Contains(ref.Store, folderSep) {
		return ref.Store + folderSep + ref.Folder
	}
	return string(src)
}

// policyReach says who a row at level lv of the place labelled src reaches.
func policyReach(lv policy.Level, src string) string {
	switch lv {
	case policy.LevelRepo:
		return "everyone working in this repo"
	case policy.LevelPersonal:
		return "only you"
	case policy.LevelOrg:
		org, _, _ := strings.Cut(src, folderSep)
		return "every folder and repo of " + org + " (a folder may only tighten it)"
	case policy.LevelFolder:
		return "every repo linked to " + src
	case policy.LevelStoreRoot:
		return "every folder and repo of store " + src
	}
	return "everyone who reads " + src
}

// policyCommand is the exact command that answers the question with --yes.
func policyCommand(verb, key, val, to string) string {
	return profilestore.PolicyCommand(verb, key, val, to)
}
