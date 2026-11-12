# choosing.sh: where each storage choice keeps its files (docs/choosing/backend.md).
#
# Alice (agency developer) has lore in the client's repo github.com/client/website and
# a folder store next to it. Shows where the row files and the caches land, that the
# repo cache lives in lore's home, not in the repo, and the refusal of a store inside a cloud-sync folder.
# Fake GitHub and file:// stores only.

lore_repo alice client/website

note "where the files are"
on alice work/website "lore store init notes file://\$HOME/stores/notes"
on alice work/website "lore memory add --to notes --body 'The client pays invoices on the 15th.'"
on alice work/website "lore rule add --body 'Run the tests before you push.'"
on alice work/website "find ~/stores/notes -type f | sort"
on alice work/website "ls .lore/data/rules"
on alice work/website "ls -a .lore"
on alice work/website "find ~/.lore/cache -name '*.db' | sed -e 's#/sto_[0-9a-z]*/#/<store id>/#' -e 's/-[0-9a-f]\{12\}\.db/-<hash>.db/' | sort"

note "cloud sync folder"
on! alice work/website "lore store init synced file://\$HOME/Dropbox/notes"
