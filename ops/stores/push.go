package stores

// push.go — `lore store set` and `lore store compact`, the store push
// modes and history compaction (owner decision 2026-11-09). The rules are
// logic/storepush; where the values live and who may change them is the
// composition root's (Pusher, wire/storepushmode.go):
//
//	store.set <store> k=v…   parse ─► Pusher.SetStorePush (me: config.yaml;
//	                         an org store: its lore-store.yaml, admin only;
//	                         a stores.yaml store: its lore-store.yaml;
//	                         repo and folder stores: E_REFUSED)
//	store.compact <store>    Pusher.CompactPlan ─► nothing to do: done
//	                         ─► not confirmed: E_NEEDS_CONFIRM (--yes)
//	                         ─► Pusher.CompactStore (rewrite, epoch +1, push)
//	store.show <store>       Pusher.StorePush (the settings and the clone)

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// Operation names.
const (
	OpSet     = "store.set"
	OpCompact = "store.compact"
)

// Where a store's push settings live (PushView.Place).
const (
	PlacePersonal = "personal" // ~/.lore/config.yaml personal:
	PlaceOrg      = "org"      // the store's lore-store.yaml, set by an org admin
	PlaceStore    = "store"    // a .lore/stores.yaml store's lore-store.yaml, set by whoever can push to it
)

// Pusher is the optional Manager upgrade behind the push-mode commands.
type Pusher interface {
	// StorePush reads one store's settings and push state.
	StorePush(ctx context.Context, name string) (PushView, error)
	// SetStorePush applies the assignments (already parsed and checked)
	// where the store's settings live, refusing what may not be set.
	SetStorePush(ctx context.Context, name string, set []storepush.Assignment) (PushView, error)
	// CompactStore plans (dry run) or runs one compaction.
	CompactStore(ctx context.Context, in CompactIn) (CompactOut, error)
	// PushViews lists every store this repo (or machine) pushes to, for
	// `lore doctor` and `lore sync status`.
	PushViews(ctx context.Context) ([]PushView, error)
}

// PushView is one store's push and compaction settings and state.
type PushView struct {
	Store string `json:"store"`
	// Place is PlacePersonal, PlaceOrg or PlaceStore; Org names the org of
	// an org store.
	Place string `json:"place"`
	Org   string `json:"org,omitempty"`
	// Push, PushEvery, Compact, CompactAfter are the effective settings.
	Push         string `json:"push"`
	PushEvery    string `json:"push_every"`
	Compact      string `json:"compact"`
	CompactAfter string `json:"compact_after,omitempty"`
	// Summary is the one-line form ("push batch every 10m, compact off").
	Summary string `json:"summary"`
	// Problem names a setting that does not read (the store pushes as now).
	Problem string `json:"problem,omitempty"`
	// Epoch is the store's compaction epoch; Unpushed the local commits
	// waiting for a push; LastPush the last push this machine made (RFC
	// 3339, "" = none recorded).
	Epoch    int    `json:"epoch"`
	Unpushed int    `json:"unpushed"`
	LastPush string `json:"last_push,omitempty"`
	// WaitingSince is when the oldest write not pushed yet was made (RFC
	// 3339, "" = nothing waits), WaitingFor its age ("3h", "2d"), and
	// Stale that it is older than a day (`lore doctor` warns).
	WaitingSince string `json:"waiting_since,omitempty"`
	WaitingFor   string `json:"waiting_for,omitempty"`
	Stale        bool   `json:"stale,omitempty"`
	// Admin: this machine may change an org store's settings.
	Admin bool `json:"admin,omitempty"`
	// Changed is set by store.set when a value changed.
	Changed bool `json:"changed,omitempty"`
	// URL is where the personal store lives (store show me); Folder that
	// it is a local folder store (file://), which has no remote to push to
	// and no history to compact, so the push settings do not apply.
	URL    string `json:"url,omitempty"`
	Folder bool   `json:"folder,omitempty"`
	// SharedBy names the setups whose personal store this is: a person has
	// one personal store `me`, shared by all their setups (decided
	// 2026-11-09; journey joiner).
	SharedBy []string `json:"shared_by,omitempty"`
}

