# stores.sh: one person's stores (docs/sync/stores.md): list, the lore-store.yaml marker,
# visibility, a moved store, folders, moving a row between places and detaching a store.
#
# Alice (agency developer) keeps her own notes next to the client's repo
# github.com/client/website in a folder store. Fake GitHub and file:// stores only.

lore_repo alice client/website

note "store init"
on alice work/website "lore store init --name notes --url file://\$HOME/stores/notes"
on alice work/website "cat .lore/stores.yaml"
on alice work/website "ls ~/stores/notes; cat ~/stores/notes/lore-store.yaml"
on alice work/website "lore store list"

note "visibility"
on! alice work/website "lore store visibility --name notes --visibility shareable"
on alice work/website "lore store visibility --name notes --visibility shareable --yes"
on alice work/website "grep visibility ~/stores/notes/lore-store.yaml"
on alice work/website "lore store visibility --name notes --visibility internal --yes"

note "store moved"
on alice work/website "mv ~/stores/notes ~/stores/agency-notes"
on alice work/website "lore store list"
on alice work/website "lore store set-url --name notes --url file://\$HOME/stores/agency-notes"
on alice work/website "lore store list"

note "store folders"
on alice work/website "lore store folder add --store notes --folder golang"
on alice work/website "lore memory add --to notes/golang --body 'Close every channel from the sender side.'"
on alice work/website "cat .lore/stores.yaml"
on alice work/website "lore memory list"
on alice work/website "lore store folder list --store notes"

note "move a row"
on alice work/website "lore rule add --body 'Keep the agency hourly rate out of the client repo.'"
local r_rate; r_rate=$(id rul)
on! alice work/website "lore row move --id $r_rate --to notes"
on alice work/website "lore row move --id $r_rate --to notes --yes"
on alice work/website "lore rule list"
on alice work/website "cat .lore/data/rules/$r_rate.json"

note "no way back"
# A row never returns to a place it left (its tombstone stays the last word there).
on! alice work/website "lore row move --id $r_rate --to repo --yes"
on alice work/website "lore rule list"

note "detach a store"
on alice work/website "lore store remove --name notes/golang --yes"
on! alice work/website "lore store remove --name notes"
on alice work/website "lore store remove --name notes --yes"
on alice work/website "lore store list"
on alice work/website "lore rule list"
on alice work/website "ls ~/stores/agency-notes"
