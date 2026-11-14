package wire

// leftgate.go — a person the org removed (`lore member remove`, `lore
// actor offboard`: their actor row says left or archived) no longer
// writes to the org's stores or its repos (B2-12). Before this, their
// lore kept writing and pushing with no word; only `lore doctor --all`
// said "no open period".
//
//	open ──▶ caches refreshed ──▶ canonicalActor (the caller's actor id)
//	     ──▶ refuseWritesIfLeft: the caller's actor row in the org folder
//	         store (the roster, Level org) says left|archived?
//	           yes ─▶ every store but the personal ones (and the machine
//	                  store, never attached here) refuses writes with
//	                  E_REFUSED naming the org, the day and the fix
//	           no  ─▶ nothing changes
//
// Reads keep working, and so does the personal store: the person's own
// notes are theirs. The roster on this machine is the one last synced, so
// the refusal starts at the first command after `lore sync` brings the
// removal, and ends the same way after an admin restores them.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// refuseWritesIfLeft marks the org's stores and the repo store
// write-refused when the caller's actor row on the org roster is left or
// archived. Best effort on reads: a roster that cannot be read here
// refuses nothing (doctor reports the read).
func (st *state) refuseWritesIfLeft(ctx context.Context, home string, attached []attach.Opened) {
	if st.eng == nil || st.actor.IsZero() {
		return
	}
	var status, leftAt string
	found := false
	for _, o := range attached {
		if o.Level != policy.LevelOrg {
			continue
		}
		_, r, err := st.eng.Get(ctx, engine.Ref{Store: o.Name, Table: schema.TableActors, ID: st.actor.Any()})
		if err != nil || r.Deleted {
			continue
		}
		status, leftAt, found = fieldText(r, schema.FieldActorStatus), fieldText(r, schema.FieldActorLeftAt), true
		break
	}
	if !found {
		// B2-25: no org folder attached (a command run inside a store
		// clone's project folder attaches that folder alone): the roster
		// row is read from the org folder's clone on this machine.
		status, leftAt = rosterStatusOnDisk(home, st.who.Org, st.actor.String())
	}
	if !rosterGone(status) {
		return
	}
	refusal := leftRefusal(st.who.Org, status, leftAt)
	st.eng.RefuseWrites(store.SourceRepo, refusal)
	for _, o := range attached {
		if o.Level == policy.LevelPersonal {
			continue
		}
		st.eng.RefuseWrites(o.Name, refusal)
	}
}

// armLeftHold gives every store clone of the org (all but the personal
// stores) a push hold read from the roster at push time (F-9): the
// refusal above starts with the command after the sync that brings the
// removal, so without it that very sync pushed what the person wrote
// after the removal (a store write committed locally, waiting for lore
// sync). The roster is read again right before each push, after the
// fetch merged the remote, so the sync that brings the removal holds.
func (st *state) armLeftHold(home string, attached []attach.Opened) {
	if st.actor.IsZero() || st.who.Org == "" {
		return
	}
	hold := leftPushHold(home, st.who.Org, st.actor.String())
	for _, o := range attached {
		if o.Level == policy.LevelPersonal {
			continue
		}
		if sp := storePushOf(o.Backend); sp != nil {
			sp.hold = hold
		}
	}
}

// storePushOf finds the storePush wrapper in b's chain (nil = none: a
// store with no remote, or a repo store).
func storePushOf(b store.Backend) *storePush {
	for b != nil {
		if sp, ok := b.(*storePush); ok {
			return sp
		}
		u, ok := b.(Unwrapper)
		if !ok {
			return nil
		}
		b = u.Unwrap()
	}
	return nil
}

// leftPushHold is the push hold of a person (actor) of org: none while the
// roster on this machine says they are in the org; once it says left or
// archived, commits made after left_at are held with leftRefusal's text
// (an unreadable left_at holds every commit: fail closed).
func leftPushHold(home, org, actor string) gitbackend.PushHoldFunc {
	return func(context.Context) (gitbackend.PushHold, bool) {
		status, leftAt := rosterStatusOnDisk(home, org, actor)
		if !rosterGone(status) {
			return gitbackend.PushHold{}, false
		}
		return gitbackend.PushHold{After: parseLeftAt(leftAt), Reason: leftPushReason(org, status, leftAt)}, true
	}
}

// LivePushHold is LiveConfig.PushHold (cmd/lore): the hold of the
// caller on the repo's refs/lore/live push, the same rule as the org's
// store clones (armLeftHold); ok false when the caller is in the org,
// has no org here, or the command never opened the App (it then wrote
// nothing; opening it at the push only to check would cost a whole open).
func (a *App) LivePushHold(ctx context.Context) (after time.Time, reason string, ok bool) {
	a.mu.Lock()
	st := a.st
	a.mu.Unlock()
	if st == nil || st.actor.IsZero() || st.who.Org == "" {
		return time.Time{}, "", false
	}
	h, ok := leftPushHold(a.opts.Home, st.who.Org, st.actor.String())(ctx)
	return h.After, h.Reason, ok
}

// leftAtLayouts are the spellings of an actor's left_at.
var leftAtLayouts = []string{time.RFC3339Nano, time.RFC3339, time.DateOnly}

