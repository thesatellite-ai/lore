package wire

// homelayout.go — every entry lore keeps in its home folder (~/.lore),
// with what it is and what happens when a person edits or deletes it
// (plain files sweep 3.1/3.2, 2026-11-12). One table feeds `lore where`
// (where.go, which walks the real folder against it and names anything it
// does not match) and ~/.lore/README.md (homereadme.go), so the map and
// the listing never disagree.
//
// A pattern is a slash path relative to the home; each segment is a
// path.Match pattern, and a last segment "**" covers everything below.
// The first matching entry wins, so the specific ones come first. Collapse
// lists a folder as one line and does not walk into it.

import (
	"path"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
)

// homeEntry is one kind of path in the lore home.
type homeEntry struct {
	// Pattern is the slash path (segments are path.Match patterns).
	Pattern string
	// Dir: the pattern names folders (else files).
	Dir bool
	// Collapse: one line for the folder, its contents not walked.
	Collapse bool
	// What is what it is, in plain words.
	What string
	// ByHand is what happens when a person edits or deletes it.
	ByHand string
	// Readme lists the entry in ~/.lore/README.md (temp and old leftovers
	// are named by `lore where` only).
	Readme bool
}

// Words reused by several entries.
const (
	byHandRebuilt  = "safe to delete: lore makes it again from the row files the next time it needs it"
	byHandTemp     = "safe to delete: a leftover of a command that was cut short"
	byHandLock     = "holds no data; safe to delete while no lore command runs"
	byHandOldLeft  = "left by an older lore after it moved the contents; safe to delete"
	byHandNoEdit   = "do not edit it by hand"
	whatCorruptBak = "a damaged cache file moved aside (lore built a new one); lore removes it once it is a week old"
)

