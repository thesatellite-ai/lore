# Story I — one person on two machines; text and environment edge cases

Dev-company round 3, track I. Replayed by `TestStoryITwoMachines`, `TestStoryIText` and `TestStoryIEnvironment` in `I_two_machines_text_test.go` (build tag `stories`).

## Who

- A CTO with two laptops: two HOMEs with the same git identity.
- A developer with a laptop and a CI-like container.
- A contractor who works in a client-style repo.
- Repos, generated: a Go API (format 1), a Go library (format 1), and two repos with no lore data (an Astro-style site and a small JS app).

## Steps

1. The CTO writes on laptop A and syncs; writes on laptop B without syncing; both go offline; the same repo note and the same store note are edited on both; both come back. The repo note's `git pull` stops on a conflict that keeps both edits in the row file, `lore memory show` shows both sides and `lore doctor` counts the open conflict until the person settles it with `lore memory edit`; the store note keeps one edit and the other is listed by `lore sync conflicts`; nothing is lost, `lore whoami` shows one actor on both; `lore doctor` counts the open store conflicts and names the command that resolves them; sync's received and sent counts are right; doctor's Team versions names actors `lore actor show` can find.
2. The admin's second laptop can join as an admin.
3. Text: titles and bodies with accents, CJK, emoji with ZWJ sequences, Persian and Hindi with ZWNJ/ZWJ, RTL text, a 300-character title, a 1 MB body, markdown with tables and code fences, CRLF line endings. `show --json` gives back exactly what was stored after lore's documented cleanup (CRLF and CR become LF, NFC, bidi controls dropped, ZWJ and ZWNJ kept; `docs/knowledge/rules-memories-decisions.md`), `show` prints them; render never hard-wraps; `--json` output is valid UTF-8; CJK words inside a sentence and emoji are found by search; a search hitting the 1 MB body (pasted through `lore row markdown` and `lore edit --file`, since one argument cannot hold 1 MB) stays under 5 s; control characters (ESC) print escaped, never raw.
4. Environment: clones with `core.autocrlf=true` and `input` keep row files and LORE.md byte-stable (no endless diffs); a plain read on a fresh clone writes no tracked file; two machines with `TZ=Asia/Kolkata` and `TZ=America/Los_Angeles` see due dates and reminders in their own local date; `LANG=C` reads and searches accented text; `LANG=tr_TR.UTF-8` (Turkish dotted and dotless i fold in search); a repo path with spaces and non-ASCII (`my repo ü`; a HOME path with spaces is covered by `tests/machines`); `umask 077` and `002` give the modes a shared group checkout expects; `member add --handle chloé` keeps the accent and `@chloe` resolves.
5. Adopting lore in the JS app by the contractor's client-style flow and by the CTO as their own repo: what is written where is as documented; a new row written under umask 002 is group-writable; init (in a second clone of the site) writes its pointer once when `CLAUDE.md` imports `@AGENTS.md`; the untouched site clone stays byte-identical. The client-repo checks (`lore where` on a live client clone, a refused client write never hinting `lore init`) live in story F and `TestJourneyClientRepoNoInit`, since this story links no repo to an org.

## What must hold

- Nothing lost between the two machines; one actor per person.
- Text round-trips exactly; nothing a teammate writes can drive the reader's terminal.
- Standing rule 1: no remote moves without a push or sync.
- Standing rule 2: the untouched repo stays byte-identical.
- Standing rule 3: format-1 repos stay format 1.
- Standing rules 4, 5, 6, 7; no fake secret value printed.

## Findings this story guards

| Finding | Round-3 id | What | Guarded by |
|---|---|---|---|
| D-117 | r3 I-4 | write path strips ZWJ/ZWNJ | `TestStoryIText`; `TestJourneyTextSafety` |
| D-118 | r3 I-5 | search misses CJK words inside sentences | `TestStoryIText`; `TestJourneyTextSafety` |
| D-119 | r3 I-6 | search ~10 s when a hit is a 1 MB body | `TestStoryIText` |
| D-133 | r3 I-7 | a read on a fresh clone writes tracked rows | `TestStoryIEnvironment`; `TestJourneyPullNeverBlocked` |
| D-150 | r3 I-16 | control characters drive the reader's terminal | `TestStoryIText`; `TestJourneyTextSafety` |
| D-120 | r3 I-1 | doctor HEALTHY with open store conflicts | `TestStoryITwoMachines` |
| D-121 | r3 I-2 | sync received/sent counts wrong | `TestStoryITwoMachines` |
| D-122 | r3 I-3 | doctor Team versions names an unknown actor | `TestStoryITwoMachines` |
| D-153 | r3 I-17 | the admin's second laptop joins only as a member | `TestStoryITwoMachines` |
| D-136 | r3 I-8 | lists show UTC dates, not the viewer's | `TestStoryIEnvironment` |
| D-137 | r3 I-9 | search case folding not Turkish-aware | `TestStoryIEnvironment` |
| D-138 | r3 I-10 | `--handle chloé` stored as `@chlo` | `TestHandleKeyFoldsAccents`, `TestPersonHandleFoldsAccents` (story I has no team roster) |
| D-139 | r3 I-11 | umask: group cannot read or edit rows | `TestStoryIEnvironment` |
| D-145 | r3 I-12 | `lore where` labels live client caches gone | `TestStoryFClientRepo`; `TestJourneyClientRepoNoInit` |
| D-146 | r3 I-13 | client refusal hints `lore init` | `TestJourneyClientRepoNoInit`; `TestClientRepoHintsNeverSayInit` |
| D-147 | r3 I-14 | init points both CLAUDE.md and AGENTS.md | `TestStoryIEnvironment` |
