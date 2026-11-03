# Reference

**In short:** these pages are for looking things up, not for learning step by step. Each one is a set of tables: every command, flag, setting, environment variable and error code, with a plain one-line meaning. They are built from the code and from the real program's output, so they match the version in this repo.

To learn lore step by step, start with the [team guide](../team/README.md) or the guides linked from each row of the [feature index](features.md).

| Page | Look here when you want to know |
|---|---|
| [Feature index](features.md) | what lore can do, one row per feature, with its guide and a runnable example |
| [Command index](commands.md) | which commands exist and what each one does |
| [Flags](flags.md) | what a shared flag such as `--db`, `--json`, `--to` or `--live` means |
| [Config files](config.md) | every key of `~/.lore/config.yaml` and the other settings files, with defaults |
| [Environment variables](environment.md) | every `LORE_*` switch and the other variables lore reads |
| [Error codes and exit statuses](errors.md) | what an `E_…` code means, what to do about it, and which exit status it gives |

## How these pages stay true

- The command index is written by `tests/docs/commands_index.py`, which runs `lore --help` for every command.
- Every command output on these pages is pasted from a real run of `tests/docs/sections/reference.sh` (in a throwaway folder, with a fake git server) by `tests/docs/paste.py`. Nothing is typed by hand.
- The full error table in [errors.md](errors.md#the-full-list-as-lore-prints-it) comes from `lore errors list`.
- Defaults and lists that the code names are marked so a change in the code flags the sentence that describes it.

## Related

- [Team command reference](../team/commands.md): team commands with their flags and examples.
- [Team troubleshooting](../team/troubleshooting.md): fixes for the errors people hit most.
