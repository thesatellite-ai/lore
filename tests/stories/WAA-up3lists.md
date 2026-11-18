# WAA — every list, context, store doctor and a bad id from the home folder (owner use pass 3)

**Who.** Ada, the admin of the org `acme` (made-up names), on one laptop with one setup `acme-admin` covering `~/code/**`, working from the home folder, which no repo, store clone or setup folder answers for.

**Why.** The third use pass found: 11 list commands (playbook, pattern, prompt, handoff, incident, snapshot, suggestion, architecture note, behaviour, cookbook recipe, repo) that failed `--setup <setup>` from the home folder with "no store named repo is attached" while rule and memory list answered; `lore context --setup` refused "not inside a git work tree" although its own hint named `--setup`; `lore store doctor --store <org>` was "unknown flag"; `lore show --id mem_bogus` said "not inside a git work tree" instead of naming the bad id.

**Test.** `TestStoryWAAUp3Lists` (tests/stories/WAA_up3lists_test.go).

## Steps

1. Day 1: Ada starts `acme` from `~/code/platform`, adds the folder `acme/sync` and, from the home folder with `--setup acme-admin`, writes a playbook, a tag `deploy` and a rule tagged `deploy` there.
2. D-631: every row kind's list in the registry (`<kind> list`, generated from the op registry so a new kind is walked the day it registers) answers `--setup acme-admin` from the home folder; `playbook list`, `playbook show --id` and `playbook search` find the playbook; a plain `playbook list` in `~/code` (the setup's folder, no repo) reads the default setup.
3. D-632: `lore context --topics`, `--topic deploy` and `--for-prompt` with `--setup acme-admin` answer from the home folder and name the tag and the rule; the plain `lore context --topics` refusal names commands that work where printed.
4. D-633: `lore store doctor --store acme` checks acme's stores, with no "unknown flag".
5. D-634: `lore show --id mem_bogus`, `lore memory show --id mem_bogus` (also with `--setup`) exit 20 `E_INVALID_ID` saying the id is not a valid lore id and its shape `mem_<32 hex>`, never "git work tree".
