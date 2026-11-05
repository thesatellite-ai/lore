# Reminders

**In short:** a reminder is a note with a date, like "Renew the domain name on 1 June". `lore reminder list` shows what is coming up and marks what is overdue with `!`. A reminder can repeat: mark a yearly one done and lore moves it to next year.

## What it is and when you want it

Some work is not a task yet, only a date you must not forget: renew a domain, call the client back, check that a certificate is still valid. A reminder holds a message and a due date. You can make it repeat every 7 days, 30 days, month, 3 months, 6 months or year.

Reminders are shared with the code like rules and memories: lore saves each one as a file in `.lore/data`, and you commit it with your next change. Anyone who pulls the repo sees the same reminders.

Alice (an agency developer) keeps the dates for the client's website, `github.com/client/website`.

## Before you start

- lore set up in the repo: see [Your first project](../knowledge/first-project.md).

## Step 1 — Add reminders

`--due` is required. It takes a date (`YYYY-MM-DD`) or a full time. `--recurrence` makes it repeat.

<!-- example: tasks#reminder -->
```console
# alice, in website
$ lore reminder add 'Renew the domain name' --due 2027-06-01 --recurrence 1y
✓ reminder rmd_b69e…5ed7 due 2027-06-01
$ lore reminder add 'Call the client about the new logo' --due 2026-11-01
✓ reminder rmd_0aae…a61f due 2026-11-01
$ lore reminder list
! 2026-11-01  Call the client about the new logo
    rmd_0aae…a61f
  2027-06-01  Renew the domain name (1y)
    rmd_b69e…5ed7
```
<!-- /example -->

Reminders are listed by date, soonest first. A `!` at the start of the line means the date has passed. A repeating reminder shows how often it repeats, here `(1y)`.

## Step 2 — Mark reminders done

`lore reminder done` closes a reminder. A repeating one is not closed: lore moves its date forward by one step (`rescheduled to …`) and keeps it in the list. A one-time reminder moves to the done list, which `--done` shows.

<!-- example: tasks#reminder-done -->
```console
# alice, in website
$ lore reminder done rmd_b69e…5ed7
✓ rescheduled to 2028-06-01
$ lore reminder done rmd_0aae…a61f
✓ reminder rmd_0aae…a61f completed
$ lore reminder list
  2028-06-01  Renew the domain name (1y)
    rmd_b69e…5ed7
$ lore reminder list --done
  2026-11-01  Call the client about the new logo
    rmd_0aae…a61f
```
<!-- /example -->

## Step 3 — Delete a reminder

Delete only for mistakes. lore asks first; without a terminal it stops with `E_NEEDS_CONFIRM` until you add `--yes`:

<!-- example: tasks#reminder-delete -->
```console
# alice, in website
$ lore reminder delete rmd_0aae…a61f
ERROR: [E_NEEDS_CONFIRM] engine: run reminder delete?
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore reminder delete rmd_0aae…a61f --yes
✓ rmd_0aae…a61f moved to the trash (restore: lore trash restore rmd_0aae…a61f)
lore sync: trashed rows 1
lore sync: rows whose file disappeared are kept in `lore trash --local` (undo: `lore trash restore --local <id>`)
```
<!-- /example -->

## Try it

<!-- ds:block id=reminder-recurrence-gs41nylk -->
| `--recurrence` | Next date after `done` |
|---|---|
| `7d` | 7 days later |
| `30d` | 30 days later |
| `1m` | the same day next month |
| `3m` | the same day 3 months later |
| `6m` | the same day 6 months later |
| `1y` | the same day next year |

After `done`, the yearly reminder from Step 1 sits one year later, and the one-time reminder is on the done list:

<!-- example: tasks#reminder-done only=3,4 -->
```console
# alice, in website
$ lore reminder list
  2028-06-01  Renew the domain name (1y)
    rmd_b69e…5ed7
$ lore reminder list --done
  2026-11-01  Call the client about the new logo
    rmd_0aae…a61f
```
<!-- /example -->

## Pitfalls

- **Nothing pops up.** lore does not send a message, and it does not write reminders into the file your coding tool reads. You see them when you run `lore reminder list`. Run it at the start of your day, or ask your coding tool to run it.
- **The next date counts from the old due date, not from today.** A weekly reminder that was due three weeks ago and that you mark done today moves one week forward, so it can still be overdue. Mark it done again, or delete it and add a new one.
- **A repeating reminder never shows in `--done`.** Each `done` only moves its date. To stop it for good, delete it.
- **Unknown repeat values are refused.** Only the six values in the table above work; `2w`, for example, does not.
- **Reminders are not tasks.** They have no status, priority or mission. If the date means real work, add a task with `--due` too, or snooze a task with `--defer-until` (see [Commitment and snooze](commitment-and-snooze.md)).

## Related

- [Tasks](tasks.md): work with a status, a priority and a due date.
- [Commitment and snooze](commitment-and-snooze.md): hide a task until a date.
- [Your first project](../knowledge/first-project.md): committing what lore writes to `.lore/data`.
