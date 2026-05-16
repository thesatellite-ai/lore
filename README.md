# lore

Local-first **knowledge & context store for AI coding agents — and humans**. It
captures project knowledge (rules, memories, decisions, hotfixes, patterns,
snapshots, playbooks, tasks), retrieves the relevant parts with hybrid + FTS5
search, and compiles compact context files (`CLAUDE.md`, `AGENTS.md`,
`.cursorrules`, …) that every future AI session reads.

Single static binary. Pure-Go SQLite — no CGO, no external services, nothing
to run. Ships with a Claude Code skill so the agent writes knowledge back
automatically.

## Install

### Homebrew (macOS / Linux)

```sh
brew install thesatellite-ai/tap/lore
```

### Script (macOS / Linux)

```sh
curl -sL https://raw.githubusercontent.com/thesatellite-ai/lore/main/install.sh | sh
```

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/thesatellite-ai/lore/main/install.ps1 | iex
```

The script/Homebrew install also drops the Claude skill at
`~/.claude/skills/lore`. Or grab a tarball from
[Releases](https://github.com/thesatellite-ai/lore/releases) and put `lore` on
your `PATH` (the `skills/` folder in the archive is the skill bundle).

## Quick start

```sh
cd your-project

lore init                # create .lore/ + sqlite db
lore setup               # build the FTS5 search index
lore directive install   # add the agent-directive block to CLAUDE.md / AGENTS.md

# capture knowledge
lore memory add --body="use Tailwind v4, not v3"
lore rule add --body="never commit without asking"

# retrieve it
lore search "tailwind"
lore render              # regenerate CLAUDE.md from stored knowledge

lore version
```

`lore --help` for the full command set; `lore tui` for the interactive UI.

## The Claude skill

The skill teaches Claude Code when and how to write knowledge back to lore (on
corrections, decisions, "remember this", etc.). It installs to
`~/.claude/skills/lore` — restart Claude Code to load it.

## How it works

| Layer | Detail |
|-------|--------|
| Storage | One SQLite file under `.lore/`, pure-Go `modernc.org/sqlite` |
| Search | SQLite FTS5 (built into the binary) + hybrid ranking |
| Output | Compiles stored knowledge into `CLAUDE.md` / `AGENTS.md` / `.cursorrules` |
| Agent loop | Claude skill captures corrections & decisions back into the db |

## Building from source / contributing

See **[docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)** (build, release, Homebrew tap)
and **[CONTRIBUTING.md](CONTRIBUTING.md)**.

## License

`lore` is source-available under the **[PolyForm Perimeter License
1.0.0](LICENSE)**.

You **may**: use it freely — personally *and* commercially — copy, modify, build
on it, and redistribute it, with the copyright notice kept intact.

You **may not**: use it to provide a **product that competes with `lore`** —
i.e. fork/rebrand/repackage it and ship it as a substitute for `lore`'s
functionality or value (the "Cursor-from-VSCode" scenario), in any form, even
free of charge.

This license is **perpetual — it does not convert to MIT/Apache or any other
license over time**. See [LICENSE](LICENSE).
