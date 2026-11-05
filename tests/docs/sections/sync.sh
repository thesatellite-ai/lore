# sync.sh: notes that travel with git (docs/sync/**).
#
# Alice and Bob (agency developers) work in the client's repo github.com/client/website.
# Every row they keep "with the code" is one JSON file in .lore/data, committed like code.
# The fix-projects part uses a second repo, github.com/client2/app. Fake GitHub only.

solo_repo alice client/website
person bob bob@agency.example

# --- docs/sync/how-notes-travel-with-git.md ------------------------------------------
note "init"
on alice work/website "lore init --non-interactive --name=website"
on alice work/website "git status --short"
on alice work/website "ls .lore .lore/data"
on alice work/website "cat .gitattributes"
on alice work/website "git add -A && git commit -qm 'lore: init' && git push -q origin main"

note "first rule"
on alice work/website "lore rule add --body 'Run the tests before you push.'"
local r_tests; r_tests=$(id rul)
on alice work/website "git status --short"
on alice work/website "cat .lore/data/rules/$r_tests.json"
on alice work/website "lore sync status"
on alice work/website "git add -A && git commit -qm 'lore: test rule' && git push -q origin main"

note "teammate clone"
on bob work "git clone -q https://github.com/client/website"
on bob work/website "lore rule list"

note "sync by hand"
on bob work/website "lore sync"
on bob work/website "lore sync status"

note "sync off"
on alice work/website "LORE_SYNC=0 lore memory add --body 'The client replies faster on WhatsApp than email.'"
local m_whatsapp; m_whatsapp=$(id mem)
on alice work/website "git status --short"
on alice work/website "lore sync"
on alice work/website "git status --short"
on alice work/website "git add -A && git commit -qm 'lore: whatsapp memory' && git push -q origin main"

# --- docs/sync/branches-peek-promote.md -------------------------------------------------
note "branch notes"
on alice work/website "git checkout -q -b feature/payments"
on alice work/website "lore memory add --body 'We use Stripe for payments.'"
on alice work/website "lore rule add --body 'Never put passwords in the code.'"
local r_pw; r_pw=$(id rul)
on alice work/website "git add -A && git commit -qm 'lore: payment notes' && git push -q -u origin feature/payments"
on alice work/website "git checkout -q main && lore memory list"
on alice work/website "git checkout -q feature/payments && lore memory list"

note "peek"
on bob work/website "git fetch -q origin"
on bob work/website "lore sync peek origin/feature/payments"
on bob work/website "lore sync peek origin/feature/payments memories"

note "promote"
on alice work/website "lore sync promote $r_pw --to main --dry-run"
on alice work/website "lore sync promote $r_pw --to main"
on alice work/website "git log --oneline -2 main"
on alice work/website "git push -q origin main"
on bob work/website "git pull -q && lore rule list"

# --- docs/sync/conflicts-and-duplicates.md ----------------------------------------------
note "merge by field"
on alice work/website "git checkout -q main && git pull -q --no-rebase origin main"
on alice work/website "lore rule add --body 'Buttons are blue.' --severity should"
local r_btn; r_btn=$(id rul)
on alice work/website "git add -A && git commit -qm 'lore: button rule' && git push -q origin main"
on bob work/website "git pull -q"
on alice work/website "lore rule edit $r_btn --severity must && git commit -qam 'lore: buttons are a must' && git push -q origin main"
on bob work/website "lore rule edit $r_btn --body 'Buttons are blue, links are green.' && git commit -qam 'lore: link colour'"
on bob work/website "git pull --no-rebase origin main"
on bob work/website "lore rule list"
on bob work/website "git push -q origin main"

note "same text conflict"
on alice work/website "git pull -q --no-rebase origin main"
on alice work/website "lore rule edit $r_btn --body 'Buttons are dark blue.' && git commit -qam 'lore: dark blue'"
on bob work/website "lore rule edit $r_btn --body 'Buttons are light blue.' && git commit -qam 'lore: light blue' && git push -q origin main"
on! alice work/website "git pull -q --no-rebase origin main"
on alice work/website "grep body .lore/data/rules/$r_btn.json"
on alice work/website "lore sync status"
on! alice work/website "git commit -qam 'merge'"
on alice work/website "lore rule edit $r_btn --body 'Buttons are blue.'"
on alice work/website "git add -A && git commit -qm 'merge: buttons are blue' && git push -q origin main"

note "duplicates"
on bob work/website "git pull -q --no-rebase origin main"
on alice work/website "lore memory add --body 'Send the invoice reminder before the demo on Friday.' && git add -A && git commit -qm 'lore: invoice' && git push -q origin main"
on bob work/website "lore memory add --body 'send the invoice reminder before the demo on friday.' && git add -A && git commit -qm 'lore: invoice (bob)'"
on bob work/website "git pull -q --no-rebase origin main"
on bob work/website "lore memory list | grep -i invoice"
local m_a m_b
m_a=$(grep -o 'mem_[0-9a-f]\{32\}' <<<"$LAST_OUT" | sed -n 1p)
m_b=$(grep -o 'mem_[0-9a-f]\{32\}' <<<"$LAST_OUT" | sed -n 2p)
# merge-rows folds the duplicate on the 0.1.18 core: references move to the kept row,
# the dropped row goes to the trash in one batch.
on bob work/website "lore sync merge-rows memories --keep $m_a --drop $m_b"
on bob work/website "lore memory list"
on bob work/website "lore trash list"
on bob work/website "git add -A && git commit -qm 'lore: fold duplicate memory' && git push -q origin main"

