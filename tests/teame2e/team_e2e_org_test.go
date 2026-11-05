//go:build teame2e

package teame2e

// P1 — config, orgs, identity, personal notes (LORE_TEAM_SPEC.md F10, F17,
// F18, F19, E8, E12, E13, cookbook 5, 11, §5.6).

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// orgWorld is §5.2's laptop without stores in play (P1: org resolution by
// pattern, LORE_ORG, repo_orgs and .lore/org.yaml): alice with three entries
// and one checkout per kind of repo.
type orgWorld struct {
	w      *th.World
	alice  *th.Actor
	acme   *th.Clone // client repo (repos: pattern of northwind)
	beta   *th.Clone // betalabs own repo
	tools  *th.Clone // northwind own repo
	loose  *th.Clone // matches nothing
	remote map[string]*th.Remote
}

// newOrgWorld builds the P1 laptop; every repo gets a lore project.
func newOrgWorld(t *testing.T, caps ...th.Cap) *orgWorld {
	t.Helper()
	w := newTeamWorld(t, append([]th.Cap{th.Cmd("whoami"), th.Cmd("store", "init")}, caps...)...)
	o := &orgWorld{w: w, remote: map[string]*th.Remote{}}
	o.alice = w.Human("alice", "alice@northwind.example")
	o.alice.WriteConfig(aliceConfig())
	// The stores alice's config lists exist, as on a real machine: a store
	// that is configured but missing reads as unreachable, and the F16 leak
	// hook then (correctly) fails closed on every commit (lore-stores report).
	for _, slug := range []string{slugStore, slugBetaStore} {
		w.Remote(slug)
		initStore(w, o.alice, "", "https://"+slug, "", "--yes").OK(t)
	}
	mk := func(slug, name string) *th.Clone {
		r := w.Remote(slug)
		o.remote[slug] = r
		c := o.alice.Clone(r, name)
		c.MustLore("init", "--non-interactive", "--name="+name)
		c.CommitAll("lore")
		c.AdminPush("origin", "main")
		return c
	}
	o.acme = mk(slugApp, "app")
	o.beta = mk(slugBetaAPI, "api")
	o.tools = mk(slugOwnTools, "tools")
	o.loose = mk(slugLoose, "tool")
	return o
}

// E8 / F10 / cookbook 11 / §5.6: one laptop, two companies; each repo loads
// exactly one org and its identity, never both; staging never auto-matches.
func TestE08_TwoOrgHomeWhoamiInEachRepo(t *testing.T) {
	o := newOrgWorld(t)
	o.acme.MustLore("whoami").Says(t, orgNorthwind, aliceKey, "matched by").Never(t, aliceBetaKey, orgStaging)
	o.beta.MustLore("whoami").Says(t, orgBetalabs, aliceBetaKey, "matched by", "github.com/betalabs/*").Never(t, aliceKey)
	o.tools.MustLore("whoami").Says(t, orgNorthwind, aliceKey).Never(t, aliceBetaKey, orgStaging)
	// No org matched while orgs exist (F10 rule 6, Q7 13:35, fail closed):
	// the fallback chain skips any value equal to an org identity key, so
	// alice's git email (= his Northwind key) must not appear.
	// The one-line skip note names sources and orgs, never the org key itself
	// (lore-config-org 337c8a4); the identity line must not use it either.
	r := o.loose.MustLore("whoami").Says(t, "none", "lore org use", "skipped").Never(t, aliceKey, aliceBetaKey)
	if id := whoamiField(t, r, "identity"); strings.Contains(id, aliceKey) || strings.Contains(id, aliceBetaKey) || strings.Contains(id, "org entry") {
		t.Fatalf("no org matched: the identity must not borrow an org key: %q", id)
	}
}

