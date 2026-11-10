# Keep lore healthy and safe

**In short:** These pages are for the day-to-day care of lore: checking that it is healthy, rebuilding a damaged cache, seeing who changed what, keeping secrets out of git, recording your servers and domains, upgrading and running lore in CI. Every example on these pages is a real run.

## Pages

| Page | Read it when you want to… |
|---|---|
| [Check lore's health with `lore doctor`](doctor.md) | know if lore is fine in this repo, read its exit codes, look up an error code, or send a support bundle with a bug report |
| [Check every setup with `lore doctor --all`](doctor-all.md) | see every setup's identity, GitHub key, stores, repos and tools at once, and repair what fails |
| [A damaged cache](backup-restore-repair.md) | rebuild the local cache and the search index from the files in git |
| [The audit log](audit-log.md) | see who changed which note and when, and catch changes made outside lore |
| [Secrets and personal data](secrets-and-pii.md) | understand why lore refuses a note, keep a secret in your password manager and only its name in the note, check, rename or hand over those references, add your own patterns, or use the `LORE_ALLOW_*` safety switches |
| [Use lore in CI](ci.md) | run lore read-only on a build machine, quiet the setup hint and fail the job when lore is not healthy |
| [Your settings and team rules](plugins-and-db-config.md) | read and write your settings in `~/.lore/config.yaml`, or set a rule for the whole team |
| [Resources: servers, domains and the services on them](resources.md) | record your infrastructure as typed rows, see what depends on a server, import and export them, and keep vault references instead of secrets |
| [Upgrade lore](upgrade.md) | install a new release, pin one version for the team or CI, or turn off the daily new-release line |

## Commands on these pages

| Command | Page |
|---|---|
| `lore doctor`, `lore support-bundle` | [doctor](doctor.md) |
| `lore doctor --all`, `lore setup repair` | [doctor-all](doctor-all.md) |
| `lore search rebuild`, rebuilding `.lore/lore-cache.db` | [backup-restore-repair](backup-restore-repair.md) |
| `lore audit log`, `lore audit verify` | [audit-log](audit-log.md) |
| `lore secrets list/show/check/link/move/relabel/rename-entry`, `lore secrets allow` (list, rm), `lore secrets rule add/edit/off/on`, `lore secrets rules`, `lore pii-pattern add/list/enable/disable`, `--secret-ref` / `--secret-unstored` / `--not-a-secret` on a secret warning, `LORE_ALLOW_ROOT`, `LORE_ALLOW_NETWORK_FS`, `LORE_ALLOW_DB_PATH_OUTSIDE` | [secrets-and-pii](secrets-and-pii.md) |
| `--read-only`, `LORE_READ_ONLY`, `lore context --internal` | [ci](ci.md) |
| `lore config get/set/list`, `lore policy set/show` | [plugins-and-db-config](plugins-and-db-config.md) |
| `lore resource` (every subcommand: add, list, show, edit, graph, impact, check, stale, clone, rename, import, export, repoint, merge, archive, delete, lint, report, history, adopt, rehome, kinds, kind …, field …) | [resources](resources.md) |
| `lore upgrade` (`--check`, `--version`, `--ignore-pin`), `lore_version_pin`, `LORE_VERSION_PIN`, `LORE_NO_UPGRADE_NOTICE`, `upgrade_notice` / `auto_upgrade` | [upgrade](upgrade.md) |

## lore EE

The guided setup page and its Doctor page, with Repair and the Debug log, ship with lore EE (the enterprise build). Every check on them is also on the command line: [`lore doctor --all`](doctor-all.md).

## Related

- [Your first project](../knowledge/first-project.md)
- [Git sync](../sync/README.md)
- [Reference: commands, config, environment, errors](../reference/README.md)
- [Lore for teams](../team/README.md) and its [troubleshooting](../team/troubleshooting.md)
