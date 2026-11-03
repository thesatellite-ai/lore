# search: find any note by its words (docs/knowledge/search.md).

lore_repo alice client/website
W=work/website
quiet alice $W "lore rule add --body 'Run the tests before you push' && lore memory add --body 'The client replies faster on WhatsApp than email' && lore decision add --title 'We use Stripe for payments' --body 'One payment provider for the shop and the website.' && lore hotfix add --title 'Stripe test mode is on in staging' --body 'Use the test card in staging, never a real one.' && lore memory add --body 'Old payments page is gone' && lore task add 'Send the invoice reminder before Friday'"

note "search"
on alice $W "lore search payments"

note "search one kind"
on alice $W "lore search payments --tables decision"
on alice $W "lore decision search Stripe"

note "search json"
on alice $W "lore search WhatsApp --json"

note "archived"
quiet alice $W "lore memory archive \$(lore memory list | grep 'Old payments' | cut -d' ' -f1)"
on alice $W "lore search payments --tables memory"
on alice $W "lore search payments --tables memory --include-archived"

note "status"
on alice $W "lore search status"

note "rebuild"
on alice $W "lore search rebuild"
on alice $W "lore search rebuild --kind memory"

note "repo scope"
on alice $W "lore repo add web --origin https://github.com/client/website"
on alice $W "lore memory add --repo web --body 'The web repo deploys on every merge to main'"
on alice $W "lore memory list --repo web"
on alice $W "lore memory list --master-only"
on alice $W "lore memory list --repo web --no-inherit"
on alice $W "lore search deploys --all-repos"

note "word forms"
on alice $W "lore memory add --body 'The client sends invoices on Fridays'"
on alice $W "lore search invoice"
on alice $W "lore search invoices"
on alice $W "lore search 'invoi*'"
on alice $W "lore search 'client Fridays'"

note "querylog"
on alice $W "lore querylog list"

note "no results"
on alice $W "lore search pineapple"
