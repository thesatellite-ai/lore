# missions: missions, plans, plan files, handoffs and the inbox for one person
# (docs/tasks/missions-and-plans.md). Alice (an agency developer) groups the work to open
# the client's online shop into a mission, writes a plan, turns a plan file into tasks,
# leaves herself a handoff and reads her inbox.

lore_repo alice client/website
W=work/website

# --- missions ----------------------------------------------------------------
note "mission add"
on alice $W "lore mission add 'Open the online shop' --target 2026-11-30 --body 'The shop goes live with ten products and a launch email.'"
local m; m=$(id msn)
on alice $W "lore task add 'Take the product photos' --mission $m"
local t1; t1=$(id tsk)
on alice $W "lore task add 'Write the product pages' --mission $m --after $t1"
local t2; t2=$(id tsk)
on alice $W "lore task add 'Send the invoice reminder'"
local t3; t3=$(id tsk)
on alice $W "lore task edit $t3 --mission $m"

note "mission list"
on alice $W "lore mission list"
on alice $W "lore mission show $m"

note "mission status"
on alice $W "lore mission status $m"
on alice $W "lore task done $t1 --no-lesson"
on alice $W "lore mission status $m"

note "mission tasks"
on alice $W "lore task list --mission $m"

note "mission edit"
on alice $W "lore mission edit $m --target 2026-12-07 --title 'Open the online shop before Christmas'"
on alice $W "lore mission search christmas"

note "pause"
on alice $W "lore mission pause $m"
on alice $W "lore mission list"
on alice $W "lore mission resume $m"

note "mission done"
on alice $W "lore mission done $m"
on alice $W "lore mission list"
on alice $W "lore mission list --all"

note "mission delete"
on alice $W "lore mission add 'Test mission, please ignore'"
local m2; m2=$(id msn)
on! alice $W "lore mission delete $m2"
on alice $W "lore mission delete $m2 --yes"

# --- plans (written notes) ------------------------------------------------------
note "plan add"
on alice $W "lore plan add --title 'Newsletter plan' --body 'Collect emails on the home page first, then send one newsletter a month.'"
local p; p=$(id pln)
on alice $W "lore plan list"
on alice $W "lore plan search newsletter"
on alice $W "lore plan edit $p --status approved"

# --- mission apply: a file that becomes a mission and its tasks ---------------------------
note "plan file"
quiet alice $W "cat > newsletter.yaml <<'Y'
mission: {key: newsletter, title: \"Start the monthly newsletter\"}
tasks:
  - {key: form,  title: \"Add the sign-up form to the home page\", timebox: 90m}
  - {key: first, title: \"Write the first newsletter\", after: [form]}
  - {key: send,  title: \"Send the first newsletter\", after: [first]}
Y"
on alice $W "cat newsletter.yaml"

note "mission apply"
on alice $W "lore mission apply newsletter.yaml"
local nm; nm=$(id msn)
on alice $W "lore mission status $nm"

note "mission apply again"
on alice $W "lore mission apply newsletter.yaml"

note "plan errors"
quiet alice $W "printf 'mission: {title: \"Bad plan\"}\ntasks:\n  - {key: b, title: \"Second\", after: [a]}\n  - {key: a, title: \"First\"}\n' > bad.yaml"
on alice $W "cat bad.yaml"
on! alice $W "lore mission apply bad.yaml"

# --- handoffs and the inbox -------------------------------------------------------
note "register"
on alice $W "lore actor register"

note "handoff"
on alice $W "lore task add 'Write the about page'"
local t4; t4=$(id tsk)
on alice $W "lore handoff add --for @alice --task $t4 'Two of the three sections are written; the team photo is still missing'"
local h; h=$(id hnd)
on alice $W "lore inbox"

note "handoff ack"
on alice $W "lore handoff ack $h"
on alice $W "lore inbox"
on alice $W "lore handoff search photo"

note "handoff unknown"
on! alice $W "lore handoff add --for @dave 'The logo files are in the shared drive'"
