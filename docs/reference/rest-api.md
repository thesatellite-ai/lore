# Local REST API

**In short:** `lore api` serves every lore command as an HTTP route on your own machine, so a tool that cannot run a shell command can still read and write your notes. It listens on `127.0.0.1` only, and every request needs the token lore keeps in `~/.lore/api-token`. A route runs the same operation as the command, with the same checks, errors and audit entry.

## What it is and when you want it

Most of the time you and your coding agents run `lore` commands directly. Reach for `lore api` when a program you use can only speak HTTP: an editor plugin, a local dashboard, a script in a language where starting a process is awkward. Every operation in lore's registry gets a route built from the command's name, and its input fields become the JSON body (or query parameters for `GET` and `DELETE`). The routes are listed in [cli-commands.md](cli-commands.md) next to each command, and as an OpenAPI document in [openapi.json](openapi.json).

What stays the same as on the command line:

- The secret check, team policies, confirmations and the audit log apply on every route; a handler never skips them.
- Errors carry the same `E_…` codes, with an HTTP status taken from the code's class (see [Error codes](errors.md)). `E_INTERNAL` is the only one that answers 500.
- Writes are made as the person `lore api` resolved when it started (the same identity `lore whoami` shows in that folder).

What is different:

- Nothing asks a question: a route that would ask first needs `"yes": true` in the body (or `?yes=true`), like `--yes`.
- The answer to a write that looks like it holds a secret is given in headers instead of flags: `X-Lore-Not-A-Secret: <why>`, `X-Lore-Secret-Ref: <manager>://<entry>` (once per finding, in order) or `X-Lore-Secret-Unstored: true`.
- `X-Lore-Agent: <name>` names the agent behind a request, and `X-Lore-Setup: <name>` picks a setup, like the global `--setup` flag.

## Before you start

- lore set up in a git repo (`lore init`), as in [your first project](../knowledge/first-project.md). `lore api` opens the notes of the folder it starts in, so start it inside the repo.
- `curl` or any HTTP client.

## Step 1 — Start the server

Run it in the repo, in a terminal of its own (or in the background). `--port` picks the port; the default is 7461, and `--port 0` picks a free one:

```sh
cd ~/work/website
lore api --port 7461
```

It prints one line when it is ready, then serves until you stop it with Ctrl-C. On the first start it writes a new random token to `~/.lore/api-token`, readable by you only; it never prints the token. The examples below ran on a free port, shown as `$PORT`:

<!-- example: api#api-start -->
```console
# alice, in ~
$ cat api.log
lore api listening on http://127.0.0.1:$PORT (token: ~/.lore/api-token)
$ ls -l .lore/api-token | cut -d" " -f1
-rw-------
```
<!-- /example -->

## Step 2 — Call it with the token

Send the token as `Authorization: Bearer <token>`. Without it every route, the OpenAPI document included, answers 401 with `E_UNAUTHORIZED`:

<!-- example: api#api-no-token -->
```console
# alice, in ~
$ curl -s -w "\nHTTP %{http_code}\n" http://127.0.0.1:$PORT/v1/rules
{"error":{"code":"E_UNAUTHORIZED","message":"missing or wrong bearer token","hint":"send Authorization: Bearer \u003cthe token in ~/.lore/api-token\u003e","exit":28,"class":"unauthorized","retryable":false,"needs_person":false}}
HTTP 401
```
<!-- /example -->

With the token, `POST /v1/rules` is `lore rule add` (201 Created), and `GET /v1/rules` is `lore rule list`. The rule written over HTTP is an ordinary rule, as `lore rule list` shows:

