package wire

// storepushmode.go — where a store's push and compaction settings come
// from and who may change them (owner decision 2026-11-09, "store push
// modes"; logic/storepush holds the rules):
//
//	me (personal store)   personal: block of ~/.lore/config.yaml; this machine decides
//	an org's store        the store's lore-store.yaml (every clone reads the
//	                      same values); only an org admin writes them
//	a stores.yaml store   the store's lore-store.yaml too (decided 2026-11-10,
//	                      land7 A: a lore-only store attached in .lore/stores.yaml
//	                      gets push modes as org stores do); no org, so
//	                      whoever can push to the store may change them
//	repo (code repo)      refused: lore never pushes or rewrites a code repo
//
// A stores.yaml store whose worktree holds an org's lore-org.yaml is that
// org's store even when this machine has not imported the org: it is
// treated as an org store (only that org's admin may set or compact), so
// attaching an org store by url never side-steps the admin rule.
//
// An admin of an org, on this machine, is someone with an admin setup of
// that org (`lore setup` → Start a team; the same test team invites use).

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// Stamp files beside a store clone's worktree (never inside it).
const (
	pushStampFile    = "push_stamp"
	compactStampFile = "compact_stamp"
	// pushFailStampFile records a batch push that failed (retry after push_every).
	pushFailStampFile = "push_fail_stamp"
	stampFileMode     = 0o600
)

// pushPlace says where a store's settings live.
type pushPlace int

const (
	placeNone     pushPlace = iota // not a store push settings apply to
	placePersonal                  // ~/.lore/config.yaml personal:
	placeOrg                       // the store's lore-store.yaml, set by an org admin
	placeStore                     // a stores.yaml store's lore-store.yaml, set by whoever can push to it
)

// personalPushRaw reads the personal: block's settings.
func personalPushRaw(home string) (storepush.Raw, error) {
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil {
		return storepush.Raw{}, err
	}
	p := cfg.Personal
	return storepush.Raw{Push: p.Push, PushEvery: p.PushEvery, Compact: p.Compact, CompactAfter: p.CompactAfter}, nil
}

// orgOfClone names the configured org one of whose stores the clone at
// dir (a git backend's worktree) belongs to; "" when none.
func orgOfClone(home, dir string) string {
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil || dir == "" {
		return ""
	}
	parent := filepath.Dir(dir) // <base> or <base>@<branch>
	for _, o := range cfg.Orgs {
		for _, s := range o.Stores {
			if base := cloneBaseOf(home, s.URL); base != "" && (parent == base || strings.HasPrefix(parent, base+branchCloneSep)) {
				return o.Name
			}
		}
	}
	return ""
}

// orgOfURL names the configured org that lists the store url; "".
func orgOfURL(home, raw string) string {
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil {
		return ""
	}
	want, err := canonical(attach.Store{URL: raw})
	if err != nil {
		return ""
	}
	for _, o := range cfg.Orgs {
		for _, s := range o.Stores {
			if c, cerr := canonical(attach.Store{URL: s.URL}); cerr == nil && c == want {
				return o.Name
			}
		}
	}
	return ""
}

// cloneBaseOf is the clone base lore keeps for a store url ("" when the
// url is not a git store); a pinned branch adds "@<branch>" to it.
func cloneBaseOf(home, raw string) string {
	canon, err := gitbackend.CanonicalURL(raw)
	if err != nil {
		return ""
	}
	u, err := url.Parse(canon)
	if err != nil {
		return ""
	}
	r, err := gitbackend.RemoteOf(u)
	if err != nil {
		return ""
	}
	base, err := gitbackend.CloneBase(home, r)
	if err != nil {
		return ""
	}
	return base
}

// isOrgAdmin reports whether this machine holds an admin setup of org.
func isOrgAdmin(home, org string) bool {
	if org == "" {
		return false
	}
	raw, err := readConfigRaw(home)
	if err != nil || raw == nil {
		return false
	}
	d, err := decodeSetupsDoc(raw)
	if err != nil {
		return false
	}
	for _, e := range d.Setups {
		if e.Kind == onboard.KindAdmin && e.Org == org {
			return true
		}
	}
	return false
}

