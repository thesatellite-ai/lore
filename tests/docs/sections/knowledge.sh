# knowledge: every kind of note you write down, and how to change, replace, comment on
# and tag them (docs/knowledge/rules-memories-decisions.md, other-row-kinds.md,
# comments-and-tags.md, import-existing-notes.md).

lore_repo alice client/website
W=work/website

# --- rules, memories, decisions ---------------------------------------------
note "rule"
on alice $W "lore rule add --body 'Run the tests before you push'"
local r1; r1=$(id rul)
on alice $W "lore rule add --body 'Never put passwords in the code' --severity must"
on alice $W "lore rule add --body 'Keep pull requests under 400 lines' --severity should"
on alice $W "lore rule list"
on alice $W "lore rule show $r1"

note "memory"
on alice $W "lore memory add --body 'The client replies faster on WhatsApp than email'"
local m1; m1=$(id mem)
on alice $W "lore tag add --name staging"
on alice $W "lore memory add --body 'The staging site is reset every Monday morning' --tag staging"
on alice $W "lore memory list"

note "decision"
on alice $W "lore decision add --title 'We use Stripe for payments' --body 'Stripe already works in the client shop app, so we keep one payment provider.'"
local d1; d1=$(id dec)
on alice $W "lore decision list"
on alice $W "lore decision show $d1"

note "edit"
on alice $W "lore memory edit $m1 --body 'The client replies faster on WhatsApp than email, even on weekends'"
on alice $W "lore memory show $m1"

note "supersedes"
on alice $W "lore decision add --title 'We use Stripe and bank transfer for payments' --body 'The client asked for bank transfer too.' --supersedes $d1"
on alice $W "lore decision list"

note "invalidate"
on alice $W "lore memory add --body 'The office printer is on the second floor'"
local m2; m2=$(id mem)
on alice $W "lore memory invalidate $m2"
on alice $W "lore memory list"
on alice $W "lore memory show $m2"
on alice $W "lore search printer"

note "archive"
on alice $W "lore rule archive $r1"
on alice $W "lore rule list"
on alice $W "lore rule list --archived"
on alice $W "lore render >/dev/null && grep -c 'Run the tests before you push' .lore/LORE.md || true"
on alice $W "lore rule unarchive $r1"
on alice $W "lore rule list"

note "search one kind"
on alice $W "lore memory search WhatsApp"

note "delete"
on alice $W "lore memory add --body 'Lunch is at noon'"
local m3; m3=$(id mem)
on alice $W "lore memory delete $m3 --yes"
on alice $W "lore memory show $m3 || true"

note "secrets"
on! alice $W "lore memory add --body 'The staging password is password=example-only'"
on alice $W "lore memory add --body 'The staging password is password=example-only' --not-a-secret --reason 'example value, not a real password' --yes"
local ms; ms=$(id mem)
on alice $W "ls .lore/data/memories | grep -c $ms || true"
on alice $W "lore memory delete $ms --yes"

# --- the other kinds ----------------------------------------------------------
note "hotfix"
on alice $W "lore hotfix add --title 'Checkout button is broken on Safari' --body 'Use the old button until the fix ships.' --severity critical"
on alice $W "lore hotfix list"

note "pattern"
on alice $W "lore pattern add --title 'Load data in one place' --body 'Each page loads its data in its loader, never inside a button handler.'"

note "playbook"
on alice $W "lore playbook add --title 'How to release' --body '1. Run the tests. 2. Tag the release. 3. Post in the team chat.'"

note "prompt"
on alice $W "lore prompt add --title 'Review a pull request' --description 'Bugs first, then style' --body 'Read the change, list bugs first, then style notes.'"

note "snapshot"
on alice $W "lore snapshot add --title 'Week 44' --body 'Checkout works. Search is half done. Demo on Friday.'"
on alice $W "lore snapshot list"

note "incident"
on alice $W "lore incident add --title 'Site down for 20 minutes' --body 'The disk filled up with logs. We now rotate logs daily.'"

note "behaviour"
on alice $W "lore behaviour add --title 'Ask before deleting' --body 'Ask before you delete any file you did not create.'"

note "architecturenote"
on alice $W "lore architecturenote add --title 'How the site is built' --body 'The website reads products from the shop API and caches them for 5 minutes.'"

note "cookbookrecipe"
on alice $W "lore cookbookrecipe add --title 'Add a new page' --body 'Copy pages/about, rename it, add a link in the menu.'"

note "suggestion"
on alice $W "lore suggestion add --title 'Add dark mode' --body 'Several users asked for it.'"

note "tastepref"
on alice $W "lore tastepref add --body 'Buttons are blue'"

note "techdoc"
on alice $W "lore techdoc add --name 'Payments docs' --base-url https://docs.example.com/payments --description 'How to take a card payment'"

note "workflow"
on alice $W "lore workflow add --title 'Code review' --body 'One approval from a second developer before merge.'"

note "workspace"
on alice $W "lore workspace add --title 'Website redesign' --body 'Everything for the new look of the site.'"

note "other kinds listed"
on alice $W "lore incident list; lore tastepref list; lore techdoc list"

# --- comments and tags --------------------------------------------------------
note "comment"
on alice $W "lore comment add --on-table decisions --on-id $d1 --body 'The client confirmed this on the call.'"
on alice $W "lore comment list --on-table decisions --on-id $d1"

note "tag"
on alice $W "lore tag add --name payments"
on alice $W "lore tag attach --tag payments --on-table decisions --on-id $d1"
on alice $W "lore tag list"
on alice $W "lore tag detach --tag payments --on-table decisions --on-id $d1"

note "commit"
on alice $W "git status --short | head -5"
on alice $W "git add -A && git commit -qm 'lore: notes' && git log --oneline | head -1"

# --- import existing notes ----------------------------------------------------
note "import"
quiet alice $W "mkdir -p .ai && printf '# Team notes\n\n## Testing\n\nRun the tests before you push.\n\n## Payments\n\nWe use Stripe for payments.\n' > .ai/notes.md"
on alice $W "cat .ai/notes.md"
on alice $W "lore learn-from docs --paths .ai/notes.md"
on alice $W "lore learn list"

note "promote"
local c1 c2 c3
c1=$(grep -o 'lcd_[0-9a-f]\{32\}' <<<"$LAST_OUT" | sed -n 1p)
c2=$(grep -o 'lcd_[0-9a-f]\{32\}' <<<"$LAST_OUT" | sed -n 2p)
c3=$(grep -o 'lcd_[0-9a-f]\{32\}' <<<"$LAST_OUT" | sed -n 3p)
on! alice $W "lore learn promote $c2 --target rules"
on alice $W "lore learn promote $c3"
on alice $W "lore learn reject $c1"
on alice $W "lore learn list --status ''"
on alice $W "lore memory list"

note "external source"
on alice $W "lore external-source add --kind wiki --name 'Team wiki'"
on alice $W "lore external-source list"
local e1; e1=$(id ext)
on alice $W "lore external-source enable $e1 && lore external-source list"
on alice $W "lore external-source disable $e1"