// Text prints one store's settings.
func (v PushView) Text() string {
	var b strings.Builder
	if v.Folder {
		fmt.Fprintf(&b, "%s: a local folder store (no remote: push modes and compaction do not apply)\n", v.Store)
		v.personalLines(&b)
		return b.String()
	}
	fmt.Fprintf(&b, "%s: %s\n", v.Store, v.Summary)
	v.personalLines(&b)
	where := "~/.lore/config.yaml (personal:)"
	switch v.Place {
	case PlaceOrg:
		where = "the store's lore-store.yaml (org " + v.Org + "; set by an org admin"
		if v.Admin {
			where += ", which this machine is"
		}
		where += ")"
	case PlaceStore:
		where = "the store's lore-store.yaml (shared by every clone; anyone who can push to the store may change it)"
	}
	fmt.Fprintf(&b, "  set in    %s\n", where)
	if v.Problem != "" {
		fmt.Fprintf(&b, "  problem   %s (pushing as now until it is fixed)\n", v.Problem)
	}
	fmt.Fprintf(&b, "  epoch     %d\n", v.Epoch)
	if v.Unpushed > 0 {
		age := ""
		if v.WaitingFor != "" {
			age = ", the oldest " + v.WaitingFor + " old"
		}
		fmt.Fprintf(&b, "  waiting   %d local commit(s) not pushed yet%s (`lore sync` pushes them)\n", v.Unpushed, age)
	}
	if v.LastPush != "" {
		fmt.Fprintf(&b, "  pushed    %s\n", v.LastPush)
	}
	return b.String()
}

// personalLines are the personal store's "at" and "shared by" lines.
func (v PushView) personalLines(b *strings.Builder) {
	if v.URL != "" {
		fmt.Fprintf(b, "  at        %s\n", v.URL)
	}
	switch len(v.SharedBy) {
	case 0:
	case 1:
		fmt.Fprintf(b, "  used by   your setup %s (one personal store per person: any other setup of yours shares it)\n", v.SharedBy[0])
	default:
		fmt.Fprintf(b, "  shared by your setups %s (one personal store per person)\n", strings.Join(v.SharedBy, ", "))
	}
}

// SetIn is the input of store.set.
type SetIn struct {
	Store    string   `json:"store" arg:"0" desc:"the store: me (personal), an org's store (team, or its name), or a .lore/stores.yaml store"`
	Settings []string `json:"settings" arg:"1" rest:"true" desc:"key=value: push=now|batch|manual, push_every=5m, compact=on|off, compact_after=30d"`
}

