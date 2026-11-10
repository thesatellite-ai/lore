package wire

// config_setuptrash.go — trash.setups of ~/.lore/config.yaml (owner
// decision 2026-11-06: remove = move to the trash, restorable):
//
//	TrashSetup   setups[i] ─▶ trash.setups (+ removed_at, repo_pins; its repo_setups pins dropped)
//	RestoreSetup trash.setups (newest of that name) ─▶ setups[] (pins back where still free)
//	PurgeSetup   trash.setups entry ─▶ gone; optionally the top-level identity / personal.store
//
// Every edit works on the node tree (editSetups), so the entry's unknown
// keys travel with it byte for byte, and the candidate file is validated
// before anything is written. Nothing here touches a store or a repo.

import (
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/yamlfmt"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Config keys of the trash block and a trashed entry's own keys.
const (
	configKeyTrash      = "trash"
	configKeyRemovedAt  = "removed_at"
	configKeyRepoPins   = "repo_pins"
	configKeyDisabled   = "disabled"
	configKeyDisabledAt = "disabled_at"
	// configKeyPurged is trash.purged: one onboard.PurgedSetup per
	// permanently deleted setup, so the import scan never offers it again.
	configKeyPurged = "purged"
	// configKeyIdentityKey is identity.key (the tombstone fingerprints it).
	configKeyIdentityKey = "key"
)

// trashSeq is the trash.setups sequence node, created when absent.
func trashSeq(root *yaml.Node) *yaml.Node {
	t := mapChild(root, configKeyTrash)
	if s := mapGet(t, configKeySetups); s != nil && s.Kind == yaml.SequenceNode {
		return s
	}
	s := &yaml.Node{Kind: yaml.SequenceNode}
	mapSet(t, configKeySetups, s)
	return s
}

// TrashSetup moves the setup named name to trash.setups with removed_at =
// at and the repo ids pinned to it; found is false when there is none.
func TrashSetup(home, name string, at time.Time) (found bool, err error) {
	err = editSetups(home, func(root *yaml.Node) error {
		seq := mapGet(root, configKeySetups)
		if seq == nil || seq.Kind != yaml.SequenceNode {
			return nil
		}
		i := setupIndex(seq, name)
		if i < 0 {
			return nil
		}
		found = true
		ent := seq.Content[i]
		seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
		var pins []string
		if pm := mapGet(root, configKeyRepoSetups); pm != nil && pm.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(pm.Content); {
				if pm.Content[j+1].Value == name {
					pins = append(pins, pm.Content[j].Value)
					pm.Content = append(pm.Content[:j], pm.Content[j+2:]...)
					continue
				}
				j += 2
			}
		}
		// The last setup trashed: no `setups: []` / `repo_setups: {}`
		// left behind (a flow node the next add would append into).
		yamlfmt.DropEmpty(root, configKeySetups)
		yamlfmt.DropEmpty(root, configKeyRepoSetups)
		mapDelete(ent, configKeyRemovedAt)
		mapDelete(ent, configKeyRepoPins)
		mapSet(ent, configKeyRemovedAt, scalar(at.UTC().Format(time.RFC3339)))
		if len(pins) > 0 {
			ps := &yaml.Node{Kind: yaml.SequenceNode}
			for _, p := range pins {
				ps.Content = append(ps.Content, scalar(p))
			}
			mapSet(ent, configKeyRepoPins, ps)
		}
		ts := trashSeq(root)
		ts.Content = append(ts.Content, ent)
		return nil
	})
	return found, err
}

// trashNodes are the trash mapping and its setups sequence; ts is nil
// when either is absent.
func trashNodes(root *yaml.Node) (t, ts *yaml.Node) {
	t = mapGet(root, configKeyTrash)
	if t == nil || t.Kind != yaml.MappingNode {
		return nil, nil
	}
	ts = mapGet(t, configKeySetups)
	if ts == nil || ts.Kind != yaml.SequenceNode {
		return t, nil
	}
	return t, ts
}

// newestTrashIndex is the index of the newest trash entry named name, -1
// when none (onboard.NewestTrashed over the node list).
func newestTrashIndex(ts *yaml.Node, name string) int {
	var list []onboard.TrashedSetup
	idx := []int{}
	for i, m := range ts.Content {
		if m.Kind != yaml.MappingNode {
			continue
		}
		t := onboard.TrashedSetup{}
		if n := mapGet(m, configKeyName); n != nil {
			t.Setup.Name = n.Value
		}
		if r := mapGet(m, configKeyRemovedAt); r != nil {
			t.RemovedAt = r.Value
		}
		list = append(list, t)
		idx = append(idx, i)
	}
	if at := onboard.NewestTrashed(list, name); at >= 0 {
		return idx[at]
	}
	return -1
}

// trashNotFound is E_SETUP_NOT_FOUND for a name not in the trash.
func trashNotFound(name string) error {
	return errs.New(errs.SetupNotFound, "no setup named "+strconv.Quote(name)+" in the trash").
		WithHint("lore setup trash list")
}

