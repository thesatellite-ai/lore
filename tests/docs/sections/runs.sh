# runs: a step-by-step log of one piece of work (docs/tasks/run-log.md). Alice (an agency
# developer) logs how a typo on the client's pricing page was found and fixed, replays it,
# cancels a second run and lists sessions.

lore_repo alice client/website
W=work/website

note "task"
on alice $W "lore task add 'Fix the typo on the pricing page'"
local t; t=$(id tsk)

note "run start"
on alice $W "lore run start --task $t --agent manual --goal 'Find and fix the wrong price'"
local r; r=$(id run)

note "run step"
on alice $W "lore run step $r --kind tool --name grep --summary 'Found 9 instead of 19 in pricing.html'"
on alice $W "lore run step $r --kind decide --summary 'Change pricing.html only; the price list file is right'"
on alice $W "lore run step $r --kind verify --name tests --passed --summary 'The page tests pass'"
on alice $W "printf 'Changed line 42 of pricing.html\nfrom: 9 a month\nto:   19 a month\n' | lore run step $r --kind note --name diff --summary 'What changed' --payload-stdin"

note "run end"
on alice $W "lore run end $r --outcome success --summary 'The price now says 19'"

note "run show"
on alice $W "lore run show $r"

note "run replay"
on alice $W "lore run replay $r"

note "run cancel"
on alice $W "lore run start --agent manual --goal 'Try a new logo colour'"
local r2; r2=$(id run)
on alice $W "lore run cancel $r2 --reason 'The client wants to keep the old logo'"
on alice $W "lore run show $r2"

note "run list"
on alice $W "lore run list"

note "run stays local"
on alice $W "lore sync && git status --short"

note "session list"
on alice $W "lore session list"