// readStamp reads a time stamp file (zero when absent or unreadable).
func readStamp(dir, name string) time.Time {
	if dir == "" {
		return time.Time{}
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// writeStamp records now in a stamp file; a lost stamp only means the
// next command decides again.
func writeStamp(dir, name string, now time.Time) {
	if dir == "" {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, name), []byte(now.UTC().Format(time.RFC3339Nano)+"\n"), stampFileMode)
}

// gitOf is the git backend under every wrapper, or nil.
func gitOf(b store.Backend) *gitbackend.Backend {
	gb, _ := concrete(b).(*gitbackend.Backend)
	return gb
}

// otherClonesUnpushed lists unpushed work in this machine's other clones
// of the store whose worktree is work (its branch clones and the v0.1.17
// link clone; the clone at work itself is brought level by its own sync).
// A clone that cannot be listed counts as holding work (fail closed).
func otherClonesUnpushed(ctx context.Context, home, work string) []string {
	rel, err := filepath.Rel(filepath.Join(home, gitbackend.ClonesDir), filepath.Dir(work))
	if err != nil || strings.HasPrefix(rel, "..") {
		return []string{"the clone " + work + " is not under " + filepath.Join(home, gitbackend.ClonesDir)}
	}
	norm, _, _ := strings.Cut(filepath.ToSlash(rel), branchCloneSep)
	c := &standaloneClones{home: home}
	clones, err := c.clonesOf("https://"+norm, norm)
	if err != nil {
		return []string{"the clones of the store cannot be listed: " + err.Error()}
	}
	var out []string
	for _, cl := range clones {
		if cl.work != "" && filepath.Clean(cl.work) == filepath.Clean(work) {
			continue
		}
		out = append(out, unpushedIn(ctx, cl)...)
	}
	return out
}

// errCodeRepoPush is the refusal of push settings or a compaction on the
// code repo.
func errCodeRepoPush(verb string) error {
	return errs.New(errs.Refused, verb+": `repo` is the code repository's own .lore/data; lore never pushes or rewrites a code repo's history, so push and compact settings do not apply there").
		WithHint("you push the code repo with your code; `lore live compact` compacts its live rows")
}

// errFolderStorePush is the refusal on a file:// store.
func errFolderStorePush(verb, name string) error {
	return errs.New(errs.Refused, verb+": store "+name+" is a folder store (file://): it has no remote to push to and no history to compact").
		WithHint("push modes and compaction apply to git stores; `lore store set-url " + name + " <git url>` moves it to one")
}

// errNotAdmin refuses an org store's shared setting to a non-admin.
func errNotAdmin(verb, name, org string) *errs.Error {
	return errs.New(errs.Refused, verb+": store "+name+" belongs to org "+org+" and only an admin of "+org+" may change its shared settings; this machine has no admin setup of "+org).
		WithHint("ask the org admin to run `lore store set " + name + " …`; a member cannot override the org's setting locally")
}

// placeOf decides where name's settings live; for an org store it also
// names the org. A store attached only in .lore/stores.yaml is placeStore
// here; refinePlace turns it into placeOrg once its worktree shows an
// org's lore-org.yaml.
func (a *App) placeOf(ctx context.Context, verb, name string) (attach.Store, pushPlace, string, error) {
	if name == string(store.SourceRepo) {
		return attach.Store{}, placeNone, "", errCodeRepoPush(verb)
	}
	src, err := a.findStore(ctx, verb, name)
	if err != nil {
		return attach.Store{}, placeNone, "", err
	}
	if strings.HasPrefix(src.URL, folderURLScheme) {
		return src, placeNone, "", errFolderStorePush(verb, name)
	}
	if name == attach.ReservedMe {
		return src, placePersonal, "", nil
	}
	if org := orgOfURL(a.opts.Home, src.URL); org != "" {
		return src, placeOrg, org, nil
	}
	return src, placeStore, "", nil
}

// refinePlace is placeOf's answer after the store's worktree (dir) is open:
// a placeStore store that holds an org's lore-org.yaml is that org's store.
func refinePlace(dir string, place pushPlace, org string) (pushPlace, string) {
	if place != placeStore {
		return place, org
	}
	if o := orgFileOrg(dir); o != "" {
		return placeOrg, o
	}
	return place, org
}

// orgFileOrg names the org whose lore-org.yaml the store worktree dir holds
// ("" when none), looking where `lore org import` does (orgcfg
// ReadSuggestion): the marker's org_folder, then "org", then the root.
func orgFileOrg(dir string) string {
	if dir == "" {
		return ""
	}
	folders := []string{fallbackOrgFileFolder, ""}
	if m, err := rowfs.ReadMarker(dir); err == nil && m.OrgFolder != "" {
		folders = append([]string{m.OrgFolder}, folders...)
	}
	for _, f := range folders {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f), rowfs.LoreDir, orgcfg.OrgFile))
		if err != nil {
			continue
		}
		var doc struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(b, &doc) == nil && strings.TrimSpace(doc.Name) != "" {
			return strings.TrimSpace(doc.Name)
		}
	}
	return ""
}

// fallbackOrgFileFolder is the org folder read when the marker names none
// (orgcfg's fallbackOrgFolder, v0.1.17 defaultMarkerOrgFolder).
const fallbackOrgFileFolder = "org"

// cloneOrg names the org of the store clone at dir: the configured org
// listing it, else the org its lore-org.yaml names ("" for a store of no
// org).
func cloneOrg(home, dir string) string {
	if o := orgOfClone(home, dir); o != "" {
		return o
	}
	return orgFileOrg(dir)
}
