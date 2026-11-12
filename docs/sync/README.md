# Notes that travel with git

**In short:** lore saves your notes (rules, memories, decisions) as small files inside your repo, in `.lore/data`. You commit them with your code, so they follow your branches, reach main when a pull request merges, and arrive on every teammate's machine with `git pull`. These pages show how that works and how to fix it when two people change the same note.

## Who these pages are for

You use lore in a git repo and you want to know what happens to your notes when you branch, merge, pull or open a pull request. If you work alone and never branch, you can skip most of this: lore does it all by itself.

The examples follow Alice and Bob (two agency developers) in the client's repo `github.com/client/website`. The agency builds the client's website. Every command and every output on these pages comes from a real run (see [Where the examples come from](#where-the-examples-come-from)).

## The pages

| Page | Read it when |
|---|---|
| [How notes travel with git](how-notes-travel-with-git.md) | You want the big picture: what `lore init` adds, what to commit, what `lore sync` does, and how a teammate gets your notes. Start here. |
| [Branches: peek and promote](branches-peek-promote.md) | You want to see a teammate's notes on their branch without switching to it, or move one urgent rule onto main now. |
| [Conflicts and duplicates](conflicts-and-duplicates.md) | Two people changed the same note, the same fact was written twice, or two people set up lore on separate branches. |
| [Trash, restore from git and purge](trash-and-purge.md) | A note disappeared, `.lore/data` was deleted, or you want old archived notes gone for good. |
| [Editing by hand](editing-by-hand.md) | You opened a store's row files in a text editor, deleted or moved one, or used plain `git pull` / `git commit` in a store clone, and want to know what lore does with it. |
| [Hooks and the merge driver](hooks-and-merge-driver.md) | You want to know what lore adds to `.git/hooks` and `.gitattributes`, and why a commit was refused. |
| [The GitHub workflow](github-workflow.md) | Pull requests on GitHub show a conflict in `.lore/data` that lore could have merged. |
| [Projects and repos](multi-repo-projects.md) | You want notes that apply to one part of the code only, or you want to see and tidy the projects in a database. |
| [Your stores: list, move, retire](stores.md) | You keep notes in a store next to the client's repo and want to see its stores, move a note into one, follow a store that moved, split it into folders or detach it. |
| [One store for several repos](shared-database-mode-b.md) | A client's product is several repos and you want them to share notes through one folder store on your machine, or a repo still uses the old shared database. |

## Two words you will see everywhere

- A **row** is one note: one rule, one memory, one task. Think of one card in a card box.
- **`.lore/data`** is the folder in your repo that holds one small text file (JSON) per row. git tracks it like any other code. The database `.lore/lore.db` is a fast copy of those files that stays on your machine; if you delete it, lore builds it again from the files.

## What these pages do not cover

Some notes do not wait for a commit. Tasks, for example, are *live* by default: lore shares them at once through a hidden list on GitHub that is not a branch (a "ref"), so a teammate sees a new task straight away. Live rows, stores (private notes kept outside the client's repo) and teams have their own pages:

- [Lore for teams](../team/README.md), with [Concepts](../team/concepts.md) and [How it works](../team/how-it-works.md)
- [Getting started with teams](../team/getting-started.md)

## Where the examples come from

The examples are run by `tests/docs/run-examples.sh sync`, `tests/docs/run-examples.sh projects` and `tests/docs/run-examples.sh stores`, in throwaway folders with a fake GitHub made of plain git repos on disk. Nothing touches a real account or the network. The script writes what each command printed to `tests/docs/transcripts/`, and the pages paste from there, so the output you see is the output lore really prints.

## Related

- [Feature index](../reference/features.md): every lore feature with its guide and example.
- [Your first project](../knowledge/first-project.md): set up lore in one repo, no team.
