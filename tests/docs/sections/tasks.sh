# tasks: tasks for one person (docs/tasks/tasks.md, commitment-and-snooze.md,
# reminders.md, link-commits.md). Alice (an agency developer) keeps her to-do list for
# the client's website in lore: add, list, start, finish, change, cancel, delete, order,
# group in tasklists, park ideas, snooze, set reminders and link the commits that did it.

lore_repo alice client/website
W=work/website

# --- the basics -------------------------------------------------------------
note "add"
on alice $W "lore task add --title \"Send the invoice reminder before Friday's demo\" --due 2026-11-06 --priority high"
local t1; t1=$(id tsk)
on alice $W "lore task add --title 'Fix the typo on the pricing page' --body 'The Pro plan says 9 a month; it should say 19.'"
local t2; t2=$(id tsk)
on alice $W "lore task add --title 'Update the footer year'"
local t3; t3=$(id tsk)

note "list"
on alice $W "lore task list"

note "start"
on alice $W "lore task start --id $t2"
on alice $W "lore task list --status in_progress"

note "show"
on alice $W "lore task show --id $t2"

note "edit"
on alice $W "lore task edit --id $t3 --priority low --due 2026-11-20 --title 'Update the footer year to 2027'"
on alice $W "lore task show --id $t3"

note "done"
on alice $W "lore task done --id $t1 --lesson 'The client pays faster when the reminder goes out two days before the demo'"
on alice $W "lore task list"
on alice $W "lore task list --all"

note "search"
on alice $W "lore task search --query pricing"
on alice $W "lore task search --query invoice"
on alice $W "lore task search --query invoice --all"

note "cancel"
on alice $W "lore task cancel --id $t3"
on alice $W "lore task list --status cancelled"

note "delete"
on alice $W "lore task add --title 'Test task, please ignore'"
local t4; t4=$(id tsk)
on! alice $W "lore task delete --id $t4"
on alice $W "lore task delete --id $t4 --yes"

# --- order: one task waits for another ---------------------------------------
note "after"
on alice $W "lore task add --title 'Write the welcome email'"
local a; a=$(id tsk)
on alice $W "lore task add --title 'Send the welcome email' --after $a"
local b; b=$(id tsk)
on alice $W "lore task show --id $b"

note "cycle"
on! alice $W "lore task edit --id $a --after $b"

# --- tasklists and saved views -------------------------------------------------
note "tasklist"
on alice $W "lore tasklist add --title 'Launch week' --body 'Everything that must happen before the shop opens'"
local tl; tl=$(id tlt)
on alice $W "lore task add --title 'Check every link on the home page' --tasklist $tl"
on alice $W "lore task edit --id $a --tasklist $tl"
on alice $W "lore tasklist list"
on alice $W "lore tasklist search --query launch"
on alice $W "lore tasklist edit --id $tl --status closed"

note "task view"
on alice $W "lore task view add --name open_todo --where 'status:todo'"
on alice $W "lore task view list"
on alice $W "lore task list --view open_todo"

# --- commitment and snooze ------------------------------------------------------
note "commitment"
on alice $W "lore task add --title 'Try a dark mode for the shop' --commitment someday"
on alice $W "lore task add --title 'Add a FAQ page' --commitment proposed"
local p; p=$(id tsk)
on alice $W "lore task list"

note "triage"
on alice $W "lore task triage"
on alice $W "lore task someday"

note "accept"
on alice $W "lore task edit --id $p --commitment accepted"
on alice $W "lore task triage"

note "defer"
on alice $W "lore task add --title 'Renew the SSL certificate' --defer-until 2026-12-01"
local d; d=$(id tsk)
on alice $W "lore task deferred"
on alice $W "lore task list --include-deferred"

note "undefer"
on alice $W "lore task edit --id $d --clear-defer"
on alice $W "lore task deferred"

# --- reminders ---------------------------------------------------------------
note "reminder"
on alice $W "lore reminder add --message 'Renew the domain name' --due 2027-06-01 --recurrence 1y"
local r1; r1=$(id rmd)
on alice $W "lore reminder add --message 'Call the client about the new logo' --due 2026-11-01"
local r2; r2=$(id rmd)
on alice $W "lore reminder list"

note "reminder done"
on alice $W "lore reminder done --id $r1"
on alice $W "lore reminder done --id $r2"
on alice $W "lore reminder list"
on alice $W "lore reminder list --done"

note "reminder delete"
on! alice $W "lore reminder delete --id $r2"
on alice $W "lore reminder delete --id $r2 --yes"

# --- link commits ------------------------------------------------------------
note "push after done"
quiet alice $W "echo 'Pro plan: 19 a month' > pricing.md && git add pricing.md"
on alice $W "git commit -qm 'Fix the Pro plan price on the pricing page'"
on! alice $W "git push -q origin main"
on alice $W "git pull -q --rebase origin main && git push -q origin main"

note "link"
on alice $W "lore link add --entity $t2 --commit HEAD"
on alice $W "lore link list --entity $t2"

note "commit show"
on alice $W "lore commit-show --id \$(git rev-parse HEAD)"
on alice $W "lore commit-show --id HEAD"
on alice $W "lore commit show --id \$(git rev-parse HEAD)"

note "done with commit"
on alice $W "lore task done --id $t2 --commit \$(git rev-parse HEAD) --no-lesson"
on alice $W "lore sync"
on alice $W "lore task list --status done"

note "merged"
on alice $W "lore task add --title 'Shrink the hero image'"
local t5; t5=$(id tsk)
on alice $W "lore task done --id $t5 --no-lesson"
on alice $W "lore task merged --id $t5"

note "link remove"
on alice $W "lore link list"
local cml; cml=$(id cml)
on! alice $W "lore link remove --id $cml"
on alice $W "lore link remove --id $cml --yes"

# --- accept and decline proposals ---------------------------------------------------
note "accept and decline"
on alice $W "lore task add --title 'Add a size guide' --commitment proposed"
local pa; pa=$(id tsk)
on alice $W "lore task add --title 'Rename the shop to Website Pro' --commitment proposed"
local pd; pd=$(id tsk)
on alice $W "lore task triage"
on alice $W "lore task accept --id $pa --priority high"
on alice $W "lore task decline --id $pd --reason wont_do --note 'The client keeps the name'"
on alice $W "lore task triage"
on alice $W "lore task list --status cancelled"
on alice $W "lore task list --status cancelled --all"
on alice $W "lore task show --id $pd"

# --- export and import ---------------------------------------------------------------
note "export"
on alice $W "lore task export --where 'status:todo' --format markdown"
on alice $W "lore task export --where 'status:todo' --format csv"

note "import"
cat > "$HOMES/alice/backlog.csv" <<'CSV'
id,title,priority,status
T-1,Add a cookie banner,high,To Do
T-2,Compress the product photos,medium,In Review
CSV
on alice $W "cat ~/backlog.csv"
on alice $W "lore task import --file ~/backlog.csv --map-status 'To Do=todo,In Review=in_progress' --dry-run"
on alice $W "lore task import --file ~/backlog.csv --map-status 'To Do=todo,In Review=in_progress' --yes"
on alice $W "lore task import --file ~/backlog.csv --map-status 'To Do=todo,In Review=in_progress' --yes"
