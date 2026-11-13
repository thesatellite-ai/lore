# Example: Start using lore in an existing project

**In short:** turn the notes file you already have into lore rows, then let lore write that file for you from now on. Do it once per project.

**Scenario:** Alice (an agency developer) works on the client's website at `~/projects/website`. She wants her coding agent to remember what she teaches it. She has an old `CLAUDE.md` (the notes file her agent reads) that she has been editing by hand.

## Session transcript

```
Alice: Let's set up lore for this project.

Agent:  I see ~/projects/website has an existing CLAUDE.md.
        I'll initialize lore, read your CLAUDE.md section by section
        and propose one lore row for each item that is still true.
```

```bash
cd ~/projects/website
lore init --non-interactive
cat CLAUDE.md
```

```
Agent:  Your CLAUDE.md has three items:
          1. "Run the tests before you push"   → a must rule
          2. "Never put passwords in the code" → a must rule
          3. "Buttons are blue"                 → a memory (a fact, not a rule)
        Shall I add them?
Alice:  Yes.
```

```bash
lore rule add --severity must --body "Run the tests before you push"
lore rule add --severity must --body "Never put passwords in the code"
lore memory add --body "Buttons are blue"
lore render
```

```
Agent:  Done. Your notes are lore rows now, and lore writes .lore/LORE.md
        and points CLAUDE.md at it. Going forward, when you correct me,
        I'll capture it with `lore rule add` or `lore memory add`
        and re-render so it persists.
```

## What just happened

- `.lore/` was created; each row is one JSON file under `.lore/data/` (the truth), and `~/.lore/cache/repo/<repo folder>-<hash>.db` is a local cache built from them
- three items from the old CLAUDE.md became two rules and one memory
- `.lore/LORE.md` is written from the rows, and CLAUDE.md points at it
- the cache lives in lore's home, `~/.lore/cache/repo/<repo folder>-<hash>.db` (per machine, never in the repo)

## What to commit

```bash
git add .lore/data .lore/LORE.md CLAUDE.md .gitignore .gitattributes
```

A teammate who clones the repo gets the same notes from `.lore/data`.

## Pitfalls

- Read each item before you add it: a wrong note added here reaches every future session. Leave out what is no longer true.
- Commit the files lore creates (`.lore/` data and the rendered file); a teammate who clones the repo gets the same notes.
