package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/thesatellite-ai/lore/adapters/secretmgr"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/reveal"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// lore secrets offboarding (SECRETS_AND_REFERENCES 2.6, 2.7, 7, 15, 16):
// the index of every secret reference, reveal (SQ10, D18), verify, and the
// bulk rewrites of holder, label and entry name. Lore never copies a
// secret between vaults and never lists a vault (SQ6 dropped).
const (
	OpSecretsList        = "secrets.list"
	OpSecretsShow        = "secrets.show"
	OpSecretsCheck       = "secrets.check"
	OpSecretsMove        = "secrets.move"
	OpSecretsRelabel     = "secrets.relabel"
	OpSecretsRenameEntry = "secrets.rename_entry"
	OpSecretsLink        = "secrets.link"
)

// Words of the audit records and the messages.
const (
	cmdSecretsShow = "secrets show"
	secretOpen     = "${secret:"
	secretClose    = "}"
	rotateTitle    = "Rotate secrets %s held: %d entries"
	unstoredTitle  = "Store the secret redacted from %s, then rotate it"
	machinePrefix  = "mch_"
	actorPrefix    = "act_"
	handleSep      = "."
)

// SecretAuditor is the Env upgrade that records secret.* events (wire.App
// has it). A reveal without it is refused: a reveal is never unaudited.
type SecretAuditor = reveal.Auditor

// SecretManagerSource is an optional Env upgrade giving the managers (tests
// hand in fakes; wire.App passes secrets.manager_timeout). Without it the
// built-in managers with their default timeout are used.
type SecretManagerSource = reveal.ManagerSource

// Holderer is an optional Env upgrade: this machine's holder id and label
// for a reference lore writes (5.2). Without it lore secrets link writes
// no holder unless --holder is given.
type Holderer interface {
	ThisHolder(ctx context.Context) (id, label string, err error)
}

// sharedManagers are the managers whose entry stands for the team's
// access (a 1Password shared vault, Vault, AWS Secrets Manager): lore
// records no holder for them on its own (SECRETS 5.2; the opt-in
// secrets.holder_for_shared is deferred with the plugin system, SQ9, so it
// is off). A holder typed with --holder is still written.
var sharedManagers = map[string]bool{secretmgr.OnePass: true, secretmgr.Vault: true, secretmgr.AWS: true}

// SecretsListIn is the input of secrets.list.
type SecretsListIn struct {
	Holder   string `json:"holder,omitempty" flag:"holder" desc:"only references held by this person, person.machine label or holder id"`
	Manager  string `json:"manager,omitempty" flag:"manager" desc:"only this manager scheme (gopass, op, aws-secrets, …)"`
	Unstored bool   `json:"unstored,omitempty" flag:"unstored" desc:"only ${secret:unstored} placeholders"`
	Missing  bool   `json:"missing,omitempty" flag:"missing" desc:"check every entry now and keep the missing ones (opens the managers)"`
}

// SecretsShowIn is the input of secrets.show.
type SecretsShowIn struct {
	ID     string `json:"id" arg:"0" desc:"a secret reference (manager://entry or ${secret:…}) or a row id"`
	Reveal bool   `json:"reveal,omitempty" flag:"reveal" desc:"read the value from the manager and print it (asks every time; audited)"`
	Force  bool   `json:"force,omitempty" flag:"force" desc:"reveal without a person's answer (agents; audited as FORCED)"`
}

// SecretsCheckIn is the input of secrets.check.
type SecretsCheckIn struct {
	Manager string `json:"manager,omitempty" flag:"manager" desc:"only this manager scheme"`
	Holder  string `json:"holder,omitempty" flag:"holder" desc:"only references held by this person or holder id"`
}

// SecretsMoveIn is the input of secrets.move.
type SecretsMoveIn struct {
	From    string `json:"from" flag:"from" desc:"the current holder (person, person.machine or holder id)"`
	To      string `json:"to_holder" flag:"to" desc:"the new holder (person.machine label of a known machine, or an mch_/act_ id)"`
	Manager string `json:"manager,omitempty" flag:"manager" desc:"only this manager scheme"`
	Entry   string `json:"entry,omitempty" flag:"entry" desc:"only entries matching this glob (acme/hub/*)"`
}

// SecretsRelabelIn is the input of secrets.relabel.
type SecretsRelabelIn struct {
	Holder string `json:"holder,omitempty" flag:"holder" desc:"only this holder id"`
	// Label sets this exact label on every reference of --holder instead of
	// deriving it (CLI_REFERENCE 3.13); it asks first, --yes answers.
	Label string `json:"label,omitempty" flag:"label" desc:"with --holder: the label to write (asks first)"`
}

// SecretsRenameIn is the input of secrets.rename_entry.
type SecretsRenameIn struct {
	Manager string `json:"manager" flag:"manager" desc:"the manager scheme"`
	From    string `json:"from" flag:"from" desc:"the old entry name"`
	To      string `json:"to_entry" flag:"to" desc:"the new entry name"`
}

// SecretsLinkIn is the input of secrets.link.
type SecretsLinkIn struct {
	Row    string `json:"id" arg:"0" desc:"the row holding ${secret:unstored}"`
	At     int    `json:"at" flag:"at" desc:"which placeholder (1-based, as lore secrets list --unstored prints it)"`
	Ref    string `json:"ref" flag:"ref" desc:"the entry, as manager://entry (holder options optional)"`
	Holder string `json:"holder,omitempty" flag:"holder" desc:"the holder id to record (default this machine for a personal manager; none for op, vault, aws-secrets)"`
}

// SecretsOut is the output of secrets.list, show and check.
type SecretsOut struct {
	Count int         `json:"count"`
	Data  []SecretUse `json:"data"`
	// Revealed, Forced, Warning and Values: secrets show --reveal (2.7).
	Revealed int           `json:"revealed,omitempty"`
	Forced   bool          `json:"forced,omitempty"`
	Warning  string        `json:"warning,omitempty"`
	Values   []RevealedOne `json:"values,omitempty"`
	// Tasks are the store-then-rotate tasks secrets check added (D21).
	Tasks []string `json:"tasks,omitempty"`
	Human string   `json:"-"`
}

