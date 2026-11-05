# runs: one timed try at a piece of work (docs/tasks/run-log.md). Alice (an agency
# developer) times how long fixing a typo on the client's pricing page took, reads the
# time back on the task, cancels a second run and lists sessions.

lore_repo alice client/website
W=work/website

note "task"
on alice $W "lore task add 'Fix the typo on the pricing page'"
local t; t=$(id tsk)

note "run start"
on alice $W "lore run start --task $t --agent manual --goal 'Find and fix the wrong price'"
local r; r=$(id run)

note "run end"
STEP=600 on alice $W "lore run end $r --outcome success --summary 'The price now says 19'"

note "task time"
on alice $W "lore task time show $t"

note "run cancel"
on alice $W "lore run start --agent manual --goal 'Try a new logo colour'"
local r2; r2=$(id run)
on alice $W "lore run cancel $r2 --reason 'The client wants to keep the old logo'"

note "run stays local"
on alice $W "git status --short"

note "session list"
on alice $W "lore session list"
