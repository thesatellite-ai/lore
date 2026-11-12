package rowfs

// readme.go — the README.md lore writes once into each store (plain-files
// round, brief 1.3, D-f): what the folders are, how to browse them, how to
// edit a row by hand, what lore checks and what it keeps for itself. It is
// written when lore creates a store, or by the first lore commit in a
// store that has none (the git backend adds it to that commit; a read
// never writes it). A person's README is never overwritten; one that is
// byte for byte an earlier lore's text (earlierReadmes) is replaced with
// the current text in lore's next commit there.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/model/errs"
)

// ReadmeFile is the store README's name at the store's top.
const ReadmeFile = "README.md"

// readmeMode: committed and read by everyone who clones the store.
const readmeMode = 0o644

// StoreReadme is the README.md text lore writes into a store. One physical
// line per paragraph, bullet or table cell (never hard-wrapped: a person's
// editor wraps it; internal/mdwrap checks it in readme_test.go).
const StoreReadme = `# A lore store

This folder is a lore store: notes, rules, decisions, tasks and the other records a team keeps with lore. Every record is one small JSON file, so you can open, read and search the store with any editor, file browser or git tool. lore wrote this file; edit or replace it as you like, and lore leaves your version alone.

## What the folders are

- ` + "`lore-store.yaml`" + `: the store's identity (its id, who may see its rows).
- ` + "`_store/`" + ` holds the store's own records, the ones that belong to the whole store and to no project (actors, machines, store-wide rules and so on), one folder per kind of record: ` + "`_store/actors/act_….json`" + `. Its name starts with ` + "`_`" + `, which no project folder may, so it is never mistaken for one.
- Every other folder is a project folder (a client, a project, a team) or the org folder, each holding one folder per kind of record, one file per record: ` + "`acme/tasks/tsk_….json`" + `, ` + "`acme/memories/mem_….json`" + `. Folders can nest (` + "`team/web/tasks/`" + `). A folder may not be named like a kind of record (` + "`tasks`" + `, ` + "`memories`" + `, ` + "`rules`" + `, ...) and may not start with ` + "`_`" + ` or ` + "`.`" + `.
- ` + "`_meta.json`" + ` in a folder holds the folder's id; ` + "`_purged.json`" + ` lists records removed for good. Leave both to lore.
- ` + "`org/`" + ` (when the store has one) holds the company's people, teams and rules, and ` + "`org/lore-org.yaml`" + `, the company's settings.

## Browsing

Open any file: the record's text is in plain JSON fields (` + "`title`" + `, ` + "`body`" + `, ` + "`content`" + `, ...). From a terminal inside the store, ` + "`lore task list`" + ` (or any other lore list) shows the records of the folder you are in, and ` + "`lore path <id>`" + ` prints the file of one record.

## Editing by hand

- ` + "`lore edit <id>`" + ` opens a record in your editor as readable Markdown and writes it back as JSON when you save.
- You may also edit a JSON file directly, add one, delete one, or move one to another folder. On its next command in this store lore checks every file you changed: a valid edit is stamped with your name and the time and committed as you ("hand edit: tasks tsk_…"), so it wins over older changes when teammates sync, and pushed like any lore write; a deleted file goes to lore's trash (` + "`lore trash restore <id>`" + ` brings it back); a file moved to another folder is moved there. A file that is not valid is refused with its line and the reason, and it is not committed or pushed until it is fixed; your other edits still go.
- Plain git works here: ` + "`git pull`" + `, ` + "`git commit`" + ` and ` + "`git push`" + `. lore sets this clone up for it: ` + "`merge.lore.driver`" + ` in ` + "`.git/config`" + ` (records merge field by field), ` + "`*.json merge=lore`" + ` in ` + "`.git/info/attributes`" + `, and a block in ` + "`.git/hooks/pre-commit`" + ` that checks the records and scans for secrets before your commit (lines of your own in that hook are kept). Only when a global ` + "`core.hooksPath`" + ` would skip the clone's hooks does lore set this clone's own ` + "`core.hooksPath`" + ` back to ` + "`.git/hooks`" + `.
- Files that are not records (notes, a folder of your own) are left alone: lore never deletes or reads them, and ` + "`lore doctor`" + ` names them.

## What lore checks

Before it commits anything here lore checks that each file is a valid record of its kind, that the records it points at exist, that no secret (a key, a token, a password) is in it, and that it is allowed in this store. When two people change the same record lore merges it field by field.

## What lore keeps for itself

lore's lock, its pending audit lines and when it last fetched live in ` + "`.git/lore/`" + ` (or ` + "`.lore-state/`" + ` in a store that is a plain folder). They are never committed. You can delete them when no lore command is running.
`

// earlierReadmes are the SHA-256 sums (of the text with CRLF read as LF)
// of every README.md an earlier lore wrote into stores. A store README
// byte-identical to one of them is still lore's own text, never a
// person's: lore replaces it with StoreReadme in its next commit there.
// Add the old sum here whenever StoreReadme changes.
var earlierReadmes = map[string]bool{
	"1411646ffc3a0f2f4cc7ed0ce14d14b354bef348cfc2f9ba5df56881bb758de5": true, // 0.1.18-dev, first store layout
	"191fb469e48e1f0ba24c08f67b9ea9eabd42f22590bdf5adf24f7cf681630474": true, // 0.1.18-dev, hard-wrapped, root rows at the store top
}

// readmeSum is the sum earlierReadmes keys: SHA-256 of b with CRLF read as LF.
func readmeSum(b []byte) string {
	sum := sha256.Sum256(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")))
	return hex.EncodeToString(sum[:])
}

// ReadmeOutdated reports whether the store at dir has a README.md that is
// an earlier lore's text, byte for byte (earlierReadmes): lore may replace
// it. A person's own README, or lore's text edited by anyone, is never
// outdated.
func ReadmeOutdated(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, ReadmeFile))
	return err == nil && earlierReadmes[readmeSum(b)]
}

// EnsureReadme writes StoreReadme to <dir>/README.md when the store has no
// README.md, or has one that is an earlier lore's text (ReadmeOutdated),
// and reports whether it wrote. Any other README.md, a person's own
// included, is kept as it is. dir is the store's top.
func EnsureReadme(dir string) (bool, error) {
	p := filepath.Join(dir, ReadmeFile)
	if _, err := os.Lstat(p); err == nil {
		if !ReadmeOutdated(dir) {
			return false, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, errs.FileIO("read", p, err)
	}
	if err := writeAtomic(p, []byte(StoreReadme), fileModes{file: readmeMode, dir: sharedDirMode}); err != nil {
		return false, err
	}
	return true, nil
}

// HasReadme reports whether the store at dir has a README.md (lore doctor
// notes a store without one).
func HasReadme(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ReadmeFile))
	return err == nil
}