// RestoreResult is what RestoreSetup did.
type RestoreResult struct {
	Setup onboard.SetupEntry
	// Pins are the repo ids pinned back; Skipped those pinned to another
	// setup since (left as they are).
	Pins, Skipped []string
	// DefaultCleared is set when the restored entry was the default and
	// another setup is the default now (it stays the default).
	DefaultCleared bool
}

// RestoreSetup brings the newest trash entry named name back into setups[]
// (as name as, when given). E_SETUP_NOT_FOUND when the trash has none,
// E_SETUP_NAME_TAKEN when a setup of that name exists.
func RestoreSetup(home, name, as string) (RestoreResult, error) {
	var res RestoreResult
	if as == "" {
		as = name
	}
	if !onboard.ValidSetupName(as) {
		return res, errs.New(errs.InvalidInput, "setup restore: "+strconv.Quote(as)+" is not a valid setup name").
			WithHint("snake_case or kebab-case, e.g. personal-old")
	}
	err := editSetups(home, func(root *yaml.Node) error {
		t, ts := trashNodes(root)
		if ts == nil {
			return trashNotFound(name)
		}
		i := newestTrashIndex(ts, name)
		if i < 0 {
			return trashNotFound(name)
		}
		seq := setupsSeq(root)
		if setupIndex(seq, as) >= 0 {
			return errs.New(errs.SetupNameTaken, "a setup named "+strconv.Quote(as)+" exists; restore the trashed one under another name").
				WithHint("lore setup trash restore " + name + " --as " + name + "-old")
		}
		ent := ts.Content[i]
		ts.Content = append(ts.Content[:i], ts.Content[i+1:]...)
		if len(ts.Content) == 0 {
			mapDelete(t, configKeySetups)
			if len(t.Content) == 0 {
				mapDelete(root, configKeyTrash)
			}
		}
		var pins []string
		if ps := mapGet(ent, configKeyRepoPins); ps != nil && ps.Kind == yaml.SequenceNode {
			for _, p := range ps.Content {
				pins = append(pins, p.Value)
			}
		}
		mapDelete(ent, configKeyRemovedAt)
		mapDelete(ent, configKeyRepoPins)
		mapSet(ent, configKeyName, scalar(as))
		if d := mapGet(ent, "default"); d != nil && d.Value == "true" {
			for _, o := range seq.Content {
				if od := mapGet(o, "default"); od != nil && od.Value == "true" {
					mapDelete(ent, "default")
					res.DefaultCleared = true
					break
				}
			}
		}
		seq.Content = append(seq.Content, ent)
		if len(pins) > 0 {
			pm := mapChild(root, configKeyRepoSetups)
			for _, p := range pins {
				if cur := mapGet(pm, p); cur != nil {
					res.Skipped = append(res.Skipped, p)
					continue
				}
				mapSet(pm, p, scalar(as))
				res.Pins = append(res.Pins, p)
			}
		}
		return nil
	})
	if err != nil {
		return RestoreResult{}, err
	}
	c, err := ReadSetupsConfig(home)
	if err != nil {
		return res, err
	}
	res.Setup, _ = c.Setup(as)
	return res, nil
}

// PurgeOptions are a permanent delete's extra clears.
type PurgeOptions struct {
	// ClearIdentity removes the top-level identity: block.
	ClearIdentity bool
	// ClearPersonalStore removes personal.store.
	ClearPersonalStore bool
	// At stamps the trash.purged entry (time.Now when zero).
	At time.Time
}

// PurgeResult is what PurgeSetup removed.
type PurgeResult struct {
	Entry                         onboard.TrashedSetup
	IdentityCleared, StoreCleared bool
	// OrgCleared: the purged member/admin setup's orgs[] entry was removed
	// (no other setup, live or trashed, names that org).
	OrgCleared bool
}

// PurgeSetup deletes the newest trash entry named name for good, and the
// top-level identity / personal.store when asked. A member or admin
// setup's orgs[] entry goes with it when no other setup, live or trashed,
// names that org, so the name and the team are free to make again (owner
// decision 2026-11-08: re-adding an admin setup was refused by
// org.name_free with no way out but a hand edit). It never touches a store
// or a repo. E_SETUP_NOT_FOUND when the trash has none.
func PurgeSetup(home, name string, o PurgeOptions) (PurgeResult, error) {
	var res PurgeResult
	before, err := ReadSetupsConfig(home)
	if err != nil {
		return res, err
	}
	at := onboard.NewestTrashed(before.Trash, name)
	if at < 0 {
		return res, trashNotFound(name)
	}
	res.Entry = before.Trash[at]
	err = editSetups(home, func(root *yaml.Node) error {
		t, ts := trashNodes(root)
		if ts == nil {
			return trashNotFound(name)
		}
		i := newestTrashIndex(ts, name)
		if i < 0 {
			return trashNotFound(name)
		}
		ts.Content = append(ts.Content[:i], ts.Content[i+1:]...)
		if len(ts.Content) == 0 {
			mapDelete(t, configKeySetups)
		}
		// The tombstone, from the identity and store as they are before
		// the clears below (a solo setup used them).
		if err := appendPurged(t, onboard.PurgedOf(res.Entry, topIdentityKey(root), before.SharedStore, purgeTime(o.At))); err != nil {
			return err
		}
		if onboard.OrgFreedByPurge(before, at) {
			res.OrgCleared = deleteOrgEntry(root, res.Entry.Setup.Org)
		}
		if o.ClearIdentity {
			res.IdentityCleared = mapDelete(root, configKeyIdentity)
		}
		if o.ClearPersonalStore {
			res.StoreCleared = deletePersonalStore(root)
		}
		return nil
	})
	return res, err
}

