# Keep lore healthy and safe

**In short:** These pages are for the day-to-day care of lore: checking that it is healthy, keeping copies of your notes, seeing who changed what, keeping secrets out of git, running lore in CI, and measuring whether your notes help an agent. Every example on these pages is a real run.

## Pages

| Page | Read it when you want to… |
|---|---|
| [Check lore's health with `lore doctor`](doctor.md) | know if lore is fine in this repo, read its exit codes, look up an error code, or send a support bundle with a bug report |
| [Back up, restore and repair](backup-restore-repair.md) | copy your notes database, go back to a copy, or recover a damaged database (including a rebuild from the files in git) |
| [The audit log](audit-log.md) | see who changed which note and when, and catch changes made outside lore |
| [Secrets and personal data](secrets-and-pii.md) | understand why lore refuses a note, keep one on your machine only, add your own patterns, or use the `LORE_ALLOW_*` safety switches |
| [Use lore in CI](ci.md) | run lore read-only on a build machine, quiet the setup hint and fail the job when lore is not healthy |
| [Plugin allow-list and database settings](plugins-and-db-config.md) | trust a plugin file by its fingerprint, or read and write settings stored in the database |
| [Check that your notes help, with `lore bench`](bench.md) | write test questions with automatic markers and run them against a local model |

## Commands on these pages

| Command | Page |
|---|---|
| `lore doctor`, `lore errors list`, `lore support-bundle` | [doctor](doctor.md) |
| `lore backup`, `lore restore`, `lore repair` | [backup-restore-repair](backup-restore-repair.md) |
| `lore audit log`, `lore audit verify` | [audit-log](audit-log.md) |
| `lore pii-pattern add/list/enable/disable`, `--allow-secrets`, `LORE_ALLOW_ROOT`, `LORE_ALLOW_NETWORK_FS`, `LORE_ALLOW_DB_PATH_OUTSIDE` | [secrets-and-pii](secrets-and-pii.md) |
| `--read-only`, `LORE_READ_ONLY`, `LORE_NO_STALE_WARN`, `lore context --internal` | [ci](ci.md) |
| `lore plugin list/trust/untrust`, `lore config get/set/list` | [plugins-and-db-config](plugins-and-db-config.md) |
| `lore bench eval/run/result/grader/report`, `OLLAMA_HOST` | [bench](bench.md) |

## Related

- [Your first project](../knowledge/first-project.md)
- [Git sync](../sync/README.md)
- [Reference: commands, config, environment, errors](../reference/README.md)
- [Lore for teams](../team/README.md) and its [troubleshooting](../team/troubleshooting.md)