// RevealedOne is one revealed reference: which target answered, and the
// value (printed only because the caller asked with --reveal).
type RevealedOne = reveal.One

// Text implements op.Texter.
func (o SecretsOut) Text() string { return o.Human }

// SecretsBatchOut is the output of the bulk rewrites and link.
type SecretsBatchOut struct {
	Batch string   `json:"batch,omitempty"`
	Count int      `json:"count"`
	Rows  []string `json:"rows"`
	Task  string   `json:"task,omitempty"`
	Human string   `json:"-"`
}

// Text implements op.Texter.
func (o SecretsBatchOut) Text() string { return o.Human }

// secretErrors are the codes the secret index ops may return.
var secretErrors = append(slices.Clone(writeErrors), errs.VaultEntryUnavailable, errs.RefSyntax, errs.RefScheme)

func (o ops) secretIndexOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpSecretsList, Summary: "Every secret reference in this view: entry, holder, row and field (opens no vault)", Permission: op.PermRead,
			Access: op.AccessRead, Errors: secretErrors,
			Examples: []op.Example{{Title: "references a person holds", CLI: "lore secrets list --holder tomas", Want: "references"}}}, o.secretsList),
		op.Define(op.Spec{Name: OpSecretsShow, Summary: "Show a secret reference or a row's references; --reveal prints the value (asks every time)", Permission: op.PermRead,
			Access: op.AccessRead, Errors: secretErrors, Confirm: op.ConfirmRule{MayAsk: true}, Examples: []op.Example{{Title: "one secret reference", CLI: "lore secrets show var_1c0b"}}}, o.secretsShow),
		op.Define(op.Spec{Name: OpSecretsCheck, Summary: "Verify each referenced entry by its name (the value is read and dropped); adds the store-then-rotate tasks", Permission: op.PermWrite,
			Access: op.AccessWrite, Errors: secretErrors, Examples: []op.Example{{Title: "verify every referenced entry", CLI: "lore secrets check"}}}, o.secretsCheck),
		op.Define(op.Spec{Name: OpSecretsMove, Summary: "Rewrite the holder of matching references (one batch; never copies a secret)", Permission: op.PermWrite,
			Access: op.AccessWrite, Errors: secretErrors, Confirm: op.ConfirmRule{MayAsk: true}, Examples: []op.Example{{Title: "a person's secrets moved to a new machine", CLI: "lore secrets move --from alice.old-laptop --to alice.work-laptop"}}}, o.secretsMove),
		op.Define(op.Spec{Name: OpSecretsRelabel, Summary: "Rewrite each reference's holder label from its holder id (one batch)", Permission: op.PermWrite,
			Access: op.AccessWrite, Errors: secretErrors, Confirm: op.ConfirmRule{MayAsk: true}, Examples: []op.Example{{Title: "refresh every holder label", CLI: "lore secrets relabel"}}}, o.secretsRelabel),
		op.Define(op.Spec{Name: OpSecretsRenameEntry, Summary: "Rewrite an entry name everywhere (a renamed vault entry; one batch)", Permission: op.PermWrite,
			Access: op.AccessWrite, Errors: secretErrors, Examples: []op.Example{{Title: "a vault entry was renamed", CLI: "lore secrets rename-entry --manager gopass --from acme/db --to acme/hub/db"}}}, o.secretsRename),
		op.Define(op.Spec{Name: OpSecretsLink, Summary: "Fill a ${secret:unstored} placeholder with the entry it is now stored in", Permission: op.PermWrite,
			Access: op.AccessWrite, Errors: secretErrors, Examples: []op.Example{{Title: "fill an unstored placeholder", CLI: "lore secrets link mem_0c3a --at 1 --ref gopass://acme/hub/db-password"}}}, o.secretsLink),
	}
}

// managers is the manager set of this Env.
func (o ops) managers(ctx context.Context) (secretmgr.Set, error) {
	src, _ := o.env.(SecretManagerSource)
	return reveal.Managers(ctx, src)
}

// filterUses keeps the uses matching holder and manager.
func filterUses(rows []secretRow, holder, manager string, unstored bool) []SecretUse {
	out := []SecretUse{}
	for _, r := range rows {
		for _, u := range r.uses {
			if (manager != "" && u.Manager != manager) || !holderMatch(u, holder) || (unstored && u.Ref != refs.TargetUnstored) {
				continue
			}
			out = append(out, u)
		}
	}
	return out
}