// F10 rule 1/2 + Q-a: a staging entry is chosen only on purpose, with
// LORE_ORG for one shell or `lore org use` for one repo on this machine.
func TestF10_StagingIsSelectedOnlyOnPurpose(t *testing.T) {
	o := newOrgWorld(t, th.Cmd("org", "use"))
	o.acme.With(th.EnvLoreOrg+"="+orgStaging).MustLore("whoami").Says(t, orgStaging)
	o.acme.MustLore("org", "use", orgStaging)
	o.acme.MustLore("whoami").Says(t, orgStaging)
	if st := o.acme.Status(); st != "" {
		t.Fatalf("lore org use is machine-only and writes nothing in the repo: %q", st)
	}
	if b, _ := os.ReadFile(o.alice.LorePath("config.yaml")); !strings.Contains(string(b), "repo_orgs") {
		t.Fatal("the choice is stored in ~/.lore/config.yaml repo_orgs")
	}
	o.tools.MustLore("whoami").Never(t, orgStaging)
	o.acme.MustLore("org", "use", orgNorthwind)
	o.acme.MustLore("whoami").Never(t, orgStaging)
}

// F10 rule 6 (Q7 13:35): with orgs configured and none matched, the
// top-level identity: of config.yaml is the fallback; with no orgs at all
// the legacy chain (git email first) is unchanged.
func TestF10_NoOrgMatchedFallbackIdentity(t *testing.T) {
	o := newOrgWorld(t)
	cfg := aliceConfig()
	cfg.Identity = &th.Identity{Key: alicePersonalKey, Name: aliceName}
	o.alice.WriteConfig(cfg)
	o.loose.MustLore("whoami").Says(t, "none", alicePersonalKey).Never(t, aliceKey, aliceBetaKey)
	o.acme.MustLore("whoami").Says(t, orgNorthwind, aliceKey).Never(t, alicePersonalKey)

	o.alice.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}})
	o.loose.MustLore("whoami").Says(t, aliceKey, "git config")
}

// F10 rule 2 (lore-config-org Q2, accepted): `lore org use` stores the
// remotes it was chosen for; the same project checked out from a fork (same
// project id, other remote) skips rule 2 with a warning and never inherits
// the upstream's org; pointing back at the original remote restores it.
func TestF10_OrgUseIsSkippedInAForkWithTheUpstreamsID(t *testing.T) {
	o := newOrgWorld(t, th.Cmd("org", "use"))
	fork := o.w.Remote(slugForkApp)
	o.acme.AdminPush("https://"+slugForkApp, "main")
	if !fork.Has("refs/heads/main") {
		t.Fatal("control: the fork must hold the project, or this test proves nothing")
	}
	o.acme.MustLore("org", "use", orgStaging)
	o.acme.MustLore("whoami").Says(t, orgStaging)
	o.acme.Git("remote", "set-url", "origin", "https://"+slugForkApp)
	// The warning may name the recorded entry; the resolved org must not be it.
	r := o.acme.MustLore("whoami").Says(t, "warning", slugForkApp)
	if org := strings.Fields(whoamiField(t, r, "org")); len(org) == 0 || org[0] != "none" {
		t.Fatalf("a fork must not inherit the upstream's org use: org %v", org)
	}
	o.acme.Git("remote", "set-url", "origin", "https://"+slugApp)
	o.acme.MustLore("whoami").Says(t, orgStaging)
}

// F10 conflicts: two entries at the same step are ambiguous; lore refuses
// org-scoped commands until `lore org use` and never guesses.
func TestF10_TwoEntriesAtTheSameStepAreAmbiguous(t *testing.T) {
	o := newOrgWorld(t, th.Flag("to", "memory", "add"), th.Cmd("org", "use"))
	cfg := aliceConfig()
	cfg.Orgs[2].Repos = []string{"github.com/acme/*"} // betalabs now also claims acme/*
	o.alice.WriteConfig(cfg)
	o.acme.Lore("whoami").Says(t, "E_ORG_AMBIGUOUS", "lore org use")
	o.acme.Lore("memory", "add", "--body", "where does this go", "--to", "me").Fails(t, th.ExitOrg, "E_ORG_AMBIGUOUS")
	o.acme.MustLore("org", "use", orgBetalabs)
	o.acme.MustLore("whoami").Says(t, orgBetalabs)
}

// F10 rule 3 (Q-b): a committed .lore/org.yaml decides only in the org's own
// repos; in a client repo it cannot steer the org.
func TestF10_CommittedOrgYAMLOnlyInOwnRepos(t *testing.T) {
	o := newOrgWorld(t)
	o.tools.WriteFile(".lore/org.yaml", "org: northwind\nproject: lore-tools\n")
	o.tools.CommitAll("org choice")
	o.tools.MustLore("whoami").Says(t, orgNorthwind, ".lore/org.yaml")
	o.acme.WriteFile(".lore/org.yaml", "org: betalabs\n")
	o.acme.CommitAll("client tries to steer")
	o.acme.MustLore("whoami").Says(t, orgNorthwind).Never(t, aliceBetaKey)
}

