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
| [Local REST API](rest-api.md) | how to serve lore over HTTP on your own machine with `lore api`, call it with the token, rotate the token, and regenerate the reference with `lore spec` |
| [Command and error reference](cli-commands.md) | every command with its flags, REST route, access level and error codes, and every error code with its class and HTTP status (generated) |
| [OpenAPI document](openapi.json) | the same routes as an OpenAPI file, for HTTP client generators (generated) |

## Which page answers which question

- **"What does this flag do?"** Start with [Flags](flags.md) for the flags many commands share (`--json`, `--to`, `--live`, `--yes`, `--dry-run`). For a flag only one command has, run `lore <command> --help`; the same text is in the [command and error reference](cli-commands.md).
- **"Where is that setting?"** [Config files](config.md) lists every key of `~/.lore/config.yaml` (identity, orgs, setups, live rows, leases, agents, machine policies, your own tool profiles) and the files lore keeps in a repo or a store, each with its default. `lore config check` reads your file and names every mistake; the page shows a good file, a bad one and what each rule means.
- **"Can I change this for one command?"** [Environment variables](environment.md) are the switches for one shell or one command: read-only mode, live rows off, no network, which org or setup, a person's terminal inside an agent session (`LORE_HUMAN`). Each one is shown in front of a real command.
- **"What does this error mean?"** [Error codes](errors.md) groups the codes by area (tasks, trash, stores, metadata, references, teams, setup), says what to do for each, and shows a real refused command for the common groups. Scripts and agents should match on the code, never on the message text.
- **"Can a program talk to lore without a shell?"** [Local REST API](rest-api.md): `lore api` listens on `127.0.0.1` only, needs the token in `~/.lore/api-token`, and runs the same operations with the same checks as the commands.

## How these pages stay true

- The command index is written by `tests/docs/commands_index.py`, which runs `lore --help` for every command.
- Every command output on these pages is pasted from a real run of `tests/docs/sections/reference.sh`, `environment.sh` and `api.sh` (in a throwaway folder, with a fake git server and a server on a free local port) by `tests/docs/paste.py`. Nothing is typed by hand.
- The full error table is in [cli-commands.md](cli-commands.md#errors), written by `lore spec docs` from the code.
- Defaults and lists that the code names are marked so a change in the code flags the sentence that describes it.

## Related

- [Feature index](features.md): one row per feature, with the guide that teaches it.
- [Your first project](../knowledge/first-project.md): the step-by-step start these pages assume.
- [Doctor](../operations/doctor.md): when something is wrong and you do not have an error code yet.

- [Team command reference](../team/commands.md): team commands with their flags and examples.
- [Team troubleshooting](../team/troubleshooting.md): fixes for the errors people hit most.
