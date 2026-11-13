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
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/policy"
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
