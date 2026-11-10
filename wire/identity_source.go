package wire

// identity_source.go — one identity per setup is the source (SOURCE_OF_TRUTH
// row 1, owner 2026-11-09). The old top-level `identity:` of config.yaml is
// a copy to retire into the setup; adapters/identity.Decide says how.
//
//	any config.yaml write ─► migrateTopIdentityOnWrite (configwrite.go) ─► default solo setup?
//	                           same / fill ─► the setup gets the missing fields, identity: removed
//	                           conflict    ─► left as is: the doctor asks (doctor_identitysource.go)
//	lore identity migrate [--keep setup|top] ─► migrateTopIdentity (the active setup, or --setup)
//	lore identity set ─► setSetupIdentity (a solo setup) | orgs[<org>].identity | identity: (no setup)
//
// Only a solo setup takes part: a member or admin setup's identity is
// orgs[<org>].identity, and the top-level one still answers in repos of no
// org (F10 rule 6), so it is not a copy of the org's.

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/yamlfmt"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Identity block keys inside a setup entry (onboard.SetupIdentity).
const (
	identityKeyKey    = "key"
	identityKeyName   = "name"
	identityKeyHandle = "handle"
)

// cmdIdentityMigrate is the command that retires the top-level identity.
const cmdIdentityMigrate = "lore identity migrate"

// entryOf is a setup identity block as the chain's Entry.
func entryOf(i onboard.SetupIdentity) identity.Entry {
	return identity.Entry{Key: strings.TrimSpace(i.Key), Name: strings.TrimSpace(i.Name), Handle: strings.TrimSpace(i.Handle)}
}

// defaultSoloSetup is the enabled default setup when it is solo: the one
// setup a config write may migrate into without knowing the folder (the
// default answers wherever no other setup claims the folder, 3.3 step 6).
func defaultSoloSetup(c onboard.Config) (onboard.SetupEntry, bool) {
	for _, s := range c.Setups {
		if s.Default && !s.Disabled {
			return s, s.Kind == onboard.KindSolo
		}
	}
	return onboard.SetupEntry{}, false
}

// migrateTopIdentityOnWrite is out (the config.yaml about to be written)
// with the top-level identity retired into the default solo setup when
// Decide says no choice is needed (same, fill). A conflict, no default
// solo setup, or a result that would not decode or validate leaves out as
// it is: the migration never blocks the write it rides on.
func migrateTopIdentityOnWrite(out []byte) []byte {
	d, err := decodeSetupsDoc(out)
	if err != nil {
		return out
	}
	doc := &yaml.Node{}
	if yamlfmt.Decode(out, doc) != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return out
	}
	s, ok := defaultSoloSetup(d.config())
	if !ok {
		return out
	}
	top, own := entryOf(d.Identity), entryOf(s.Identity)
	if v := identity.Decide(top, own); v != identity.SourceSame && v != identity.SourceFill {
		return out
	}
	if !retireTopIdentity(doc.Content[0], s.Name, identity.Merge(top, own, identity.KeepSetup)) {
		return out
	}
	next, err := yamlfmt.EncodeLike(doc, out)
	if err != nil {
		return out
	}
	if _, err := validateSetupsRaw(next); err != nil {
		return out
	}
	return next
}

// retireTopIdentity writes keep into setups[name].identity (its other keys,
// such as emails, kept) and removes the top-level identity:. false when
// the entry is not in root.
func retireTopIdentity(root *yaml.Node, name string, keep identity.Entry) bool {
	ent := setupNode(root, name)
	if ent == nil {
		return false
	}
	idn := identityNode(ent)
	setIdentityFields(idn, keep)
	carryIdentityComments(mapGet(root, configKeyIdentity), idn)
	mapDelete(root, configKeyIdentity)
	return true
}

