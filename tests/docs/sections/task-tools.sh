# task-tools: the task tools beyond add/list/done for one person (docs/tasks/task-tools.md).
# Alice (an agency developer) plans the client's shop launch: a board, a checklist inside a
# task, a task split into sub-tasks, the tree check, relations between tasks, watching a task,
# its history, a template for every release, a repeating task and the stale-task sweep.

lore_repo alice client/website
W=work/website

# --- board ---------------------------------------------------------------------
note "board"
on alice $W "lore task add 'Write the launch email' --priority high"
local e; e=$(id tsk)
on alice $W "lore task add 'Pick the product photos' --priority low"
local ph; ph=$(id tsk)
on alice $W "lore task add 'Set up the payment provider' --priority urgent"
local pay; pay=$(id tsk)
on alice $W "lore task start $pay"
on alice $W "lore task board"
on alice $W "lore task board --group-by priority"

# --- checklist -----------------------------------------------------------------
note "checklist"
on alice $W "lore task checklist add $e 'Subject line' --item 'Product links' --item 'Discount code' --item 'Send a test to the client'"
on alice $W "lore task checklist tick $e 1"
on alice $W "lore task checklist edit $e 3 'Discount code LAUNCH10'"
on alice $W "lore task checklist list $e"

note "checklist untick and rm"
on alice $W "lore task checklist untick $e 1"
on alice $W "lore task checklist rm $e 2"
on alice $W "lore task checklist list $e"

note "checklist promote"
on alice $W "lore task checklist promote $e 3"
on alice $W "lore task list --parent $e"

note "checklist import"
on alice $W "lore task add 'Check the shop on a phone' --body \$'- [ ] iPhone\n- [ ] Android\n- [x] Tablet'"
local ck; ck=$(id tsk)
on! alice $W "lore task checklist import $ck --from body"
on alice $W "lore task checklist import $ck --from body --yes"
on alice $W "lore task checklist list $ck"

# --- split and the tree check --------------------------------------------------
note "split"
on alice $W "lore task split $ph 'Shoot the shirts' 'Shoot the mugs' --priority high 'Edit the photos' --dry-run"
on alice $W "lore task split $ph 'Shoot the shirts' 'Shoot the mugs' --priority high 'Edit the photos'"
on alice $W "lore task list --parent $ph"

note "tree check"
on alice $W "lore task tree check"

# --- relations -----------------------------------------------------------------
note "relate"
on alice $W "lore task add 'Write the launch mail'"
local dup; dup=$(id tsk)
on alice $W "lore task relate $dup --duplicates $e"
on alice $W "lore task add 'Thank-you email after the first order'"
local ty; ty=$(id tsk)
on alice $W "lore task relate $ty --follows-up $e --blocked-by $pay"

note "unrelate"
on alice $W "lore task unrelate $ty --blocked-by $pay"

# --- watch and history ---------------------------------------------------------
note "watch"
on alice $W "lore task watch $pay"
on alice $W "lore task unwatch $pay"
on alice $W "lore task watch $pay"

note "history"
on alice $W "lore task edit $pay --priority high"
on alice $W "lore task done $pay --no-lesson"
on alice $W "lore task history $pay"

# --- templates -----------------------------------------------------------------
note "template add"
cat > "$HOMES/alice/release.yaml" <<'YAML'
name: release
description: Ship one release of the shop
args:
  version: {type: text, required: true}
task:
  title: "Release ${arg:version}"
  priority: high
  checklist:
    - "CHANGELOG has ${arg:version}"
    - "Tag pushed"
  children:
    - {title: "Bump the version to ${arg:version}"}
    - {title: "Run the tests", after: [0]}
    - {title: "Deploy to the shop", after: [1]}
YAML
on alice $W "cat ~/release.yaml"
on alice $W "lore task template add release --from-file ~/release.yaml"
on alice $W "lore task template list"
on alice $W "lore task template show release"

note "template use"
on! alice $W "lore task add --template release"
on alice $W "lore task add --template release --arg version=1.4 --dry-run"
on alice $W "lore task add --template release --arg version=1.4"

note "template rename and delete"
on alice $W "lore task template rename release shop_release"
on alice $W "lore task template list"
on alice $W "lore task template delete shop_release --yes"

# --- recurring tasks -------------------------------------------------------------
note "recur"
on alice $W "lore task add 'Back up the shop database' --recur 'every week'"
local rc; rc=$(id tsk)
on alice $W "lore task recur show $rc"

note "recur done"
on alice $W "lore task done $rc --no-lesson"
local rc2; rc2=$(grep -o 'next instance: tsk_[0-9a-f]\{32\}' <<<"$LAST_OUT" | grep -o 'tsk_.*')
local rcs; rcs=$(id rcs)
on alice $W "lore task list --series $rcs --all"

note "recur skip"
on alice $W "lore task recur skip $rc2"
local rc3; rc3=$(grep -o 'tsk_[0-9a-f]\{32\}' <<<"$LAST_OUT" | grep -v "$rc2" | head -1)

note "recur edit"
on alice $W "lore task recur edit $rc3 'every! 14 days'"
on alice $W "lore task recur show $rc3"

note "recur stop"
on! alice $W "lore task recur stop $rc3"
on alice $W "lore task recur stop $rc3 --yes"
on alice $W "lore task recur show $rc3"

# --- stale tasks: three months later ----------------------------------------------
note "stale"
CLOCK=2027-02-01T09:00:00Z
on alice $W "lore task stale"
on alice $W "lore task stale --close --dry-run"
on alice $W "lore task stale --close --yes"
on alice $W "lore task list"