// usesText is the 2.6 table.
func usesText(us []SecretUse, tail string) string {
	if len(us) == 0 {
		return "(no secret references)\n"
	}
	var b strings.Builder
	b.WriteString("REF\tHOLDER\tUSED IN\tSTATE\n")
	rows := map[string]bool{}
	for _, u := range us {
		holder := "-"
		switch {
		case u.Label != "" && u.Holder != "":
			holder = u.Label + " (" + shortHolder(u.Holder) + ")"
		case u.Holder != "":
			holder = shortHolder(u.Holder)
		case u.Label != "":
			holder = u.Label
		}
		used := u.Row + " " + u.Field
		if u.At > 0 {
			used += " (" + strconv.Itoa(u.At) + ")"
		}
		if u.Row == "" { // a reference no row in this view holds
			used = "-"
		}
		state := u.State
		if state == "" {
			state = "-"
		} else if u.CheckedAt != "" {
			state += " (checked " + u.CheckedAt + ")"
		}
		if u.Row != "" {
			rows[u.Row] = true
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", u.Ref, holder, used, state)
	}
	fmt.Fprintf(&b, "%s in %s.%s\n", plural.Count(len(us), "reference"), plural.Count(len(rows), "row"), tail)
	return b.String()
}

// shortHolder is the 8-hex short form of a holder id (AGENT_IDENTITY 6.8).
func shortHolder(id string) string {
	p, rest, ok := strings.Cut(id, "_")
	if !ok || len(rest) <= 8 {
		return id
	}
	return p + "_" + rest[:8]
}

// secretsList is `lore secrets list` (2.6, 15): the index, filtered; no
// vault is opened unless --missing (DECISION SEC-IDX1).
func (o ops) secretsList(ctx context.Context, in SecretsListIn) (SecretsOut, error) {
	rows, err := o.secretIndex(ctx)
	if err != nil {
		return SecretsOut{}, err
	}
	us := filterUses(rows, in.Holder, in.Manager, in.Unstored)
	if in.Missing {
		us, err = o.verify(ctx, us)
		if err != nil {
			return SecretsOut{}, err
		}
		us = slices.DeleteFunc(us, func(u SecretUse) bool { return u.State != stateMissing })
		return SecretsOut{Count: len(us), Data: us, Human: usesText(us, "")}, nil
	}
	// FA-AGENT-3: a plain list shows this machine's last check, opening no vault.
	tail := ""
	last, err := o.loadChecks(ctx, us)
	if err != nil {
		return SecretsOut{}, err
	}
	if !last.IsZero() {
		tail = " State from the last lore secrets check on this machine, " + last.UTC().Format(time.DateOnly) + "."
	}
	return SecretsOut{Count: len(us), Data: us, Human: usesText(us, tail)}, nil
}

// verify checks each distinct manager target once: Reveal, drop the
// Secret unread, record secret.verified (6: present, missing, not_checked).
func (o ops) verify(ctx context.Context, us []SecretUse) ([]SecretUse, error) {
	set, err := o.managers(ctx)
	if err != nil {
		return nil, err
	}
	done := map[string]string{}
	for i, u := range us {
		if u.Manager == "" {
			continue
		}
		key := u.Manager + "://" + u.Locator + "#" + u.Fragment
		st, seen := done[key]
		if !seen {
			st = o.verifyOne(ctx, set, u)
			done[key] = st
			if a, ok := o.env.(SecretAuditor); ok {
				if err := a.AuditSecret(ctx, engine.ActionSecretVerified, engine.SecretAudit{Ref: u.Ref, Command: "secrets check", Result: st}); err != nil {
					return nil, err
				}
			}
		}
		us[i].State = st
	}
	// FA-AGENT-3: remember this machine's result for the plain listing.
	if err := o.saveChecks(ctx, us, opclock.Now(o.env)); err != nil {
		return nil, err
	}
	return us, nil
}

func (o ops) verifyOne(ctx context.Context, set secretmgr.Set, u SecretUse) string {
	m, ok := set.Get(u.Manager)
	if !ok {
		return stateNotChecked
	}
	_, err := m.Reveal(ctx, locatorOf(u))
	switch {
	case err == nil:
		return statePresent
	case isMissing(err):
		return stateMissing
	}
	return stateNotChecked
}

func isMissing(err error) bool { return err != nil && errors.Is(err, secretmgr.ErrMissing) }

// locatorOf is a use's manager locator (manager options only, never
// holder or label).
func locatorOf(u SecretUse) secretmgr.Locator {
	l := secretmgr.Locator{Path: u.Locator, Fragment: u.Fragment}
	parts := refs.Parse(secretOpen + u.Ref + secretClose)
	if len(parts) == 1 && len(parts[0].Ref.Alternates) == 1 {
		for _, op := range parts[0].Ref.Alternates[0].Options {
			if op.Name != refs.OptHolder && op.Name != refs.OptLabel {
				l.Options = append(l.Options, secretmgr.Option{Name: op.Name, Value: op.Value})
			}
		}
	}
	return l
}

// secretsCheck is `lore secrets check` (15): verify every matching entry,
// then make sure each row with ${secret:unstored} has its "store, then
// rotate" task (7, D21; idempotent by row).
func (o ops) secretsCheck(ctx context.Context, in SecretsCheckIn) (SecretsOut, error) {
	rows, err := o.secretIndex(ctx)
	if err != nil {
		return SecretsOut{}, err
	}
	us, err := o.verify(ctx, filterUses(rows, in.Holder, in.Manager, false))
	if err != nil {
		return SecretsOut{}, err
	}
	tail, added := "", []string{}
	for _, r := range rows {
		if in.Holder != "" || in.Manager != "" {
			break
		}
		id, made, terr := o.unstoredTask(ctx, r)
		if terr != nil {
			return SecretsOut{}, terr
		}
		if made {
			added = append(added, id)
			tail += "\nAdded task " + id + " (store the secret of " + r.Row.ID.String() + ", then rotate it)."
		}
	}
	return SecretsOut{Count: len(us), Data: us, Tasks: added, Human: usesText(us, tail)}, nil
}

// unstoredTask adds the D21 task for a row holding ${secret:unstored};
// made is false when the row has none or its task exists.
func (o ops) unstoredTask(ctx context.Context, r secretRow) (string, bool, error) {
	var fields []string
	for _, u := range r.uses {
		if u.Ref == refs.TargetUnstored && !slices.Contains(fields, u.Field) {
			fields = append(fields, u.Field)
		}
	}
	we, ok := o.env.(work.Env)
	if len(fields) == 0 || !ok {
		return "", false, nil
	}
	return AddUnstoredTask(ctx, we, r.Src, r.Row.ID.String(), r.Row.Table.String(), fields)
}

// AddUnstoredTask adds (or finds, by key secret-unstored-<row id>) the D21
// "store, then rotate" task for a row holding ${secret:unstored} in
// fields. lore secrets check calls it, and wire's engine RedactHook calls
// it at write time (on_detect: redact). made is false when it existed.
func AddUnstoredTask(ctx context.Context, env work.Env, src store.Source, rowID, table string, fields []string) (string, bool, error) {
	body := "Row " + rowID + " (" + table + "), fields " + strings.Join(fields, ", ") + ", holds $${secret:unstored} (escaped here so this task is not indexed as one).\n" +
		"1. Store the secret in a manager, then: lore secrets link " + rowID + " --at 1 --ref <manager>://<entry>\n" +
		"2. Rotate it (it was seen by a shell or an agent), then close this task."
	tid, existed, err := task.AddSecretTask(ctx, env, task.SecretTask{Store: src, Title: fmt.Sprintf(unstoredTitle, rowID), Body: body,
		Key: "secret-unstored-" + rowID, Rows: []string{rowID}})
	return tid, err == nil && !existed, err
}

// secretsShow is `lore secrets show <ref|row> [--reveal [--force]]` (2.7):
//
//	resolve the argument to uses ─► no --reveal: list them
//	--reveal ─► gate (SQ10: --yes, --force, or agent_reveal allow; else
//	E_NEEDS_CONFIRM) ─► auditor present ─► per reference: alternates in
//	order until one answers (19.7) ─► secret.revealed / reveal_failed
func (o ops) secretsShow(ctx context.Context, in SecretsShowIn) (SecretsOut, error) {
	us, err := o.usesOf(ctx, in.ID)
	if err != nil {
		return SecretsOut{}, err
	}
	out := SecretsOut{Count: len(us), Data: us}
	if !in.Reveal {
		if in.Force {
			return SecretsOut{}, errs.New(errs.InvalidInput, "--force goes with --reveal")
		}
		// A reference argument names the rows that hold it, as list does.
		if us, err = o.usedIn(ctx, us); err != nil {
			return SecretsOut{}, err
		}
		out.Count, out.Data = len(us), us
		out.Human = usesText(us, " Not revealed (add --reveal).")
		return out, nil
	}
	groups := revealGroups(us)
	if len(groups) == 0 {
		return SecretsOut{}, errs.New(errs.NotFound, "nothing to reveal: no manager reference in "+quote(in.ID))
	}
	pol, _ := o.env.(bulk.Policier)
	g, err := reveal.Ask(ctx, pol, groups, in.Force)
	if err != nil {
		return SecretsOut{}, err
	}
	auditor, ok := o.env.(SecretAuditor)
	if !ok {
		return SecretsOut{}, errs.New(errs.Internal, "this surface cannot audit a reveal, so lore does not reveal here")
	}
	set, err := o.managers(ctx)
	if err != nil {
		return SecretsOut{}, err
	}
	var b strings.Builder
	for _, grp := range groups {
		one, rerr := reveal.Group(ctx, set, auditor, grp, g, cmdSecretsShow, us[0].Row)
		if rerr != nil {
			return SecretsOut{}, rerr
		}
		out.Values = append(out.Values, one)
		fmt.Fprintf(&b, "%s = %s\n", one.Answered, one.Value)
	}
	out.Revealed, out.Forced = len(out.Values), g.Forced
	if g.Forced {
		out.Warning = reveal.Warning
		b.WriteString(out.Warning + "\n")
	}
	out.Human = b.String()
	return out, nil
}

// usesOf resolves a show argument: a row id (its references) or a
// reference text (bare manager://… or ${secret:…}).
func (o ops) usesOf(ctx context.Context, arg string) ([]SecretUse, error) {
	arg = strings.TrimSpace(arg)
	if strings.Contains(arg, "://") || strings.HasPrefix(arg, secretOpen) {
		text := arg
		if !strings.HasPrefix(text, secretOpen) {
			text = secretOpen + arg + secretClose
		}
		if err := refs.Validate(text); err != nil {
			return nil, err
		}
		var us []SecretUse
		for _, p := range refs.Parse(text) {
			if p.Kind == refs.PartRef && p.Ref.Scheme == refs.SchemeSecret {
				for i, t := range p.Ref.Alternates {
					u := useOf(SecretUse{Kind: useText}, "", t)
					u.Alt = i
					us = append(us, u)
				}
			}
		}
		if len(us) == 0 {
			return nil, errs.New(errs.RefSyntax, "no secret reference in the argument")
		}
		return us, nil
	}
	rows, err := o.secretIndex(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.Row.ID.String() == arg {
			return r.uses, nil
		}
	}
	if id, perr := ids.ParseAny(arg); perr == nil {
		// DELETION 3.2: a trashed row says where it is (E_IN_TRASH).
		for _, t := range schema.Builtin() {
			if t.Prefix != id.Prefix() {
				continue
			}
			if terr := inTrashByKey(ctx, o.env, engine.ReadStoresOf(o.env), t.Name, func(l located) bool { return l.Row.ID == id }); terr != nil {
				return nil, terr
			}
		}
	}
	return nil, errs.New(errs.NotFound, "no secret reference in row "+quote(arg)).WithHint("lore secrets list shows every row that holds one")
}

// usedIn replaces each row-less use (a reference given as the show
// argument) with the uses of every row in this view that holds the same
// entry: same manager, locator and fragment (holder and label options
// ignored), or the same fixed text (unstored, redacted). A reference no
// row holds stays as given, row-less, so show still prints it.
func (o ops) usedIn(ctx context.Context, us []SecretUse) ([]SecretUse, error) {
	if !slices.ContainsFunc(us, func(u SecretUse) bool { return u.Row == "" }) {
		return us, nil
	}
	rows, err := o.secretIndex(ctx)
	if err != nil {
		return nil, err
	}
	same := func(a, b SecretUse) bool {
		if a.Manager == "" || b.Manager == "" {
			return a.Manager == b.Manager && a.Ref == b.Ref
		}
		return a.Manager == b.Manager && a.Locator == b.Locator && a.Fragment == b.Fragment
	}
	out := []SecretUse{}
	for _, u := range us {
		if u.Row != "" {
			out = append(out, u)
			continue
		}
		found := false
		for _, r := range rows {
			for _, held := range r.uses {
				if same(u, held) {
					out, found = append(out, held), true
				}
			}
		}
		if !found {
			out = append(out, u)
		}
	}
	return out, nil
}

// revealGroups groups the uses by reference (row, field, position): each
// group's alternates are tried in order (19.7).
func revealGroups(us []SecretUse) [][]reveal.Target {
	var out [][]reveal.Target
	for _, u := range us {
		if u.Alt == 0 || len(out) == 0 {
			out = append(out, nil)
		}
		if u.Manager != "" {
			out[len(out)-1] = append(out[len(out)-1], reveal.Target{Ref: u.Ref, Manager: u.Manager, Loc: locatorOf(u)})
		}
	}
	return slices.DeleteFunc(out, func(g []reveal.Target) bool { return len(g) == 0 })
}

// holderOf resolves --to of move: an mch_/act_ id (label from the
// machines table when known), or a person.machine label of a known machine.
func (o ops) holderOf(ctx context.Context, q string) (id, label string, err error) {
	ms, err := liveRows(ctx, o.env, engine.ReadStoresOf(o.env), schema.TableMachines)
	if err != nil {
		return "", "", err
	}
	if strings.HasPrefix(q, machinePrefix) || strings.HasPrefix(q, actorPrefix) {
		for _, m := range ms {
			if mid := text(m.Row, schema.FieldMachineID); mid == q || m.Row.ID.String() == q {
				return q, text(m.Row, schema.FieldMachineLabel), nil
			}
		}
		return q, "", nil
	}
	_, mlabel, hasMachine := strings.Cut(q, handleSep)
	for _, m := range ms {
		if hasMachine && text(m.Row, schema.FieldMachineLabel) == mlabel {
			mid := text(m.Row, schema.FieldMachineID)
			if mid == "" {
				mid = m.Row.ID.String()
			}
			return mid, q, nil
		}
	}
	return "", "", errs.New(errs.NotFound, "no machine labelled "+quote(q)+" in the attached stores").
		WithHint("give the holder id (mch_… or act_…), or set the machine label with lore whoami --machine-label")
}

// rewriteAll applies fix to every row holding a matching reference (text
// fields, resource secret_ref fields, secret_ref variables) in one batch.
func (o ops) rewriteAll(ctx context.Context, rows []secretRow, fix rewriter) (int, []string, error) {
	total, changed := 0, []string{}
	for _, r := range rows {
		n, err := o.rewriteRow(ctx, r, fix)
		if err != nil {
			return total, changed, err
		}
		if n > 0 {
			total += n
			changed = append(changed, r.Row.ID.String())
		}
	}
	return total, changed, nil
}

// rewriteRow writes one row's rewritten references through the engine.
func (o ops) rewriteRow(ctx context.Context, r secretRow, fix rewriter) (int, error) {
	ref := engine.Ref{Store: r.Src, Table: r.Row.Table, ID: r.Row.ID}
	switch {
	case r.Row.Table == schema.TableResources:
		return o.rewriteResource(ctx, r, fix)
	case r.Row.Table == schema.TableVars && text(r.Row, vType) == varTypeSecretRef:
		nv, n := bareRewrite(display(fromJSON(r.Row.Fields[vValue])), fix)
		if n == 0 {
			return 0, nil
		}
		tv, err := txt(nv)
		if err != nil {
			return 0, err
		}
		jv, err := asJSON(tv)
		if err != nil {
			return 0, err
		}
		_, err = o.env.Update(ctx, ref, map[string]value.Value{vValue: jv})
		return n, err
	}
	patch, n, err := rowPatch(r.Row, fix)
	if err != nil {
		return 0, err
	}
	mw, mn, err := metaRewrite(r.Row, fix) // later 508: references in metadata values (secrets_meta.go)
	if err != nil || n+mn == 0 {
		return 0, err
	}
	if mn > 0 {
		ctx = engine.WithMeta(ctx, mw)
	}
	_, err = o.env.Update(ctx, ref, patch)
	return n + mn, err
}

// bareRewrite applies fix to a bare manager://… value.
func bareRewrite(raw string, fix rewriter) (string, int) {
	s, n := rewriteText(secretOpen+raw+secretClose, fix)
	return strings.TrimSuffix(strings.TrimPrefix(s, secretOpen), secretClose), n
}

// rewriteResource rewrites a resource's secret_ref values and the
// ${secret:…} references inside its text values.
func (o ops) rewriteResource(ctx context.Context, r secretRow, fix rewriter) (int, error) {
	stored, err := decodeValues(r.located)
	if err != nil {
		return 0, err
	}
	// valuesPatch writes the whole value set, as resource edit passes it:
	// starting from an empty map wiped every field this rewrite did not
	// touch (the provider, the ip …) from the resource
	vals, total := maps.Clone(stored.vals), 0
	for id, raw := range stored.vals {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		var ns string
		var n int
		if _, ok := bareUse(SecretUse{}, "", s); ok {
			ns, n = bareRewrite(s, fix)
		} else {
			ns, n = rewriteText(s, fix) // ${secret:…} inside a text value
		}
		if n == 0 {
			continue
		}
		b, merr := json.Marshal(ns)
		if merr != nil {
			return 0, errs.Wrap(errs.Internal, "secrets: encode a resource value", merr)
		}
		vals[id], total = b, total+n
	}
	if total == 0 {
		return 0, nil
	}
	d, err := o.kindDef(ctx, text(r.Row, rKind))
	if err != nil {
		return 0, err
	}
	patch, err := o.valuesPatch(ctx, d, vals, stored)
	if err != nil {
		return 0, err
	}
	_, err = o.env.Update(ctx, engine.Ref{Store: r.Src, Table: r.Row.Table, ID: r.Row.ID}, patch)
	return total, err
}

// keepRows keeps the rows with at least one use matching keep.
func keepRows(rows []secretRow, keep func(SecretUse) bool) []secretRow {
	var out []secretRow
	for _, r := range rows {
		if slices.ContainsFunc(r.uses, keep) {
			out = append(out, r)
		}
	}
	return out
}

// secretsMove is `lore secrets move` (15): rewrite holder id and label of
// every matching reference in one batch (asks; E_NEEDS_CONFIRM without a
// terminal), audit secret.refs_moved, then add the rotate task.
func (o ops) secretsMove(ctx context.Context, in SecretsMoveIn) (SecretsBatchOut, error) {
	if in.From == "" || in.To == "" {
		return SecretsBatchOut{}, errs.New(errs.InvalidInput, "give --from and --to")
	}
	toID, toLabel, err := o.holderOf(ctx, in.To)
	if err != nil {
		return SecretsBatchOut{}, err
	}
	match := func(t refs.SecretTarget) bool {
		u := useOf(SecretUse{}, "", t)
		if t.Fixed != "" || !holderMatch(u, in.From) || u.Holder == "" && u.Label == "" || (in.Manager != "" && t.Manager != in.Manager) {
			return false
		}
		if in.Entry != "" {
			ok, perr := path.Match(in.Entry, t.Locator)
			return perr == nil && ok
		}
		return true
	}
	all, err := o.secretIndex(ctx)
	if err != nil {
		return SecretsBatchOut{}, err
	}
	rows := keepRows(all, func(u SecretUse) bool {
		return u.Manager != "" && holderMatch(u, in.From) && (u.Holder != "" || u.Label != "") && (in.Manager == "" || u.Manager == in.Manager) &&
			(in.Entry == "" || globOK(in.Entry, u.Locator))
	})
	n := 0
	for _, r := range rows {
		for _, u := range r.uses {
			if u.Manager != "" && holderMatch(u, in.From) {
				n++
			}
		}
	}
	if n == 0 {
		return SecretsBatchOut{}, errs.New(errs.NotFound, "no reference held by "+quote(in.From)).
			WithHint("lore secrets list --json shows each reference's holder")
	}
	if err = bulk.Ask(ctx, fmt.Sprintf("Moving %s from %s to %s. This changes the holder only; it does not copy any secret. Continue?", plural.Count(n, "reference"), in.From, in.To)); err != nil {
		return SecretsBatchOut{}, err
	}
	ctx, bat, err := bulk.Begin(ctx, bulk.KindSecrets)
	if err != nil {
		return SecretsBatchOut{}, err
	}
	total, changed, err := o.rewriteAll(ctx, rows, func(t refs.SecretTarget) (refs.SecretTarget, bool) {
		if !match(t) {
			return t, false
		}
		return setOption(setOption(t, refs.OptHolder, toID), refs.OptLabel, toLabel), true
	})
	if err != nil {
		return SecretsBatchOut{}, err
	}
	if err = o.auditBatch(ctx, engine.ActionVaultRefsMoved, total, in.From, toLabel+" ("+toID+")"); err != nil {
		return SecretsBatchOut{}, err
	}
	out := SecretsBatchOut{Batch: bat.String(), Count: total, Rows: changed}
	out.Human = fmt.Sprintf("Moved %s in %s (batch %s).", plural.Count(total, "reference"), plural.Count(len(changed), "row"), bat)
	if we, ok := o.env.(work.Env); ok {
		// one rotate task per store, beside its rows (a task never points
		// across stores: F15).
		byStore := map[store.Source][]string{}
		var order []store.Source
		for _, r := range rows {
			if slices.Contains(changed, r.Row.ID.String()) {
				if _, seen := byStore[r.Src]; !seen {
					order = append(order, r.Src)
				}
				byStore[r.Src] = append(byStore[r.Src], r.Row.ID.String())
			}
		}
		for _, src := range order {
			tid, _, terr := task.AddSecretTask(ctx, we, task.SecretTask{Store: src, Title: fmt.Sprintf(rotateTitle, in.From, total),
				Body: "lore secrets move " + bat.String() + " moved " + plural.Count(total, "reference") + " from " + in.From +
					". They had the values: rotate each entry, then close this task.", Key: "secret-rotate-" + bat.String(), Rows: byStore[src]})
			if terr != nil {
				return SecretsBatchOut{}, terr
			}
			out.Task = tid
			out.Human += " Added task " + tid + " (tasklist " + task.SecretsTasklist + ", " + string(src) + ")."
		}
	}
	out.Human += "\n"
	return out, nil
}

func globOK(glob, s string) bool {
	ok, err := path.Match(glob, s)
	return err == nil && ok
}

// auditBatch records one bulk secret.* event (16: batch id, counts, from
// and to; never a value).
func (o ops) auditBatch(ctx context.Context, action engine.Action, n int, from, to string) error {
	a, ok := o.env.(SecretAuditor)
	if !ok {
		return nil // the rows themselves are audited by the engine
	}
	return a.AuditSecret(ctx, action, engine.SecretAudit{Command: strings.ReplaceAll(string(action), ".", " "), Count: n, From: from, To: to})
}

// secretsRelabel is `lore secrets relabel` (5.2): each machine holder's
// label becomes "<handle>.<current machine label>" (the handle part kept),
// and each actor holder's label becomes the actor's current handle (OV13b).
func (o ops) secretsRelabel(ctx context.Context, in SecretsRelabelIn) (SecretsBatchOut, error) {
	if in.Label != "" {
		return o.secretsSetLabel(ctx, in)
	}
	ms, err := liveRows(ctx, o.env, engine.ReadStoresOf(o.env), schema.TableMachines)
	if err != nil {
		return SecretsBatchOut{}, err
	}
	labels := map[string]string{}
	for _, m := range ms {
		if l := text(m.Row, schema.FieldMachineLabel); l != "" {
			labels[m.Row.ID.String()] = l
			if mid := text(m.Row, schema.FieldMachineID); mid != "" {
				labels[mid] = l
			}
		}
	}
	handles, err := actorHandles(ctx, o.env)
	if err != nil {
		return SecretsBatchOut{}, err
	}
	fix := func(t refs.SecretTarget) (refs.SecretTarget, bool) {
		h := targetOption(t, refs.OptHolder)
		if hd, isActor := handles[h]; isActor && t.Fixed == "" && (in.Holder == "" || h == in.Holder) {
			if targetOption(t, refs.OptLabel) == hd {
				return t, false
			}
			return setOption(t, refs.OptLabel, hd), true // an actor holder's label is its handle (5.2)
		}
		ml, known := labels[h]
		if t.Fixed != "" || h == "" || !known || (in.Holder != "" && h != in.Holder) {
			return t, false
		}
		old := targetOption(t, refs.OptLabel)
		handle, _, hasHandle := strings.Cut(old, handleSep)
		want := ml
		if hasHandle {
			want = handle + handleSep + ml
		}
		if want == old {
			return t, false
		}
		return setOption(t, refs.OptLabel, want), true
	}
	return o.batchRewrite(ctx, engine.ActionSecretRelabeled, fix, "Relabelled", in.Holder, "")
}

// actorHandles maps each live actor id to its handle (actors without one
// are left out, so their references keep the label they have).
func actorHandles(ctx context.Context, env Env) (map[string]string, error) {
	as, err := liveRows(ctx, env, engine.ReadStoresOf(env), schema.TableActors)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, a := range as {
		if hd := text(a.Row, fieldActorHandle); hd != "" {
			out[a.Row.ID.String()] = hd
		}
	}
	return out, nil
}

// fieldActorHandle is the actors table's handle field (schema.Builtin).
const fieldActorHandle = "handle"

// secretsSetLabel is `lore secrets relabel --holder <id> --label <text>`
// (CLI_REFERENCE 3.13): count the references of that holder id ─► none is
// E_NOT_FOUND ─► ask (E_NEEDS_CONFIRM, --yes) ─► one audited batch.
func (o ops) secretsSetLabel(ctx context.Context, in SecretsRelabelIn) (SecretsBatchOut, error) {
	if in.Holder == "" {
		return SecretsBatchOut{}, errs.New(errs.InvalidInput, "--label needs --holder <id>: the label is set on that holder's references")
	}
	if strings.ContainsFunc(in.Label, unicode.IsControl) || strings.TrimSpace(in.Label) != in.Label {
		return SecretsBatchOut{}, errs.New(errs.InvalidInput, "--label must be one line with no leading or trailing spaces")
	}
	all, err := o.secretIndex(ctx)
	if err != nil {
		return SecretsBatchOut{}, err
	}
	n := 0
	for _, r := range all {
		for _, u := range r.uses {
			if u.Manager != "" && u.Holder == in.Holder && u.Label != in.Label {
				n++
			}
		}
	}
	if n == 0 {
		return SecretsBatchOut{}, errs.New(errs.NotFound, "no reference of holder "+quote(in.Holder)+" needs the label "+quote(in.Label)).
			WithHint("the full holder id (mch_… or act_…) from `lore secrets list`")
	}
	if err = bulk.Ask(ctx, fmt.Sprintf("Set the label %s on %s held by %s? Only the label changes; the holder id stays.", quote(in.Label), plural.Count(n, "reference"), in.Holder)); err != nil {
		return SecretsBatchOut{}, err
	}
	fix := func(t refs.SecretTarget) (refs.SecretTarget, bool) {
		if t.Fixed != "" || targetOption(t, refs.OptHolder) != in.Holder || targetOption(t, refs.OptLabel) == in.Label {
			return t, false
		}
		return setOption(t, refs.OptLabel, in.Label), true
	}
	return o.batchRewrite(ctx, engine.ActionSecretRelabeled, fix, "Relabelled", in.Holder, in.Label)
}

// secretsRename is `lore secrets rename-entry` (19.5).
func (o ops) secretsRename(ctx context.Context, in SecretsRenameIn) (SecretsBatchOut, error) {
	if in.Manager == "" || in.From == "" || in.To == "" {
		return SecretsBatchOut{}, errs.New(errs.InvalidInput, "give --manager, --from and --to")
	}
	set, err := o.managers(ctx)
	if err != nil {
		return SecretsBatchOut{}, err
	}
	if m, ok := set.Get(in.Manager); !ok {
		return SecretsBatchOut{}, errs.New(errs.RefScheme, "no secret manager "+quote(in.Manager)).
			WithHint("--manager is the scheme before :// in the reference, as lore secrets list prints it")
	} else if err := m.ValidateLocator(secretmgr.Locator{Path: in.To}); err != nil && in.Manager != secretmgr.Vault {
		return SecretsBatchOut{}, err
	}
	// The new name must read back as a reference: a locator the reference
	// syntax refuses (a space, "..") was written escaped, no longer parsed
	// as a secret reference, and every renamed reference fell out of
	// `lore secrets list`, lint and check.
	written := "${secret:" + targetText(refs.SecretTarget{Manager: in.Manager, Locator: strings.Trim(in.To, "/")}) + "}"
	if err := refs.Validate(written); err != nil {
		why := err.Error()
		var e *errs.Error
		if errors.As(err, &e) {
			why = e.Message
		}
		return SecretsBatchOut{}, errs.New(errs.RefSyntax, "secrets rename-entry: --to "+quote(in.To)+" is not an entry name a reference can hold ("+why+")").
			WithHint("give the entry name as the vault spells it, without spaces; nothing written")
	}
	fix := func(t refs.SecretTarget) (refs.SecretTarget, bool) {
		if t.Manager != in.Manager || strings.Trim(t.Locator, "/") != strings.Trim(in.From, "/") {
			return t, false
		}
		t.Locator = strings.Trim(in.To, "/")
		return t, true
	}
	return o.batchRewrite(ctx, engine.ActionSecretEntryRenamed, fix, "Renamed", in.Manager+"://"+in.From, in.Manager+"://"+in.To)
}

// batchRewrite runs fix over the index in one batch and audits it.
func (o ops) batchRewrite(ctx context.Context, action engine.Action, fix rewriter, verb, from, to string) (SecretsBatchOut, error) {
	all, err := o.secretIndex(ctx)
	if err != nil {
		return SecretsBatchOut{}, err
	}
	ctx, bat, err := bulk.Begin(ctx, bulk.KindSecrets)
	if err != nil {
		return SecretsBatchOut{}, err
	}
	total, changed, err := o.rewriteAll(ctx, all, fix)
	if err != nil {
		return SecretsBatchOut{}, err
	}
	out := SecretsBatchOut{Count: total, Rows: changed}
	if total == 0 {
		out.Human = "Nothing to change.\n"
		return out, nil
	}
	if err = o.auditBatch(ctx, action, total, from, to); err != nil {
		return SecretsBatchOut{}, err
	}
	out.Batch = bat.String()
	out.Human = fmt.Sprintf("%s %s in %s (batch %s).\n", verb, plural.Count(total, "reference"), plural.Count(len(changed), "row"), bat)
	return out, nil
}

// secretsLink is `lore secrets link <row> --at N --ref REF` (7): the Nth
// ${secret:unstored} of the row becomes the given entry (holder filled in
// when known). The task stays open: only rotation ends the exposure.
func (o ops) secretsLink(ctx context.Context, in SecretsLinkIn) (SecretsBatchOut, error) {
	if in.At < 1 {
		return SecretsBatchOut{}, errs.New(errs.InvalidInput, "--at is the 1-based placeholder number (lore secrets list --unstored)")
	}
	parts := refs.Parse(secretOpen + strings.TrimSuffix(strings.TrimPrefix(in.Ref, secretOpen), secretClose) + secretClose)
	if len(parts) != 1 || parts[0].Kind != refs.PartRef || parts[0].Ref.Problem != "" || len(parts[0].Ref.Alternates) == 0 {
		if err := refs.Validate(secretOpen + in.Ref + secretClose); err != nil {
			return SecretsBatchOut{}, err
		}
		return SecretsBatchOut{}, errs.New(errs.RefSyntax, "--ref takes manager://entry")
	}
	newT := parts[0].Ref.Alternates[0]
	if newT.Fixed != "" {
		return SecretsBatchOut{}, errs.New(errs.RefSyntax, "--ref takes manager://entry, not "+newT.Fixed)
	}
	set, err := o.managers(ctx)
	if err != nil {
		return SecretsBatchOut{}, err
	}
	if m, ok := set.Get(newT.Manager); ok {
		if err = m.ValidateLocator(secretmgr.Locator{Path: newT.Locator, Fragment: newT.Fragment}); err != nil {
			return SecretsBatchOut{}, err
		}
	}
	if targetOption(newT, refs.OptHolder) == "" {
		hid, hlabel := in.Holder, ""
		if hid == "" && !sharedManagers[newT.Manager] { // SEC-LINK1: this machine through the Env
			if h, ok := o.env.(Holderer); ok {
				var herr error
				if hid, hlabel, herr = h.ThisHolder(ctx); herr != nil {
					return SecretsBatchOut{}, herr
				}
			}
		}
		newT = setOption(setOption(newT, refs.OptHolder, hid), refs.OptLabel, hlabel)
	}
	rows, err := o.secretIndex(ctx)
	if err != nil {
		return SecretsBatchOut{}, err
	}
	var target *secretRow
	for i := range rows {
		if rows[i].Row.ID.String() == in.Row {
			target = &rows[i]
		}
	}
	if target == nil {
		return SecretsBatchOut{}, errs.New(errs.NotFound, "no ${secret:unstored} in row "+quote(in.Row))
	}
	field := ""
	for _, u := range target.uses {
		if u.At == in.At {
			field = u.Field
		}
	}
	if field == "" {
		return SecretsBatchOut{}, errs.New(errs.NotFound, "row "+in.Row+" has no unstored placeholder #"+strconv.Itoa(in.At))
	}
	// position of the placeholder inside its field: count the earlier ones.
	before := 0
	for _, u := range target.uses {
		if u.At > 0 && u.At < in.At && u.Field == field {
			before++
		}
	}
	seen := 0
	fix := func(t refs.SecretTarget) (refs.SecretTarget, bool) {
		if t.Fixed != refs.TargetUnstored {
			return t, false
		}
		seen++
		if seen != before+1 {
			return t, false
		}
		return newT, true
	}
	only := secretRow{located: target.located, uses: target.uses}
	patch := map[string]value.Value{}
	wctx := ctx
	n := 0
	if name, isMeta := strings.CutPrefix(field, metaFieldPrefix); isMeta {
		// a placeholder in a metadata value (G-META-11): only that key's
		// entry is rewritten, set back by its key id
		only.Row.Meta = nil
		for kid, j := range target.Row.Meta {
			if en, derr := meta.DecodeEntry(j); derr == nil && en.Name == name {
				only.Row.Meta = map[ids.ID[ids.MetaKey]]value.JSON{kid: j}
			}
		}
		var mw engine.MetaWrite
		if mw, n, err = metaRewrite(only.Row, fix); err != nil {
			return SecretsBatchOut{}, err
		}
		wctx = engine.WithMeta(ctx, mw)
	} else {
		only.Row.Fields = map[string]value.Value{field: target.Row.Fields[field]}
		if patch, n, err = rowPatch(only.Row, fix); err != nil {
			return SecretsBatchOut{}, err
		}
	}
	if n != 1 {
		return SecretsBatchOut{}, errs.New(errs.Internal, "secrets link: placeholder not found in "+field)
	}
	if _, err = o.env.Update(wctx, engine.Ref{Store: target.Src, Table: target.Row.Table, ID: target.Row.ID}, patch); err != nil {
		return SecretsBatchOut{}, err
	}
	if err = o.auditBatch(ctx, engine.ActionSecretLinked, 1, refs.TargetUnstored, targetText(newT)); err != nil {
		return SecretsBatchOut{}, err
	}
	return SecretsBatchOut{Count: 1, Rows: []string{in.Row},
		Human: fmt.Sprintf("Replaced ${secret:unstored} #%d in %s %s with ${secret:%s}.\nIts task stays open: rotate the secret, then lore task done <task>.\n",
			in.At, in.Row, field, targetText(newT))}, nil
}