// carryIdentityComments moves the person's comments on the top-level
// block's fields (`key: … # me`) onto the same fields of the setup's
// block when those have none, so retiring the block loses no comment.
func carryIdentityComments(from, to *yaml.Node) {
	if from == nil || from.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(from.Content); i += 2 {
		k, v := from.Content[i], from.Content[i+1]
		dst := mapGet(to, k.Value)
		if dst == nil {
			continue
		}
		if dst.LineComment == "" {
			dst.LineComment = v.LineComment
		}
		for j := 0; j+1 < len(to.Content); j += 2 {
			if to.Content[j].Value == k.Value && to.Content[j].HeadComment == "" {
				to.Content[j].HeadComment = k.HeadComment
			}
		}
	}
}

// setupNode is the mapping of setups[] named name, nil when absent.
func setupNode(root *yaml.Node, name string) *yaml.Node {
	seq := mapGet(root, configKeySetups)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	if i := setupIndex(seq, name); i >= 0 {
		return seq.Content[i]
	}
	return nil
}

// identityNode is the identity mapping of a setup entry, created when absent.
func identityNode(ent *yaml.Node) *yaml.Node {
	return mapChild(ent, configKeyIdentity)
}

// setIdentityFields sets key, name and handle of an identity mapping to e
// (an empty field is removed); a value already equal keeps its node, so
// its style and comments stay.
func setIdentityFields(n *yaml.Node, e identity.Entry) {
	for _, kv := range [][2]string{{identityKeyKey, e.Key}, {identityKeyName, e.Name}, {identityKeyHandle, e.Handle}} {
		if kv[1] == "" {
			mapDelete(n, kv[0])
			continue
		}
		if cur := mapGet(n, kv[0]); cur != nil && cur.Kind == yaml.ScalarNode && cur.Value == kv[1] {
			continue
		}
		mapSet(n, kv[0], scalar(kv[1]))
	}
}

// identityMigration is what migrateTopIdentity did.
type identityMigration struct {
	// Setup is the setup migrated into.
	Setup string
	// Verdict is Decide's answer before the migration.
	Verdict identity.SourceVerdict
	// Top and Before are the two identities before; After is the setup's
	// identity now (zero when nothing changed).
	Top, Before, After identity.Entry
	// Changed is false when there was no top-level identity.
	Changed bool
}

// migrateTopIdentity retires the top-level identity into solo setup name
// (`lore identity migrate`). keep "" migrates only when no choice is
// needed; a conflict is then E_REFUSED naming both identities and the two
// commands. keep setup or top picks for a conflict: that side wins every
// field it sets, the other fills the rest.
func migrateTopIdentity(home, name string, keep identity.Keep) (identityMigration, error) {
	res := identityMigration{Setup: name}
	err := editSetups(home, func(root *yaml.Node) error {
		var d setupsDoc
		if err := root.Decode(&d); err != nil {
			return errs.Wrap(errs.ConfigInvalid, "wire: ~/.lore/"+ConfigFile+" is not valid: "+err.Error(), err)
		}
		s, ok := d.config().Setup(name)
		if !ok {
			return setupNotFound(name, d.Setups)
		}
		if s.Kind != onboard.KindSolo {
			return teamSetupIdentityRefusal(s)
		}
		res.Top, res.Before = entryOf(d.Identity), entryOf(s.Identity)
		res.Verdict = identity.Decide(res.Top, res.Before)
		switch res.Verdict {
		case identity.SourceNone:
			return nil
		case identity.SourceConflict:
			if keep == "" {
				return identityConflictError(name, res.Top, res.Before)
			}
		}
		if keep == "" {
			keep = identity.KeepSetup
		}
		res.After = identity.Merge(res.Top, res.Before, keep)
		res.Changed = retireTopIdentity(root, name, res.After)
		return nil
	})
	return res, err
}

// teamSetupIdentityRefusal is identity migrate on a member or admin setup.
func teamSetupIdentityRefusal(s onboard.SetupEntry) error {
	return errs.New(errs.Refused, "setup "+s.Name+" is a "+string(s.Kind)+" setup: its identity is orgs["+s.Org+"].identity, and the top-level identity: still answers in repos of no org, so lore does not merge the two").
		WithHint("change the org identity with lore identity set --org " + s.Org + " --key human:<email>; remove the top-level one with lore identity unset")
}

