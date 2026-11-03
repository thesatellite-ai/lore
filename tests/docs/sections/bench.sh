# bench: test questions that check whether your notes help (docs/operations/bench.md).
# Alice (an agency developer) writes one test question linked to a rule, checks its
# grader against two sample answers, exports, imports and archives questions, and looks
# at the empty run and report lists. Nothing here calls a model or the network:
# `lore bench run start` needs a model and is shown on the page but not run here.

lore_repo alice client/website
quiet alice work/website "lore rule add --body 'Run the tests before you push' && git add -A && git commit -qm 'lore: first rule'"
on alice work/website "lore rule list"
local rul; rul=$(id rul)

note "eval add"
on alice work/website "lore bench eval add --code TESTS-001 --category rule-trigger --prompt 'Write a short checklist for pushing a change.' --grader-cmd 'grep -qi test \"\$OUTPUT_FILE\"' --link rule:$rul --notes 'Passes when the answer says to run the tests'"
on alice work/website "lore bench eval list"
on alice work/website "lore bench eval show TESTS-001"

note "grader test"
on alice work/website "printf '1. Run the tests\n2. Push the branch\n' > good-answer.txt"
on alice work/website "printf 'Just push it.\n' > bad-answer.txt"
on alice work/website "lore bench grader test TESTS-001 --output-file good-answer.txt"
on alice work/website "lore bench grader test TESTS-001 --output-file bad-answer.txt"

note "grader stdin trap"
on alice work/website "lore bench eval duplicate TESTS-001 --as TESTS-002"
on alice work/website "lore bench eval edit TESTS-002 --grader-cmd 'grep -qi test'"
on alice work/website "lore bench grader test TESTS-002 --output-file good-answer.txt"

note "export import"
on alice work/website "lore bench eval export --to evals"
on alice work/website "ls evals && cat evals/TESTS-001.yaml"
on alice work/website "sed -i 's/short checklist/three-step checklist/' evals/TESTS-001.yaml"
on alice work/website "lore bench eval import --from evals"
on alice work/website "lore bench eval import --from evals --replace"
on alice work/website "lore bench eval list"

note "archive delete"
on alice work/website "lore bench eval archive TESTS-002"
on alice work/website "lore bench eval list"
on alice work/website "lore bench eval list --include-archived"
on! alice work/website "lore bench eval delete TESTS-002"
on alice work/website "lore bench eval delete TESTS-002 --confirm"

note "no runs yet"
on alice work/website "lore bench run list"
on alice work/website "lore bench result list"
on! alice work/website "lore bench report summary"
on alice work/website "lore bench report trend"
on alice work/website "lore bench grader audit"
quiet alice work/website "rm -rf evals good-answer.txt bad-answer.txt"