// CompactIn is the input of store.compact.
type CompactIn struct {
	Store  string `json:"store" arg:"0" desc:"the store: me (personal), an org's store (team, or its name), or a .lore/stores.yaml store"`
	Before string `json:"before,omitempty" flag:"before" desc:"squash history older than this age (e.g. 30d; default: the store's compact_after, else 30d)"`
	DryRun bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"show what would be squashed; change nothing"`
}

// CompactOut is the output of store.compact.
type CompactOut struct {
	Store  string `json:"store"`
	DryRun bool   `json:"dry_run,omitempty"`
	// Before is the cutoff date (UTC, YYYY-MM-DD).
	Before string `json:"before"`
	// Nothing: no history older than the cutoff beyond one commit.
	Nothing   bool   `json:"nothing,omitempty"`
	Squashed  int    `json:"squashed"`
	Kept      int    `json:"kept"`
	Rows      int    `json:"rows"`
	FromEpoch int    `json:"from_epoch"`
	ToEpoch   int    `json:"to_epoch"`
	Backup    string `json:"backup,omitempty"`
	// Commit and Branch are where the compacted history was pushed (the
	// new tip's short id, the store branch); "" for a dry run or nothing.
	Commit string `json:"commit,omitempty"`
	Branch string `json:"branch,omitempty"`
}

// Text prints the compaction.
func (o CompactOut) Text() string {
	switch {
	case o.Nothing:
		return fmt.Sprintf("nothing to compact in %s: no history before %s beyond one commit\n", o.Store, o.Before)
	case o.DryRun:
		return fmt.Sprintf("would squash %d commit(s) of %s before %s into one snapshot, keep %d later commit(s) and every one of its %d row file(s); epoch %d → %d\n",
			o.Squashed, o.Store, o.Before, o.Kept, o.Rows, o.FromEpoch, o.ToEpoch)
	}
	pushed := ""
	if o.Commit != "" {
		pushed = fmt.Sprintf("  pushed store %s: commit %s to branch %s\n", o.Store, o.Commit, o.Branch)
	}
	return fmt.Sprintf("✓ compacted %s: %d commit(s) before %s are one snapshot, %d later commit(s) kept, all %d row file(s) kept; epoch %d → %d (old history kept locally in %s)\n",
		o.Store, o.Squashed, o.Before, o.Kept, o.Rows, o.FromEpoch, o.ToEpoch, o.Backup) + pushed
}

// DefaultCompactBefore is store.compact's cutoff when neither --before
// nor compact_after says one.
const DefaultCompactBefore = "30d"

// pushErrors are the codes of the push-mode commands.
var pushErrors = []errs.Code{errs.InvalidInput, errs.NotFound, errs.ConfigInvalid, errs.Refused, errs.StoreReadOnly,
	errs.NotAStore, errs.StoreUnreachable, errs.StoreScheme, errs.OldLore, errs.Offline, errs.Contended,
	errs.NeedsConfirm, errs.Cancelled, errs.Stale, errs.StoreLocked, errs.Unsupported, errs.Internal}

// pusher is m's Pusher, or E_UNSUPPORTED naming the command.
func pusher(m Manager, cmd string) (Pusher, error) {
	p, ok := upgrade.As[Pusher](m)
	if !ok {
		return nil, errs.New(errs.Unsupported, "store "+cmd+": this build has no store push modes")
	}
	return p, nil
}

// CheckSettings parses and checks store.set's words (exported for tests):
// E_INVALID_INPUT naming the word and the keys.
func CheckSettings(words []string) ([]storepush.Assignment, error) {
	set, err := storepush.ParseAssignments(words)
	if err != nil {
		return nil, errs.New(errs.InvalidInput, "store set: "+err.Error()).
			WithHint("e.g. lore store set me push=batch push_every=10m compact=on compact_after=30d")
	}
	s := storepush.Default()
	for _, a := range set {
		if s, err = s.Apply(a.Key, a.Value); err != nil {
			return nil, errs.New(errs.InvalidInput, "store set: "+err.Error()).
				WithHint("push=now|batch|manual, push_every=<interval ≥ 1m>, compact=on|off, compact_after=<age ≥ 1d>")
		}
	}
	return set, nil
}

// compactQuestion is what store.compact asks.
func compactQuestion(o CompactOut) string {
	return "rewrite the history of store " + o.Store + ": squash " + strconv.Itoa(o.Squashed) + " commit(s) before " + o.Before +
		" into one snapshot (every row is kept; every clone re-fetches at epoch " + strconv.Itoa(o.ToEpoch) + ")?"
}

// pushOps are the push-mode commands bound to m.
func pushOps(m Manager) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpSet, Summary: "Set a store's push mode and history compaction (me, an org store as its admin, or a .lore/stores.yaml store)",
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: pushErrors,
			Examples: []op.Example{
				{Title: "batch the personal store's pushes", CLI: "lore store set me push=batch push_every=10m", Want: "push batch every 10m"},
				{Title: "compact an org store's history older than 30 days", CLI: "lore store set team compact=on compact_after=30d", Want: "compact on after 30d"},
			},
		}, func(ctx context.Context, in SetIn) (PushView, error) {
			p, err := pusher(m, "set")
			if err != nil {
				return PushView{}, err
			}
			set, err := CheckSettings(in.Settings)
			if err != nil {
				return PushView{}, err
			}
			return p.SetStorePush(ctx, strings.TrimSpace(in.Store), set)
		}),
		op.Define(op.Spec{
			Name: OpCompact, Summary: "Squash a store's history older than an age into one snapshot commit (every row is kept; asks first)",
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: pushErrors,
			Confirm: op.ConfirmRule{MayAsk: true, PersonOnlyEligible: true},
			Examples: []op.Example{
				{Title: "see what would be squashed", CLI: "lore store compact me --before 30d --dry-run", Want: ""},
				{Title: "compact the personal store", CLI: "lore store compact me --yes", Want: ""},
			},
		}, func(ctx context.Context, in CompactIn) (CompactOut, error) {
			p, err := pusher(m, "compact")
			if err != nil {
				return CompactOut{}, err
			}
			in.Store = strings.TrimSpace(in.Store)
			if in.Before != "" {
				if _, perr := storepush.ParseAge(in.Before); perr != nil {
					return CompactOut{}, errs.New(errs.InvalidInput, "store compact: --before "+perr.Error()).WithHint("e.g. --before 30d")
				}
			}
			plan := in
			plan.DryRun = true
			out, err := p.CompactStore(ctx, plan)
			if err != nil || in.DryRun || out.Nothing {
				return out, err
			}
			if !engine.Confirmed(ctx) {
				q := compactQuestion(out)
				return CompactOut{}, errs.New(errs.NeedsConfirm, "store compact "+in.Store+": "+q).
					WithQuestion(q).WithHint("ask the person, then rerun with --yes")
			}
			return p.CompactStore(ctx, in)
		}),
	}
}

// OpPush is `lore store push`.
const OpPush = "store.push"

// PushIn is the input of store.push.
type PushIn struct {
	Store   string `json:"store" arg:"0" desc:"the store: a name this repo or machine uses (me, team, a .lore/stores.yaml store) or its url"`
	Restore bool   `json:"restore,omitempty" flag:"restore" desc:"put this machine's copy back into a store remote that was deleted and made again (lore found it reset)"`
	// Branch is the store's pinned branch (from its entry; "" = default).
	Branch string `json:"-"`
}

// PushOut is the output of store.push.
type PushOut struct {
	Store    string `json:"store"`
	URL      string `json:"url"`
	Restored bool   `json:"restored,omitempty"`
	Sent     int    `json:"sent"`
	Offline  bool   `json:"offline,omitempty"`
}

// Text prints the push.
func (o PushOut) Text() string {
	switch {
	case o.Offline:
		return "store " + o.Store + ": offline, nothing pushed; the local commits wait for the next sync\n"
	case o.Restored:
		return "✓ store " + o.Store + ": this machine's copy is back at " + o.URL + " (sent " + strconv.Itoa(o.Sent) + ")\n"
	}
	return "✓ store " + o.Store + ": sent " + strconv.Itoa(o.Sent) + "\n"
}

// StorePusherNow is the optional Manager upgrade behind store.push
// (B3-29): push one store's clone now, and with Restore first clear the
// mark of a remote lore found reset, so this copy goes back into it.
type StorePusherNow interface {
	PushStoreNow(ctx context.Context, in PushIn) (PushOut, error)
}

// pushNowOp is store.push bound to m.
func pushNowOp(m Manager) op.Entry {
	return op.Define(op.Spec{
		Name: OpPush, Summary: "Push a store's local commits now; --restore puts this machine's copy back into a store remote that was deleted and made again",
		Permission: op.PermAdmin, Access: op.AccessRead, Errors: pushErrors,
		// --restore writes every old row into the remote someone reset: it asks.
		Confirm: op.ConfirmRule{MayAsk: true, PersonOnlyEligible: true, NoSecretFindings: true},
		Examples: []op.Example{
			{Title: "push the personal store now", CLI: "lore store push me", Want: ""},
			{Title: "put the local copy back into a reset remote", CLI: "lore store push team --restore --yes", Want: ""},
		},
	}, func(ctx context.Context, in PushIn) (PushOut, error) {
		p, ok := upgrade.As[StorePusherNow](m)
		if !ok {
			return PushOut{}, errs.New(errs.Unsupported, "store push: this build cannot push a store on its own")
		}
		e, named, err := storeNamed(ctx, m, "push", in.Store)
		if err != nil {
			return PushOut{}, err
		}
		if in.Restore && !engine.Confirmed(ctx) {
			q := "put this machine's copy of store " + strings.TrimSpace(in.Store) + " back into its remote, which was deleted and made again (every old row returns there)?"
			return PushOut{}, errs.New(errs.NeedsConfirm, "store push --restore: "+q).WithQuestion(q).
				WithHint("ask the person, then rerun with --yes")
		}
		out := PushOut{Store: strings.TrimSpace(in.Store)}
		if named {
			in.Store, in.Branch = e.URL, e.Branch
		}
		res, err := p.PushStoreNow(ctx, in)
		res.Store = out.Store
		return res, err
	})
}