// deleteOrgEntry removes the orgs[] entry named org (and orgs: when that
// leaves it empty); false when there was none.
func deleteOrgEntry(root *yaml.Node, org string) bool {
	seq := mapGet(root, configKeyOrgs)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return false
	}
	for i, m := range seq.Content {
		if m.Kind != yaml.MappingNode {
			continue
		}
		if n := mapGet(m, configKeyName); n != nil && n.Value == org {
			seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
			if len(seq.Content) == 0 {
				mapDelete(root, configKeyOrgs)
			}
			return true
		}
	}
	return false
}

// deletePersonalStore removes personal.store from the config root (and
// personal: when that leaves it empty); false when it was not set.
func deletePersonalStore(root *yaml.Node) bool {
	p := mapGet(root, configKeyPersonal)
	if p == nil || p.Kind != yaml.MappingNode {
		return false
	}
	found := mapDelete(p, configKeyStore)
	if len(p.Content) == 0 {
		mapDelete(root, configKeyPersonal)
	}
	return found
}

// ClearLeftover removes the top-level identity: (k = identity) or
// personal.store (k = personal_store) from config.yaml, keeping every
// other key and comment; found=false when it was not set. It does not
// check that the key is a leftover (setup.leftovers.clear does).
func ClearLeftover(home string, k onboard.LeftoverKind) (found bool, err error) {
	err = editSetups(home, func(root *yaml.Node) error {
		switch k {
		case onboard.LeftoverIdentity:
			found = mapDelete(root, configKeyIdentity)
		case onboard.LeftoverPersonalStore:
			found = deletePersonalStore(root)
		default:
			return errs.New(errs.InvalidInput, "clear leftover: unknown kind "+string(k))
		}
		return nil
	})
	return found, err
}

// purgeTime is at as RFC 3339 UTC, now when at is zero.
func purgeTime(at time.Time) string {
	if at.IsZero() {
		at = time.Now()
	}
	return at.UTC().Format(time.RFC3339)
}

// topIdentityKey is config.yaml's identity.key ("" when none).
func topIdentityKey(root *yaml.Node) string {
	if id := mapGet(root, configKeyIdentity); id != nil && id.Kind == yaml.MappingNode {
		if k := mapGet(id, configKeyIdentityKey); k != nil {
			return strings.TrimSpace(k.Value)
		}
	}
	return ""
}

// appendPurged adds p to trash.purged (t is the trash mapping), replacing
// an entry with the same name, kind, org and fingerprints (a setup made
// and purged again) so the list does not grow with repeats.
func appendPurged(t *yaml.Node, p onboard.PurgedSetup) error {
	var n yaml.Node
	if err := n.Encode(p); err != nil {
		return errs.Wrap(errs.Internal, "setup trash purge: cannot write trash.purged", err)
	}
	seq := mapGet(t, configKeyPurged)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		seq = &yaml.Node{Kind: yaml.SequenceNode}
		mapSet(t, configKeyPurged, seq)
	}
	for i, old := range seq.Content {
		var o onboard.PurgedSetup
		if old.Decode(&o) == nil && o.Name == p.Name && o.Kind == p.Kind && o.Org == p.Org && o.IdentitySHA == p.IdentitySHA && o.StoreSHA == p.StoreSHA {
			seq.Content[i] = &n
			return nil
		}
	}
	seq.Content = append(seq.Content, &n)
	return nil
}

// SetSetupDisabled sets or clears disabled (and disabled_at) on the setup
// named name, keeping every other key as it is; found is false when there
// is none. changed is false when it already was so.
func SetSetupDisabled(home, name string, disabled bool, at time.Time) (found, changed bool, err error) {
	err = editSetups(home, func(root *yaml.Node) error {
		seq := mapGet(root, configKeySetups)
		if seq == nil || seq.Kind != yaml.SequenceNode {
			return nil
		}
		i := setupIndex(seq, name)
		if i < 0 {
			return nil
		}
		found = true
		ent := seq.Content[i]
		cur := mapGet(ent, configKeyDisabled)
		was := cur != nil && cur.Value == "true"
		if was == disabled {
			return nil
		}
		changed = true
		if disabled {
			mapSet(ent, configKeyDisabled, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
			mapSet(ent, configKeyDisabledAt, scalar(at.UTC().Format(time.RFC3339)))
			return nil
		}
		mapDelete(ent, configKeyDisabled)
		mapDelete(ent, configKeyDisabledAt)
		return nil
	})
	return found, changed, err
}