// homeLayout is the lore home, specific entries first.
var homeLayout = []homeEntry{
	{Pattern: HomeReadmeFile, What: "this map of lore's home, written by lore and rewritten when the layout changes", ByHand: "safe to delete: lore writes it again", Readme: true},
	{Pattern: ".README.md.tmp-*", What: "a half-written README.md", ByHand: byHandTemp},
	{Pattern: "config.yaml", What: "this machine's lore settings: setups, orgs, stores (YAML; `lore config check` validates it)", ByHand: "you may edit it; deleting it forgets every setup, org and store on this machine (the stores themselves are not touched)", Readme: true},
	{Pattern: "config.yaml.lock", What: "the lock lore takes while it rewrites config.yaml", ByHand: byHandLock, Readme: true},
	{Pattern: ".config.yaml.tmp-*", What: "a half-written config.yaml", ByHand: byHandTemp},
	{Pattern: "identity", What: "this machine's secret salt for your actor id (never synced)", ByHand: "deleting it makes lore treat you as a new person on this machine", Readme: true},
	{Pattern: "machine.json", What: "this machine's id, kept so it stays the same between runs", ByHand: "deleting it makes lore work the id out again; where it was random, this machine then shows up as a new one", Readme: true},
	{Pattern: "api-token", What: "the token clients of `lore api` send", ByHand: "deleting it makes `lore api` make a new one; clients then need the new token", Readme: true},
	{Pattern: ".api-token-*", What: "a half-written api-token", ByHand: byHandTemp},
	{Pattern: "identity.yaml.migrated", What: "your identity settings from an older lore, already moved into config.yaml; the setup page reads it to tell that move apart from an identity you set yourself", ByHand: "safe to delete once every setup has its own identity (`lore setup list`)"},
	{Pattern: "pii-patterns.txt.migrated", What: "your secret patterns from an older lore, already moved into rows", ByHand: byHandOldLeft},

	{Pattern: "state", Dir: true, What: "machine-local state: never synced, and not rebuildable", ByHand: "do not delete the folder; the files below say what each one holds", Readme: true},
	{Pattern: "state/machine.db", What: "the machine store: rows that only mean something on this machine (agent sessions, runs), the audit chain, doctor results and small local state (usage counts, secret-check times, unsent time entries)", ByHand: byHandNoEdit + "; deleting it loses those rows and the audit chain", Readme: true},
	{Pattern: "state/machine.db-*", What: "SQLite's working files of state/machine.db", ByHand: byHandNoEdit},
	{Pattern: "state/local.db*", What: "the local state's file before it moved into state/machine.db; lore moves it on its next run", ByHand: "leave it: lore moves its rows and removes it"},
	{Pattern: "state/" + "store-clones.json", What: "store clones that are not at their usual place: your own clone (`lore store use`) or a second store with the same name", ByHand: "deleting it makes lore use its own clones under stores/ again (your clone stays where it is)", Readme: true},
	{Pattern: "state/store-addresses.json*", What: "store addresses lore remembers for this machine", ByHand: "deleting it makes lore ask for them again", Readme: true},
	{Pattern: "state/discover-cache.json*", What: "what `lore setup` found on this machine (accounts, keys), kept for a while", ByHand: byHandRebuilt, Readme: true},
	{Pattern: "state/suggest-again.json", What: "setup suggestions you put off", ByHand: "deleting it brings them back", Readme: true},
	{Pattern: "state/import-dismissed.json", What: "import offers you turned down", ByHand: "deleting it brings them back", Readme: true},
	{Pattern: "state/setup-debug.log*", What: "the log of the `lore setup` page (rotated)", ByHand: "safe to delete", Readme: true},
	{Pattern: "state/audit", Dir: true, Collapse: true, What: "the audit log of writes, one .jsonl file per store", ByHand: "deleting a file loses that store's local audit trail", Readme: true},
	{Pattern: "state/batches", Dir: true, Collapse: true, What: "records of coordinated batch writes, so a batch can be undone", ByHand: "deleting them loses the undo of those batches", Readme: true},
	{Pattern: "state/session-start", Dir: true, Collapse: true, What: "when each repo's last agent session started (for \"changed since your last session\")", ByHand: "safe to delete: the next session lists a little more", Readme: true},
	{Pattern: "state/hook-seen", Dir: true, Collapse: true, What: "the rows already sent to an agent in its current session", ByHand: "safe to delete: the agent gets them again", Readme: true},
	{Pattern: "state/" + ReadNoticeDir, Dir: true, Collapse: true, What: "notices a read gave once (a repo not yet wired for lore), so it does not repeat them", ByHand: "safe to delete: the notice shows once more", Readme: true},
	{Pattern: "state/upgrade-check.json", What: "when lore last looked for a new version", ByHand: "safe to delete", Readme: true},
	// B4-20: the record and lock of lore moving old store clones to stores/
	{Pattern: "state/" + gitbackend.MoveRecordFile, What: "what lore moved when it put the store clones of lore 0.1.17 or an earlier build under stores/ (old place, new place, time), so doctor can tell a later change in an old clone", ByHand: "safe to delete once `lore doctor` names no old clone"},
	{Pattern: "state/" + gitbackend.MoveLockFile, What: "the lock lore takes while it moves old store clones", ByHand: byHandLock},
	{Pattern: "state/upgrade.lock", What: "the lock `lore upgrade` takes", ByHand: byHandLock, Readme: true},

	{Pattern: "cache", Dir: true, What: "rebuildable caches: everything here is made again from the row files when deleted", ByHand: byHandRebuilt, Readme: true},
	{Pattern: "cache/**/*.corrupt-*", What: whatCorruptBak, ByHand: "safe to delete"},
	{Pattern: "cache/" + machineCacheFile + "*", What: "the cache of state/machine.db, arranged for search", ByHand: byHandRebuilt, Readme: true},
	// B4-7: 0.1.17 mirrored each store folder's checkout here
	// (stores/<host>/<owner>/<repo>/<branch>/<folder>/.lore/…); a store id
	// never holds a dot, a host does
	{Pattern: "cache/stores/*.*", Dir: true, Collapse: true, What: "lore 0.1.17's store caches (stores/<host>/<owner>/<repo>/…); this lore does not read them", ByHand: "safe to delete; `lore cache prune` removes them"},
	{Pattern: "cache/stores", Dir: true, What: "one cache file per store folder: stores/<store id>/<branch>/<folder>.db (`lore where` names the store and the repos that use each)", ByHand: byHandRebuilt, Readme: true},
	{Pattern: "cache/stores/**", What: "a store folder's cache", ByHand: byHandRebuilt},
	{Pattern: "cache/combined", Dir: true, What: "one cache per repo that attaches more stores than get a file each: combined/<hash of the repo path>.db", ByHand: byHandRebuilt, Readme: true},
	{Pattern: "cache/combined/**", What: "a repo's combined cache", ByHand: byHandRebuilt},
	{Pattern: "cache/" + repoCacheDir, Dir: true, What: "one cache per code repo checkout, of the rows in its .lore/data: " + repoCacheDir + "/<repo folder name>-<short hash of the repo path>.db (kept here so a command never creates a file in the repo)", ByHand: byHandRebuilt, Readme: true},
	{Pattern: "cache/" + repoCacheDir + "/**", What: "a code repo's cache", ByHand: byHandRebuilt},
	{Pattern: "cache/remote", Dir: true, What: "the cache of a repo linked by its remote that holds no .lore/ of its own: remote/<hash of the repo path>.db", ByHand: byHandRebuilt, Readme: true},
	{Pattern: "cache/remote/**", What: "a linked repo's cache", ByHand: byHandRebuilt},
	{Pattern: "cache/uses", Dir: true, What: "one record per repo of the cache files it uses: uses/<hash of the repo path>.json (`lore cache prune` keeps what a repo still uses)", ByHand: "deleting them lets `lore cache prune` remove caches a repo still uses (they are rebuilt)", Readme: true},
	{Pattern: "cache/uses/**", What: "a repo's cache use record", ByHand: "safe to delete"},
	{Pattern: "cache/repos", Dir: true, What: "the cache use records as an older lore kept them; the next command moves them to cache/uses", ByHand: "safe to delete"},
	{Pattern: "cache/repos/**", What: "a repo's cache use record (older layout)", ByHand: "safe to delete"},
	{Pattern: "cache/doctor-health.json", What: "the last `lore doctor` summary, for the setup page", ByHand: "safe to delete", Readme: true},
	{Pattern: "cache/prj_*", What: "a cache from lore 0.1.17; `lore cache prune` removes it", ByHand: "safe to delete"},
	{Pattern: "cache/links-*.json", What: "lore 0.1.17's index of an org's store links; this lore reads the links from the store clones; `lore cache prune` removes it", ByHand: "safe to delete"},

	{Pattern: gitbackend.StoresDir, Dir: true, Collapse: true, What: "the stores lore keeps on this machine: stores/<owner>/<repo>/ is a plain git clone of each store (rows are JSON files you may open and edit; its .git/config holds lore's merge driver and your git identity for that store, user.name and user.email, so your own commits there carry it), stores/" + gitbackend.PersonalFolder + "/ the personal store when it is a plain folder (the _ keeps it apart from any owner folder); `lore where` lists each one", ByHand: "a clone with no unpushed work is safe to delete (lore clones it again); `lore store forget` removes one cleanly", Readme: true},
	{Pattern: "personal", Dir: true, Collapse: true, What: "your personal notes as lore 0.1.17 kept them", ByHand: "your data: do not delete it before it is moved into the personal store", Readme: true},
	{Pattern: "render", Dir: true, What: "private context rendered for repos from internal stores, one folder per repo (imported by that repo's CLAUDE.local.md)", ByHand: "safe to delete: the next `lore render` writes it again", Readme: true},
	// B4-20: lore 0.1.17 wrote one file per project here, imported by the
	// repo's CLAUDE.local.md until the next render points it at render/<repo id>/
	{Pattern: "render/prj_*.md", What: "private context lore 0.1.17 rendered for one project (its CLAUDE.local.md imported it); this lore writes render/<repo id>/ instead", ByHand: "safe to delete once `lore render` (or `lore fix`) ran in that project's repos"},
	{Pattern: "render/*", Dir: true, Collapse: true, What: "private context of one repo (imported by its CLAUDE.local.md)", ByHand: "safe to delete: the next `lore render` writes it again"},
	{Pattern: "logs", Dir: true, What: "logs", ByHand: "safe to delete", Readme: true},
	{Pattern: "logs/hooks.log*", What: "errors of agent hooks (`lore context --hook`), which must never print into the agent; capped, one older copy kept", ByHand: "safe to delete", Readme: true},
	{Pattern: "tmp", Dir: true, Collapse: true, What: "scratch space for setup probes; lore removes what it left there after a day, and the folder once it is empty", ByHand: "safe to delete while no lore command runs", Readme: true},
	{Pattern: "clones", Dir: true, Collapse: true, What: "store clones of lore 0.1.17 (<host>/<owner>/<repo>.git, a bare clone) or of an earlier 0.1.18 build (now under stores/; lore moves them itself)", ByHand: "safe to delete once `lore doctor` reports no unpushed work in it (`lore fix` removes the ones doctor proved safe)"},
	{Pattern: "clones-v017", Dir: true, Collapse: true, What: "second store clones of an earlier 0.1.18 build (no longer used)", ByHand: "safe to delete"},
	{Pattern: "worktrees", Dir: true, Collapse: true, What: "browse copies `lore store open` made in an earlier 0.1.18 build (it now prints the store's clone)", ByHand: "safe to delete"},
}

// matchHome is the first homeLayout entry for rel (a slash path relative
// to the home) and whether one matched; dir says rel is a folder.
func matchHome(rel string, dir bool) (homeEntry, bool) {
	for _, e := range homeLayout {
		if e.Dir == dir && matchPattern(e.Pattern, rel) {
			return e, true
		}
	}
	return homeEntry{}, false
}

// matchPattern reports whether rel matches pattern ("**" as a segment
// matches any number of segments, none included).
func matchPattern(pattern, rel string) bool {
	return matchSegs(strings.Split(pattern, "/"), strings.Split(rel, "/"))
}

func matchSegs(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			for i := 0; i <= len(segs); i++ {
				if matchSegs(rest, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], segs[0]); err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}