// F10 edge case: ssh and https remotes, with or without .git, normalise to
// the same host/owner/repo.
func TestF10_RemoteFormsNormaliseToTheSameOrg(t *testing.T) {
	o := newOrgWorld(t)
	r := o.remote[slugBetaAPI]
	for name, url := range map[string]string{"ssh": r.SSHURL(), "https.git": r.URL() + ".git", "ssh-no-suffix": "git@github.com:betalabs/api"} {
		c := o.alice.CloneURL(r, url, "api-"+name)
		c.MustLore("whoami").Says(t, orgBetalabs, aliceBetaKey)
	}
}

// F10 fail-closed rule (P3): a store that cannot be read and has no cached
// link index makes the link status unknown; writes without --to are refused
// (E_LINK_UNKNOWN) while reads keep working.
func TestF10_UnknownLinkStatusFailsClosed(t *testing.T) {
	a := newAgency(t)
	for _, dir := range []string{"clones", "cache"} {
		if err := os.RemoveAll(a.alice.LorePath(dir)); err != nil {
			t.Fatal(err)
		}
	}
	a.alice.Offline()
	a.aliceApp.Lore("memory", "add", "--body", "where does this go").Fails(t, th.ExitLinkUnknown, "E_LINK_UNKNOWN")
	a.aliceApp.MustLore("memory", "list")
}

// F10 conflicts: LORE_ORG disagreeing with a link refuses org-scoped writes
// (E_ORG_DISAGREES) unless --force-org.
func TestF10_LoreOrgDisagreeingWithALinkIsRefused(t *testing.T) {
	a := newAgency(t)
	a.aliceApp.With(th.EnvLoreOrg+"="+orgBetalabs).Lore("memory", "add", "--body", "wrong company").Fails(t, th.ExitOrg, "E_ORG_DISAGREES")
}

// ---------------------------------------------------------------- F18 identity

// F18: with an org resolved, a human key in LORE_ACTOR is ignored with a
// warning and the org identity is used.
func TestF18_HumanKeyInLoreActorIsIgnored(t *testing.T) {
	o := newOrgWorld(t)
	o.acme.With(th.EnvLoreActor+"=human:intruder@else.example").MustLore("whoami").Says(t, aliceKey, "ignored").Never(t, "intruder@else.example\n")
}

// F18 / §7.4: an agent actor that is not on the org roster cannot claim.
func TestF18_UnregisteredAgentCannotClaim(t *testing.T) {
	cr := newCrew(t)
	id := cr.task(t, "only for registered agents")
	ghost := cr.alice.Agent("ghost")
	cr.toolsC.As(ghost).Lore("task", "claim", id).Refused(t, "roster")
	if _, ok := cr.claim(t, id); ok {
		t.Fatal("an unknown agent's claim must not be written")
	}
}

// F18: the same person registering on two machines gives one roster file
// (the id derives from stable_key).
func TestF18_SamePersonTwoMachinesOneRosterFile(t *testing.T) {
	a := newAgency(t)
	server := a.w.Human("alice-server", "alice@northwind.example")
	server.WriteConfig(aliceConfig())
	sc := server.Clone(a.app, "app")
	sc.MustLore("actor", "register")
	n := 0
	for _, f := range a.store.FilesMatching(refMain, folderOrg+"/"+dataDir+"/"+tblActors+"/") {
		if body, _ := a.store.Read(refMain, f); strings.Contains(body, aliceKey) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("one person, one roster file; found %d", n)
	}
}

// F18: handles are unique per roster.
func TestF18_HandleClashIsRefused(t *testing.T) {
	a := newAgency(t)
	dev2 := a.w.Human("dev2", "dev2@northwind.example")
	dev2.WriteConfig(th.Config{Orgs: []th.Org{northwindOrg("human:dev2@northwind.example", "Dev Two", "dev1")}, Live: th.FreshFetch()})
	dev2.Clone(a.app, "app").Lore("actor", "register").Refused(t, "handle")
}