// identityConflictError is the refusal to pick between two identities.
func identityConflictError(setup string, top, own identity.Entry) error {
	return errs.New(errs.Refused, "the top-level identity: says "+top.String()+" and setup "+setup+" says "+own.String()+"; lore never picks one for you").
		WithHint("keep the setup's: " + migrateCmd(identity.KeepSetup, setup) + "; or keep the top-level one: " + migrateCmd(identity.KeepTop, setup))
}

// migrateCmd is the identity migrate command that keeps keep for setup.
func migrateCmd(keep identity.Keep, setup string) string {
	return cmdIdentityMigrate + " --keep " + string(keep) + " --setup " + setup
}

// setSetupIdentity writes e as solo setup name's identity (key, name,
// handle; its emails kept; a replaced key joins aliases:). A member or admin setup is E_INVALID_INPUT:
// its identity is the org entry's.
func setSetupIdentity(home, name string, e identity.Entry) error {
	return editSetups(home, func(root *yaml.Node) error {
		ent := setupNode(root, name)
		if ent == nil {
			return errs.New(errs.NotFound, "setup "+strconv.Quote(name)+" is not in ~/.lore/"+ConfigFile).WithHint("lore setup list shows the setups")
		}
		idn := identityNode(ent)
		keepOldKeyAsAlias(idn, e.Key)
		setIdentityFields(idn, e)
		return nil
	})
}

// identityKeyAliases is a solo setup identity's aliases: list
// (onboard.SetupIdentity.Aliases).
const identityKeyAliases = "aliases"

// keepOldKeyAsAlias appends the identity block n's current key to its
// aliases: when key replaces it (a new email for the same person, `lore
// identity set` on a solo setup), so the actor keeps its id
// (SOURCE_OF_TRUTH, an email change keeps one person). An alias already
// listed is not added twice.
func keepOldKeyAsAlias(n *yaml.Node, key string) {
	cur := mapGet(n, identityKeyKey)
	if cur == nil || cur.Kind != yaml.ScalarNode || cur.Value == "" || key == "" || cur.Value == key {
		return
	}
	seq := mapGet(n, identityKeyAliases)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		mapSet(n, identityKeyAliases, seq)
	}
	for _, x := range seq.Content {
		if x.Value == cur.Value {
			return
		}
	}
	seq.Content = append(seq.Content, scalar(cur.Value))
}

// clearSetupIdentity removes key, name, handle and aliases of solo setup name's
// identity (the block goes when nothing else is left in it); false when
// it held none.
func clearSetupIdentity(home, name string) (bool, error) {
	cleared := false
	err := editSetups(home, func(root *yaml.Node) error {
		ent := setupNode(root, name)
		if ent == nil {
			return nil
		}
		n := mapGet(ent, configKeyIdentity)
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		// the aliases are earlier keys of the key removed here: they go
		// with it (a block of aliases alone would be a keyless identity)
		for _, k := range []string{identityKeyKey, identityKeyName, identityKeyHandle, identityKeyAliases} {
			if mapDelete(n, k) {
				cleared = true
			}
		}
		if len(n.Content) == 0 {
			mapDelete(ent, configKeyIdentity)
		}
		return nil
	})
	return cleared, err
}

// keylessSetupProblems lists every solo setup whose identity block sets a
// name or handle but no key: the chain skips such a block (it needs the
// key), so who you are silently comes from the next step. Reported by
// lore config check and the doctor; a write is never refused for it.
func keylessSetupProblems(setups []onboard.SetupEntry) []string {
	var out []string
	for _, s := range setups {
		if s.Kind != onboard.KindSolo || s.Identity.IsZero() || strings.TrimSpace(s.Identity.Key) != "" {
			continue
		}
		name := strings.TrimSpace(s.Identity.Name)
		set := `lore identity set "<Your Name> <your email>" --setup ` + s.Name
		if name != "" {
			set = `lore identity set "` + name + ` <your email>" --setup ` + s.Name
		}
		out = append(out, "setups["+s.Name+"].identity.key: missing, so lore ignores this identity block ("+entryOf(s.Identity).String()+
			") and takes who you are from the next step of the chain (lore identity show names it); add  key: human:<your email>  under it, or run "+set)
	}
	return out
}