<!-- example: api#api-calls -->
```console
# alice, in ~
$ curl -s -X POST -H "Authorization: Bearer $(cat ~/.lore/api-token)" -d '{"body": "Run the tests before you push"}' -w "\nHTTP %{http_code}\n" http://127.0.0.1:$PORT/v1/rules
{"id":"rul_7a67…9504","store":"repo","scope":"master","severity":"must"}
HTTP 201
$ curl -s -H "Authorization: Bearer $(cat ~/.lore/api-token)" http://127.0.0.1:$PORT/v1/rules
{"schema_version":1,"kind":"rule.list","count":1,"data":[{"id":"rul_7a67…9504","created_at":"2026-11-02T10:00:00Z","updated_at":"2026-11-02T10:00:00Z","trust_score":0.5,"source_kind":"manual","project_id":"prj_2ad3…f891","body":"Run the tests before you push","activation":"always","severity":"must","created_by_actor_id":"act_162d…9666","created_by":"act_162d…9666","updated_by":"act_162d…9666"}]}

# alice, in website
$ lore rule list
rul_7a67…9504 [must/always] Run the tests before you push
```
<!-- /example -->

The route of a command follows its name: `add` is `POST /v1/<plural>`, `list` is `GET /v1/<plural>`, `show`, `edit` and `delete` are `GET`, `PATCH` and `DELETE /v1/<plural>/{id}`, and any other verb is `POST` on `/v1/<plural>/{id}/<verb>` (or `/v1/<plural>/<verb>` without an id). A command whose id is optional (`lore policy show`, `lore land run --all`, `lore task heartbeat`, a bulk `lore resource delete --kind host`) is also served on its route with the `/{id}` part taken out, such as `GET /v1/policy`, `POST /v1/lands/run` or `DELETE /v1/resources?kind=host`; its operationId ends in `.bulk`. `GET /openapi.json` and `GET /v1/spec` describe every route the server has.

## Step 3 — Read an error

An error is one JSON object with the code, the message, the exit status the command would give and its class. A parameter the command does not take is 400 `E_INVALID_INPUT`; a path with no route is 404 `E_NOT_FOUND`:

<!-- example: api#api-error -->
```console
# alice, in ~
$ curl -s -H "Authorization: Bearer $(cat ~/.lore/api-token)" -w "\nHTTP %{http_code}\n" "http://127.0.0.1:$PORT/v1/rules?colour=red"
{"error":{"code":"E_INVALID_INPUT","message":"unknown parameter colour","exit":20,"class":"bad_input","retryable":false,"needs_person":false}}
HTTP 400
$ curl -s -H "Authorization: Bearer $(cat ~/.lore/api-token)" -w "\nHTTP %{http_code}\n" http://127.0.0.1:$PORT/v1/notes
{"error":{"code":"E_NOT_FOUND","message":"no route GET /v1/notes","exit":27,"class":"not_found","retryable":false,"needs_person":false}}
HTTP 404
```
<!-- /example -->

## Step 4 — Rotate the token

`lore api token rotate` writes a new token to `~/.lore/api-token`. A running server checks that file on every request, so no restart is needed: right after the rotate the old token gets 401 and the new one works:

<!-- example: api#api-rotate -->
```console
# alice, in ~
$ cp .lore/api-token old-token

# alice, in website
$ lore api token rotate
new token written to ~/.lore/api-token

# alice, in ~
$ curl -s -o /dev/null -w "old token: HTTP %{http_code}\n" -H "Authorization: Bearer $(cat ~/old-token)" http://127.0.0.1:$PORT/v1/rules
old token: HTTP 401
$ curl -s -o /dev/null -w "new token: HTTP %{http_code}\n" -H "Authorization: Bearer $(cat ~/.lore/api-token)" http://127.0.0.1:$PORT/v1/rules
new token: HTTP 200
```
<!-- /example -->

## Step 5 — Stop it

Press Ctrl-C in its terminal, or stop the process. It finishes the requests in flight (for up to 5 seconds) and closes the port:

<!-- example: api#api-stop -->
```console
# alice, in ~
$ curl -s http://127.0.0.1:$PORT/v1/rules || echo "no answer (curl exit $?): lore api has stopped"
no answer (curl exit 7): lore api has stopped
```
<!-- /example -->

## Try it