// F18 / §7.2: `lore agent start` registers one agent actor per label with its
// owner on the org roster; nothing reaches the client repo.
func TestF18_AgentStartRegistersAnAgentWithItsOwner(t *testing.T) {
	a := newAgency(t, th.Cmd("agent", "start"))
	ag := startAgent(t, a.alice, a.aliceApp, "frontend-2", "alice")
	if !strings.HasPrefix(ag.Env(th.EnvLoreActor), "agent:") || ag.Env(th.EnvLoreSession) == "" {
		t.Fatalf("agent start must set LORE_ACTOR=agent:… and LORE_SESSION: %q %q", ag.Env(th.EnvLoreActor), ag.Env(th.EnvLoreSession))
	}
	found := false
	for _, f := range a.store.FilesMatching(refMain, folderOrg+"/"+dataDir+"/"+tblActors+"/") {
		body, _ := a.store.Read(refMain, f)
		if strings.Contains(body, "frontend-2") {
			found = true
			if !strings.Contains(body, `"agent"`) || !strings.Contains(body, "owner_actor_id") {
				t.Fatalf("agent actor needs kind agent and owner: %s", body)
			}
		}
	}
	if !found {
		t.Fatal("the agent must be on the org roster")
	}
	if f := a.clientSees(t, "frontend-2"); len(f) != 0 {
		t.Fatalf("registering an agent writes nothing the client can read: %v", f)
	}
}

// ---------------------------------------------------------------- F17 personal

// E12 / F17 / D11: a personal note written in a Northwind repo is filed
// under northwind/ and never loads in a Betalabs repo; a note written where
// no org applies goes to everywhere/ and loads in both.
func TestE12_PersonalNoteStaysInItsOrg(t *testing.T) {
	o := newOrgWorld(t, th.Flag("to", "memory", "add"))
	o.acme.MustLore("memory", "add", "--body", "ask Raj about the staging creds", "--to", "me").Says(t, "northwind")
	o.loose.MustLore("memory", "add", "--body", "I prefer tabs", "--to", "me").Says(t, "everywhere")
	if !personalHas(t, o.alice, orgNorthwind, "ask Raj about the staging creds") {
		t.Fatal("the note must be filed under personal northwind/")
	}
	if !personalHas(t, o.alice, "everywhere", "I prefer tabs") {
		t.Fatal("a note with no org must be filed under personal everywhere/")
	}
	o.beta.MustLore("memory", "list").Says(t, "I prefer tabs").Never(t, "ask Raj about the staging creds")
	o.tools.MustLore("memory", "list").Says(t, "I prefer tabs", "ask Raj about the staging creds")
}

// personalHas reports whether the local personal store's folder holds a row
// containing text (~/.lore/personal/<folder>/.lore/data/**).
func personalHas(t *testing.T, a *th.Actor, folder, text string) bool {
	t.Helper()
	root, err := os.OpenRoot(filepath.Join(a.LorePath("personal"), folder, ".lore", "data"))
	if err != nil {
		return false // no data folder yet: nothing written
	}
	defer func() { _ = root.Close() }() // read-only handle
	found := false
	data := root.FS()
	_ = fs.WalkDir(data, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if b, rerr := fs.ReadFile(data, p); rerr == nil && strings.Contains(string(b), text) {
			found = true
		}
		return nil
	})
	return found
}

// F17 warn rule: a personal note shown in every repo that names a known org
// needs confirmation; without a TTY it is refused unless --yes. Ported from
// `--to me --everywhere` to `--to me` (STORE TARGETS, DECISIONS_LOG 18: no
// everywhere word or flag; `me` is the personal store, one place, its root,
// shown in all your repos), so the personal store here is a 0.1.18 store as
// in Recipe05. Where every repo reads it is checked from a betalabs repo.
func TestF17_EverywhereNoteNamingAKnownOrgNeedsConfirmation(t *testing.T) {
	o := newOrgWorld(t, th.Flag("to", "memory", "add"), th.Flag("yes", "memory", "add"), th.Cmd("store", "init"))
	personal := o.w.Remote(slugPersonal)
	initStore(o.w, o.alice, "personal", personal.URL(), "", "--yes").OK(t)
	cfg := aliceConfig()
	cfg.Personal = &th.Personal{Store: personal.URL()}
	o.alice.WriteConfig(cfg)
	o.acme.Lore("memory", "add", "--body", "betalabs invoices go to finance@", "--to", "me").Refused(t, "betalabs")
	o.beta.MustLore("memory", "list").Never(t, "betalabs invoices")
	o.acme.MustLore("memory", "add", "--body", "betalabs invoices go to finance@", "--to", "me", "--yes")
	o.beta.MustLore("memory", "list").Says(t, "betalabs invoices")
}