// parseLeftAt reads left_at; the zero time when it cannot be read, which
// holds every commit.
func parseLeftAt(s string) time.Time {
	for _, l := range leftAtLayouts {
		if t, err := time.Parse(l, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Time{}
}

// leftPushReason is the one line a held push says: who is marked as what
// in which org since when, and that writes made after it stay unsent.
func leftPushReason(org, status, leftAt string) string {
	since := ""
	if day, _, _ := strings.Cut(leftAt, "T"); day != "" {
		since = " since " + day
	}
	return "you are marked as " + status + " in " + org + since + ": writes made after that are not sent to its stores or repos (ask an admin of " + org + " to run lore member add --email <your-email> if you are back)"
}

// leftRefusal is the E_REFUSED every write of a removed person gets: the
// org, since when, and what to do.
func leftRefusal(org, status, leftAt string) error {
	if org == "" {
		org = "this org"
	}
	since := ""
	if day, _, ok := strings.Cut(leftAt, "T"); ok || day != "" {
		since = " since " + day
	}
	return errs.New(errs.Refused, "you are marked as "+status+" in "+org+since+"; writes to its stores and repos are refused (your personal store still works)").
		WithHint("if you are back, ask an admin of " + org + " to run lore member add --email <your-email>")
}

// orgRosterClone is the folder of org's roster (org folder) inside this
// machine's clone of its store, and the clone's top; ok false when the
// org or its roster store is not known here.
func orgRosterClone(home, org string) (top, folder string, ok bool) {
	if org == "" {
		return "", "", false
	}
	one, found, err := orgFolderStore(home, org)
	if err != nil || !found {
		return "", "", false
	}
	r, err := gitbackend.RemoteOfRaw(one.URL)
	if err != nil {
		return "", "", false
	}
	dir, _, err := gitbackend.CloneDirFor(home, r, one.Branch)
	if err != nil {
		return "", "", false
	}
	return dir, filepath.Join(dir, filepath.FromSlash(one.Folder)), true
}

// rosterCloneStamp names the commit org's roster clone is at ("" when
// unknown): doctor's result cache keys on it (B2-24), so a sync that brings
// a removal or a restore re-probes identity.period at once instead of
// serving the answer cached before it.
func rosterCloneStamp(home, org string) string {
	top, _, ok := orgRosterClone(home, org)
	if !ok {
		return ""
	}
	gitDir := filepath.Join(top, ".git")
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	ref, isRef := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: ")
	if !isRef {
		return ref // a detached HEAD names its commit
	}
	if b, rerr := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(ref))); rerr == nil {
		return strings.TrimSpace(string(b))
	}
	if fi, serr := os.Stat(filepath.Join(gitDir, "packed-refs")); serr == nil {
		return ref + "@" + fi.ModTime().String() // a packed ref: the file changes with it
	}
	return ""
}

// rosterStatusOnDisk is the status and left_at of actor's row in org's
// roster as this machine's clone of the org folder holds it (as last
// synced); "" when the org, its clone or the row is not here. It reads
// one file and never touches the network.
func rosterStatusOnDisk(home, org, actor string) (status, leftAt string) {
	_, folder, ok := orgRosterClone(home, org)
	if !ok {
		return "", ""
	}
	b, err := os.ReadFile(filepath.Join(folder, schema.TableActors, actor+".json"))
	if err != nil {
		return "", ""
	}
	var doc struct {
		Status string `json:"status"`
		LeftAt string `json:"left_at"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return "", ""
	}
	return doc.Status, doc.LeftAt
}

// urlPushHold is the push hold of a store clone pushed without an
// attached open: `lore store push` and the org-store pass of `lore sync`
// (pushWaitingOrgStores). armLeftHold arms only the stores a repo
// attaches, so those two paths pushed what a removed person wrote after
// their removal (D-85, gate7b). Every org of ~/.lore/config.yaml that
// lists raw holds with its own roster and the caller's identity in that
// org (its org entry, resolved as an open would); the first org that
// holds wins. nil when no org lists raw.
func (a *App) urlPushHold(ctx context.Context, raw string) gitbackend.PushHoldFunc {
	if a.opts.Home == "" {
		return nil
	}
	orgs := orgsListingStore(a.opts.Home, raw)
	if len(orgs) == 0 {
		return nil
	}
	mc, err := readMachineConfig(a.opts.Home)
	if err != nil {
		return nil
	}
	var holds []gitbackend.PushHoldFunc
	for _, org := range orgs {
		entry := identity.Org{Name: org}
		if i := slices.IndexFunc(mc.Orgs, func(m identity.Org) bool { return m.Name == org }); i >= 0 {
			entry = mc.Orgs[i]
		}
		opts := a.opts
		opts.Org = func(context.Context) (*identity.Org, error) { return &entry, nil }
		who, rerr := resolveIdentity(ctx, opts)
		if rerr != nil {
			continue // no identity: nothing on the roster can name this caller
		}
		actor, aerr := who.Person().ActorID() // the roster lists the person, also when their agent pushes
		if aerr != nil || actor.IsZero() {
			continue
		}
		holds = append(holds, leftPushHold(a.opts.Home, org, actor.String()))
	}
	if len(holds) == 0 {
		return nil
	}
	return func(ctx context.Context) (gitbackend.PushHold, bool) {
		for _, h := range holds {
			if hold, ok := h(ctx); ok {
				return hold, true
			}
		}
		return gitbackend.PushHold{}, false
	}
}

// orgsListingStore names the orgs of home's config.yaml whose entry lists
// the store at raw (urls compared normalised), in config order: the orgs
// whose roster can hold a push of that clone. Another org's store or a
// personal store is listed by none; a config that does not load lists
// nothing (`lore config check` names it).
func orgsListingStore(home, raw string) []string {
	norm, err := repourl.NormalizeStore(raw)
	if err != nil {
		return nil
	}
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil {
		return nil
	}
	var out []string
	for _, o := range cfg.Orgs {
		if slices.ContainsFunc(o.Stores, func(s orgconf.Store) bool {
			n, nerr := repourl.NormalizeStore(s.URL)
			return nerr == nil && n == norm
		}) {
			out = append(out, o.Name)
		}
	}
	return out
}