note "file and database conflict"
on alice work/website "git pull -q --no-rebase origin main"
# A memory's file is the truth (core-owned row): a hand edit of the file wins, and
# there is no database copy to conflict with it.
on alice work/website "sed -i 's/than email/than email or phone/' .lore/data/memories/$m_whatsapp.json"
on alice work/website "lore memory list"
on alice work/website "lore sync"
on alice work/website "lore sync conflicts"
on alice work/website "git add -A && git commit -qm 'lore: whatsapp wording' && git push -q origin main"

# --- docs/sync/trash-and-purge.md -------------------------------------------------------
note "trash"
on alice work/website "git rm -q .lore/data/memories/$m_whatsapp.json"
on alice work/website "lore memory list"
# A row file removed with git is not in lore's trash: the file is the truth, so the
# row comes back from git (lore trash holds rows deleted with a lore command).
on alice work/website "git checkout HEAD -- .lore/data/memories/$m_whatsapp.json"
on alice work/website "lore memory list"
on alice work/website "git add -A && git status --short"

note "export"
on alice work/website "rm -rf .lore/data && lore sync status"
# The row files are the truth (STORE_SERVER_SPEC 3): a deleted .lore/data comes back
# from git, and nothing is lost.
on alice work/website "git checkout -q HEAD -- .lore/data && git status --short"

note "purge"
on alice work/website "lore memory archive $m_whatsapp && git commit -qam 'lore: archive whatsapp memory'"
on alice work/website "lore trash purge --archived-before 0d --dry-run"
on! alice work/website "lore trash purge --archived-before 0d"
on alice work/website "lore trash purge --archived-before 0d --confirm"
on alice work/website "cat .lore/data/_purged.json"
on alice work/website "git add -A && git status --short && git commit -qm 'lore: purge archived rows' && git push -q origin main"

# --- docs/sync/hooks-and-merge-driver.md ------------------------------------------------
note "hooks"
# lore wires the hooks, the merge driver and .gitattributes after every command; there
# is no command to run for it.
on alice work/website "lore sync status >/dev/null"
on alice work/website "ls .git/hooks | grep -v sample"
# The driver line names the lore binary by its full path; shortened here.
on alice work/website "git config --get-regexp '^merge\\.lore' | sed \"s|'/[^']*'|/path/to/lore|\""
on alice work/website "cat .git/hooks/pre-commit"

note "commit refused"
on alice work/website "printf '{ broken' > .lore/data/rules/$r_btn.json && git add .lore"
on! alice work/website "git commit -m 'oops'"
on alice work/website "git checkout -q HEAD -- .lore/data && git status --short"

# --- docs/sync/github-workflow.md -------------------------------------------------------
note "workflow preview"
on alice work/website "lore sync install-action --dry-run | grep -A 6 '^on:'"

note "workflow manual"
on alice work/website "lore sync install-action"
on alice work/website "grep -A 6 '^on:' .github/workflows/lore-sync-merge.yml"

note "workflow automatic"
on alice work/website "lore sync install-action --auto"
on alice work/website "grep -A 7 '^on:' .github/workflows/lore-sync-merge.yml"
on alice work/website "rm .github/workflows/lore-sync-merge.yml"

note "ci merge"
on bob work/website "git pull -q --no-rebase origin main"
on bob work/website "git checkout -q -b feature/footer && lore rule edit $r_btn --body 'Buttons are blue, links are green.' && git commit -qam 'lore: link colour' && git push -q -u origin feature/footer"
on alice work/website "lore rule edit $r_btn --severity should && git commit -qam 'lore: buttons are a should' && git push -q origin main"
on bob work/website "git fetch -q origin && lore sync ci-merge --base origin/main --dry-run"
on bob work/website "lore sync ci-merge --base origin/main"
on bob work/website "lore rule list"
on bob work/website "lore sync ci-merge --base origin/main"

# --- docs/sync/conflicts-and-duplicates.md: two set-ups met --------------------------
note "fix projects"
solo_repo alice client2/app
on alice work/app "git checkout -q -b alice-setup && lore init --non-interactive --name=app >/dev/null && git add -A && git commit -qm 'lore: init (alice)'"
on bob work "git clone -q https://github.com/client2/app && cd app && git checkout -q -b bob-setup && lore init --non-interactive --name=app >/dev/null && git add -A && git commit -qm 'lore: init (bob)' && git push -q -u origin bob-setup"
on alice work/app "git checkout -q main && git merge -q alice-setup"
on! alice work/app "git fetch -q origin && git merge --no-edit origin/bob-setup"
on alice work/app "lore project list"
on alice work/app "lore sync fix-projects --dry-run"
on alice work/app "lore sync fix-projects"
on alice work/app "lore project list"
on alice work/app "git add -A && git commit -qm 'lore: one project' && git push -q origin main"