// Cookbook 5: personal notes in a git personal store sync between machines.
func TestRecipe05_PersonalNotesSyncBetweenMachines(t *testing.T) {
	o := newOrgWorld(t, th.Flag("to", "memory", "add"), th.Cmd("store", "init"))
	personal := o.w.Remote(slugPersonal)
	initStore(o.w, o.alice, "personal", personal.URL(), "", "--yes").OK(t)
	cfg := aliceConfig()
	cfg.Personal = &th.Personal{Store: personal.URL()}
	o.alice.WriteConfig(cfg)
	o.acme.MustLore("memory", "add", "--body", "remember the VPN on devbox", "--to", "me")
	server := o.w.Human("alice-server", "alice@northwind.example")
	server.WriteConfig(cfg)
	sc := server.Clone(o.remote[slugOwnTools], "tools")
	sc.MustLore("memory", "list").Says(t, "remember the VPN on devbox")
	// STORE TARGETS (DECISIONS_LOG 18): `me` is one place, its root, shown in
	// ALL your repos; the v0.1.17 per-company split (everywhere/ and <org>/)
	// is gone, so the note written in a northwind repo also shows in this
	// betalabs repo of the same person (it only ever renders into private,
	// uncommitted files).
	sb := server.Clone(o.remote[slugBetaAPI], "api")
	sb.MustLore("memory", "list").Says(t, "remember the VPN on devbox")
}

// ---------------------------------------------------------------- F19 migration

// legacyIdentityTOML is ~/.lore/identity.toml as lore wrote it before YAML.
const legacyIdentityTOML = "display_name = \"Alice Doe\"\nstable_key = \"human:alice@legacy.example\"\n"

// The v0.1.17 F19 cases (identity.toml migrated to identity.yaml on first
// read) are ported to OV31-d (CLI_REFERENCE 3.22, P3-ORG-4): identity.toml
// is never read nor migrated (`identity unset` removes it), identity.yaml
// is read below identity: of ~/.lore/config.yaml until `lore migrate run`,
// and `identity show` writes nothing and names each old file still there.

// assertOldFilesUntouched: a show wrote nothing next to the old files.
func assertOldFilesUntouched(t *testing.T, a *th.Actor, present ...string) {
	t.Helper()
	for _, f := range present {
		if _, err := os.Stat(a.LorePath(f)); err != nil {
			t.Fatalf("%s must be left in place by identity show: %v", f, err)
		}
	}
	for _, f := range []string{"identity.toml.migrated", "identity.yaml.migrated"} {
		if _, err := os.Stat(a.LorePath(f)); !os.IsNotExist(err) {
			t.Fatalf("identity show must not migrate (%s exists)", f)
		}
	}
	if !slices.Contains(present, "identity.yaml") {
		if _, err := os.Stat(a.LorePath("identity.yaml")); !os.IsNotExist(err) {
			t.Fatal("identity show must not write identity.yaml")
		}
	}
}

// E13 / OV31-d: upgrading the binary over an old identity.toml: show names
// the file and does not take the identity from it; identity set writes
// config.yaml; identity unset removes the old file.
func TestE13_UpgradeOverOldTomlFiles(t *testing.T) {
	w := newTeamWorld(t)
	a := w.Human("alice", "alice@git.example")
	a.WriteHomeFile(".lore/identity.toml", legacyIdentityTOML)
	a.Lore("", "identity", "show").OK(t).Says(t, "identity.toml is from before v0.1.17 and is not read", "lore identity set").Never(t, "human:alice@legacy.example")
	assertOldFilesUntouched(t, a, "identity.toml")
	a.Lore("", "identity", "set", "Alice Doe <alice@legacy.example>").OK(t)
	a.Lore("", "identity", "show").OK(t).Says(t, "human:alice@legacy.example", "identity.toml is from before v0.1.17 and is not read")
	a.Lore("", "identity", "unset").OK(t)
	if _, err := os.Stat(a.LorePath("identity.toml")); !os.IsNotExist(err) {
		t.Fatal("identity unset must remove a pre-0.1.17 identity.toml")
	}
	a.Lore("", "identity", "show").OK(t).Never(t, "identity.toml", "human:alice@legacy.example")
}

