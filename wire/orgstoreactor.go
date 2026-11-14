package wire

// orgstoreactor.go — a write into an org's store carries that org's
// identity (plainfiles:4, coordinator decision C19).
//
// The open resolves one identity for the folder lore runs in (resolveActor).
// In a repo no org matches, that is the fallback chain, which skips git's
// user.email when it is an org key and so lands on the OS user
// (human:<user>@<host>). A `--to <org store>/<folder>` write from there was
// stamped with that OS-user actor while its git commit was signed with the
// org identity (storeSigner.owner). Each attached store of a configured
// org now has its own writer: the identity the chain gives inside that org
// (identity.Resolve with that org entry: the org key, or an agent actor the
// org accepts), applied through engine.Attached.ActorAs and the actor row
// ensureActor writes there:
//
//	open ─▶ st.who (this folder's identity)
//	   └─ per attached store ─▶ org of its url? ─▶ identity in that org
//	         differs from st.who ─▶ storeWho[name]: ActorAs maps st.actor
//	                                to it; ensureActor writes its row

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// storeWriter is the person's identity in the org that owns one attached
// store, when it is not the open's own identity.
type storeWriter struct {
	who identity.Resolved
	id  ids.ID[ids.Actor]
}

// resolveStoreWriters fills st.storeWho for every attached store that a
// configured org lists (Options.OrgStoreURLs: any spelling of its url,
// folder and branch ignored) and whose identity in that org differs from
// st.who. Only an open whose identity came from the chain gets them: an
// explicit Options.Actor or ResolveActor is the caller's choice. No hook,
// an unreadable config, an org entry with no key or a url that does not
// parse adds nothing (the write then keeps the open's identity, as before).
func (st *state) resolveStoreWriters(ctx context.Context, opts Options, attached []attach.Opened) {
	if st.who.StableKey == "" || len(attached) == 0 || opts.OrgStoreURLs == nil {
		return
	}
	urls, err := opts.OrgStoreURLs(ctx)
	if err != nil || len(urls) == 0 {
		return
	}
	mc, err := readMachineConfig(opts.Home)
	if err != nil {
		return
	}
	byOrg := map[string]storeWriter{}
	for _, o := range attached {
		if o.Name == store.SourceRepo || o.Location == "" {
			continue
		}
		name, ok := orgOfStore(urls, o.Location)
		if !ok || name == st.who.Org {
			continue
		}
		w, seen := byOrg[name]
		if !seen {
			if w, ok = st.writerIn(ctx, opts, mc.Orgs, name); !ok {
				continue
			}
			byOrg[name] = w
		}
		if w.id == st.actor {
			continue
		}
		if st.storeWho == nil {
			st.storeWho = map[store.Source]storeWriter{}
		}
		st.storeWho[o.Name] = w
	}
}

// writerIn is the identity the chain gives inside the org entry named
// name (the org key, or an agent actor the org accepts). ok is false when
// there is no such entry, it has no key, or the id cannot be made.
func (st *state) writerIn(ctx context.Context, opts Options, orgs []identity.Org, name string) (storeWriter, bool) {
	i := slices.IndexFunc(orgs, func(o identity.Org) bool { return o.Name == name })
	if i < 0 || strings.TrimSpace(orgs[i].Identity.Key) == "" {
		return storeWriter{}, false
	}
	org := orgs[i]
	in := opts
	in.Org = func(context.Context) (*identity.Org, error) { return &org, nil }
	who, err := resolveIdentity(ctx, in)
	if err != nil || who.StableKey == "" {
		return storeWriter{}, false
	}
	id, err := who.ActorID()
	if err != nil {
		return storeWriter{}, false
	}
	return storeWriter{who: who, id: id}, true
}

// orgOfStore is the entry name of the first configured org that lists the
// store at raw (storeKey: any spelling of the remote, folder and branch
// ignored).
func orgOfStore(urls []OrgStoreURL, raw string) (string, bool) {
	want, ok := storeKey(raw)
	if !ok {
		return "", false
	}
	for _, u := range urls {
		if k, ok := storeKey(u.URL); ok && k == want {
			return u.Org, true
		}
	}
	return "", false
}

// storeWriterAs is the ActorAs of an attached org store with its own
// writer (storeWho): the open's actor is stamped as the person's identity
// in that org; every other id, and the mapped one, stays (idempotent).
// It reads st.actor at write time, after any rebind of the open. nil when
// the store has no writer of its own.
func (st *state) storeWriterAs(name store.Source) func(ids.ID[ids.Actor]) ids.ID[ids.Actor] {
	w, ok := st.storeWho[name]
	if !ok {
		return nil
	}
	return func(a ids.ID[ids.Actor]) ids.ID[ids.Actor] {
		if a == st.actor {
			return w.id
		}
		return a
	}
}
