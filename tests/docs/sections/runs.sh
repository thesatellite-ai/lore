# runs: one timed try at a piece of work (docs/tasks/run-log.md). Alice (an agency
# developer) times how long fixing a typo on the client's pricing page took, reads the
# time back on the task, cancels a second run and lists sessions.

lore_repo alice client/website
W=work/website

note "task"
on alice $W "lore task add --title 'Fix the typo on the pricing page'"
local t; t=$(id tsk)

note "run start"
on alice $W "lore run start --task $t --agent manual --goal 'Find and fix the wrong price'"
local r; r=$(id run)

note "run end"
STEP=600 on alice $W "lore run end --id $r --outcome success --summary 'The price now says 19'"

note "task time"
on alice $W "lore task time show --id $t"

note "run cancel"
on alice $W "lore run start --agent manual --goal 'Try a new logo colour'"
local r2; r2=$(id run)
on alice $W "lore run cancel --id $r2 --reason 'The client wants to keep the old logo'"

note "run stays local"
on alice $W "git status --short"

note "session list"
on alice $W "lore session list"

# --- time by hand ---------------------------------------------------------------------
note "time log"
on alice $W "lore task time log --id $t --duration 30m --note 'Call with the client about the prices'"
local tme; tme=$(grep -o 'tme_[0-9a-f]*' <<<"$LAST_OUT" | head -1)
on alice $W "lore task time show --id $t"

note "time edit"
on alice $W "lore task time edit --id $tme --duration 45m"
on alice $W "lore task time show --id $t"

note "time rm and restore"
on alice $W "lore task time rm --id $tme --yes"
on alice $W "lore task time restore --id $tme"

note "time stop"
on alice $W "lore task start --id $t"
STEP=1200 on alice $W "lore task time stop --id $t"
on alice $W "lore task time show --id $t"
on alice $W "lore task show --id $t | head -4"