// F19 edge case ported: show is a read, so two at once both succeed with
// the same answer and leave the old file as it was.
func TestF19_Race_TwoProcessesMigrateAtOnce(t *testing.T) {
	w := newTeamWorld(t)
	a := w.Human("alice", "alice@git.example")
	a.WriteHomeFile(".lore/identity.toml", legacyIdentityTOML)
	repo := w.Remote(slugLoose)
	c := a.Clone(repo, "tool")
	res := w.Simultaneously(c.Prepare("identity", "show"), c.Prepare("identity", "show"))
	for _, r := range res {
		r.OK(t).Says(t, "identity.toml is from before v0.1.17 and is not read").Never(t, "human:alice@legacy.example")
	}
	assertOldFilesUntouched(t, a, "identity.toml")
}

// F19 ported: both old files present → the v0.1.17 identity.yaml wins (the
// chain still reads it until `lore migrate run`), the TOML is not read, and
// show names both.
func TestF19_BothFilesYAMLWins(t *testing.T) {
	w := newTeamWorld(t)
	a := w.Human("alice", "alice@git.example")
	a.WriteHomeFile(".lore/identity.toml", legacyIdentityTOML)
	a.WriteHomeFile(".lore/identity.yaml", "display_name: Alice Doe\nstable_key: human:alice@yaml.example\n")
	a.Lore("", "identity", "show").OK(t).
		Says(t, "human:alice@yaml.example", "identity.yaml is from v0.1.17", "lore migrate run", "identity.toml is from before v0.1.17 and is not read").
		Never(t, "human:alice@legacy.example")
	assertOldFilesUntouched(t, a, "identity.toml", "identity.yaml")
}

// F19 ported: a malformed old TOML is never parsed, so show still works,
// names it, and leaves it in place.
func TestF19_MalformedTomlIsNotMigrated(t *testing.T) {
	w := newTeamWorld(t)
	a := w.Human("alice", "alice@git.example")
	a.WriteHomeFile(".lore/identity.toml", "display_name = \"Alice\"\nstable_key = \"unterminated\n")
	a.Lore("", "identity", "show").OK(t).Says(t, "identity.toml is from before v0.1.17 and is not read")
	assertOldFilesUntouched(t, a, "identity.toml")
}

// readOnlyDirMode makes ~/.lore unwritable for F19 (r-x so it can still be
// listed and read); writableDirMode puts it back so the test's temp dir can
// be removed.
const (
	readOnlyDirMode = 0o555
	writableDirMode = 0o755
)

// F19 ported: a read-only ~/.lore changes nothing for show (it writes
// nothing); the old file is still named.
func TestF19_ReadOnlyFolderReadsTomlAndWarns(t *testing.T) {
	w := newTeamWorld(t)
	a := w.Human("alice", "alice@git.example")
	a.WriteHomeFile(".lore/identity.toml", legacyIdentityTOML)
	dir := a.LorePath()
	if err := os.Chmod(dir, readOnlyDirMode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, writableDirMode) })
	a.Lore("", "identity", "show").OK(t).Says(t, "identity.toml is from before v0.1.17 and is not read")
	assertOldFilesUntouched(t, a, "identity.toml")
}

// whoamiField returns the value of one `lore whoami` line ("org", "identity",
// …): the text after the label on the first line starting with it.
func whoamiField(t *testing.T, r th.Result, label string) string {
	t.Helper()
	for _, line := range strings.Split(r.Stdout, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == label {
			return strings.TrimSpace(strings.TrimPrefix(line, label))
		}
	}
	t.Fatalf("whoami printed no %q line: %s", label, r)
	return ""
}
