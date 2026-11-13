# Example: My cache is broken

**In short:** lore's local cache `~/.lore/cache/repo/<repo folder>-<hash>.db` is only a copy built from the files in `.lore/data`. If it breaks (for example after a power cut), lore moves it aside and builds a new one by itself; if you delete it, the next command builds it again. Keep `.lore/data` committed and nothing shared can be lost.

**Scenario:** Bob (an agency developer) opens his project and a command reports `E_DB_CORRUPT`. The power went out while lore was writing.

## Triage

```bash
lore doctor
lore rule list    # shared notes still list: they are read from .lore/data
```

A cache file that cannot be opened at all is moved aside beside itself (`~/.lore/cache/repo/<repo folder>-<hash>.db.corrupt-<time>`) and rebuilt without any action. `E_DB_CORRUPT` means the file opens but holds a record lore cannot read.

## Rebuild the cache from the files

```bash
rm -f ~/.lore/cache/repo/<repo folder>-*
lore doctor | head -1    # the first command rebuilds the cache
```

Nothing else is lost: what lore keeps on this machine only (the audit log, sessions, this machine's settings) lives in `~/.lore/state/machine.db`, not in the repo's cache.

## If `.lore/data` itself is gone

The files are in git:

```bash
git checkout HEAD -- .lore/data
lore doctor
```

A file deleted in an earlier commit comes back from the commit before it (`git log --diff-filter=D -- .lore/data/<table>/<id>.json`). A row deleted with lore is in the trash: `lore trash list`, `lore trash restore --id <id>`.

## Prevention

Commit `.lore/data` with your code. That is the backup: every clone holds a full copy, and any of them can rebuild its cache.

```bash
git status --porcelain -- .lore/data   # empty = everything is in git
```