With the server from Step 1 running, ask it which routes it has for one noun. `GET /openapi.json` describes every route, so a short filter over its `paths` lists the rule routes with their methods. Each line is one command: `GET /v1/rules` is `lore rule list`, `PATCH /v1/rules/{id}` is `lore rule edit`, `POST /v1/rules/{id}/archive` is `lore rule archive`:

<!-- example: api#try-api -->
```console
# alice, in ~
$ curl -s -H "Authorization: Bearer $(cat ~/.lore/api-token)" http://127.0.0.1:$PORT/openapi.json | python3 -c 'import json, sys; p = json.load(sys.stdin)["paths"]; [print(" ".join(sorted(m.upper() for m in p[k])), k) for k in sorted(p) if k.startswith("/v1/rules")]'
DELETE GET POST /v1/rules
POST /v1/rules/search
DELETE GET PATCH /v1/rules/{id}
POST /v1/rules/{id}/archive
POST /v1/rules/{id}/unarchive
```
<!-- /example -->

Swap `/v1/rules` for another noun (`/v1/tasks`, `/v1/memories`) to see its routes; the [command reference](cli-commands.md) lists the same route next to each command.

## For maintainers: `lore spec`

`lore spec docs` and `lore spec openapi` write the generated reference of the commands in this build: the commands and errors blocks of [cli-commands.md](cli-commands.md), and [openapi.json](openapi.json). Both write into `docs/reference` by default, so run them at the top of the lore repo; `--out <folder>` names another folder. With `--check` they write nothing and fail with `E_CONFIG_INVALID` when the file on disk is stale, which is how the repo's gate catches a command change without a regenerated reference. `lore spec docs` only fills the marked blocks of an existing page, so a folder without `cli-commands.md` is refused:

<!-- example: api#spec -->
```console
# alice, in ~
$ lore spec openapi --out api-docs && ls api-docs
wrote api-docs/openapi.json
openapi.json
$ lore spec openapi --out api-docs --check
current api-docs/openapi.json
$ echo "{}" > api-docs/openapi.json; lore spec openapi --out api-docs --check
ERROR: [E_CONFIG_INVALID] docs: the committed reference is stale: api-docs/openapi.json is stale at line 1:
  committed: {}
  generated: {
  hint: run: lore spec docs && lore spec openapi, then commit the files
(exit 20)
$ lore spec docs --out api-docs
ERROR: [E_NOT_FOUND] docs: api-docs/cli-commands.md does not exist
  hint: run it in the lore repo with the default --out, or pass --out <folder> holding a page with the <!-- lore:spec:begin commands --> and <!-- lore:spec:begin errors --> blocks
(exit 27)
```
<!-- /example -->

## Pitfalls

- The server only listens on `127.0.0.1`. It sends no CORS headers, so a web page in your browser cannot call it; use it from programs on the same machine.
- A token file that someone else could read (group or other permission bits set), or that holds anything but one token, is refused with `E_CONFIG_INVALID`; `chmod 600 ~/.lore/api-token` or `lore api token rotate` fixes it.
- A missing or unusable token file refuses every request, also on a running server; `lore api token rotate` fixes it.
- A store moved while the server runs (`lore store set-url`, from the CLI or through the API) needs no restart: before each request the server checks the files that say where stores live (`~/.lore/config.yaml`, the clone and address records under `~/.lore/state/`, the repo's `.lore/stores.yaml`) and reopens its stores when one changed.
- A `GET` takes query parameters only and refuses a body; a `POST` or `PATCH` takes a JSON body only and refuses query parameters. A request body over 4 MB is refused.
- Every request is made as the person the server resolved when it started. Start a separate `lore api` (on another port) for another setup or another repo.
- `lore spec docs` needs the page with its marker blocks; it never creates `cli-commands.md` from nothing.

## Related

- [Command and error reference](cli-commands.md): every command with its route, and every error code with its HTTP status.
- [Error codes and exit statuses](errors.md): what each `E_…` code means and what to do.
- [Flags](flags.md): the command-line flags the JSON fields mirror.
- [Environment variables](environment.md): the switches lore reads for one shell or one command.
