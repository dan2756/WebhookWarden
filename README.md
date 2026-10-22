# hook

A local webhook receiver that runs a command on each POST.

Point a GitHub (or any other) webhook at it, and every delivery runs your
deploy script with the request body on stdin and the request details in
environment variables. One binary, no config file: signature checks, a run
queue, timeouts and graceful shutdown are all flags.

## Install

```sh
go install github.com/dan2756/hook@latest
```

## Usage

```sh
# run a program directly: everything after -- is the command and its args
hook -p 9000 -- ./deploy.sh production

# or give a single string, which is run with sh -c
hook -p 9000 'make deploy && say done'
```

Flags go before the command. Each POST is answered with `202 Accepted` right
away and the command runs in the background; `GET /` shows what it has been
up to:

```console
$ curl -X POST -d '{"ref":"refs/heads/main"}' localhost:9000/
accepted: run #1
$ curl localhost:9000/
{"runs":1,"running":0,"queued":0,"last_exit_code":0,"last_run":"2026-06-25T18:40:12Z"}
```

## What the command gets

The request body is piped to the command's stdin, and the request details
are in these environment variables:

| Variable             | Value                                                    |
|----------------------|----------------------------------------------------------|
| `HOOK_METHOD`        | request method (always `POST`)                           |
| `HOOK_PATH`          | URL path, e.g. `/deploy`                                 |
| `HOOK_QUERY`         | raw query string without the `?`, e.g. `env=prod`        |
| `HOOK_REMOTE_ADDR`   | client address, `ip:port`                                |
| `HOOK_CONTENT_TYPE`  | `Content-Type` of the request                            |
| `HOOK_HEADER_<NAME>` | one per request header, e.g. `HOOK_HEADER_X_GITHUB_EVENT` |

Header names are upper-cased and every character other than a letter or digit
becomes `_`, so `X-GitHub-Event` is `HOOK_HEADER_X_GITHUB_EVENT` and `Host` is
`HOOK_HEADER_HOST`. A header sent more than once arrives as one variable with
the values joined by `, `.

The rest of hook's environment is passed through, except variables that
start with `HOOK_` (`HOOK_SECRET` in particular), so everything in that
namespace describes the request.

The command has no terminal, even when hook runs in one. Anything that
prompts on the terminal (`git` asking for a username or password, `sudo`
asking for a password, `ssh` asking about a host key) fails right away
instead of waiting for an answer, just as it would under systemd. Set up the
deploy so it doesn't need to ask: a credential helper or SSH key, the host
already in `known_hosts`, a `NOPASSWD` sudo rule.

## Responses

| Status | When                                                                       |
|--------|----------------------------------------------------------------------------|
| `202`  | accepted; the command runs in the background                               |
| `200`  | `--sync` and the command exited 0; also `GET` or `HEAD` on the status path |
| `500`  | `--sync` and the command exited non-zero                                   |
| `400`  | the path contains a control character, or the body could not be read       |
| `401`  | `--secret` is set and the signature is missing or wrong                    |
| `404`  | `--path` is set and this is a different path                               |
| `405`  | not a POST, and not a GET or HEAD of the status path                       |
| `413`  | body larger than `--max-body`                                              |
| `503`  | the queue is full, or hook is shutting down                                |

Accepted requests get an `X-Hook-Run` header with the run number, which is
also how the run shows up in the log.

### Paths and status

By default any path is accepted. `--path /deploy` restricts hook to that one
path; everything else gets `404`. The flag is compared to the URL path, so it
must start with `/` and cannot include a query string (`?`) or fragment (`#`):
those are not part of the path and would never match. A path containing a
control character, such as a newline from `%0a`, is rejected with `400`.
`GET` or `HEAD` of `/` (or of `--path`) returns the status document shown
above. `last_exit_code` and `last_run` (when the last run finished) are
`null` until a run has finished.

### Sync mode

With `--sync`, hook waits for the command and answers with its combined
stdout and stderr: `200` if it exited 0, `500` otherwise. The exit code is in
the `X-Hook-Exit-Code` header either way. Output beyond 1 MB is cut off in the
response (the log still gets all of it).

```console
$ hook --sync 'git -C /srv/site pull --ff-only'
$ curl -si -X POST localhost:9000/
HTTP/1.1 200 OK
X-Hook-Exit-Code: 0
X-Hook-Run: 1

Already up to date.
```

## Security

hook binds to `127.0.0.1` by default, so only programs on the same machine
can reach it. `--bind 0.0.0.0` exposes it to every network the machine is on,
and then anyone who can reach the port can run your command. Use `--secret`
whenever hook is reachable from outside, or better, keep it on localhost
behind the reverse proxy that already terminates TLS for you (see
[examples](#examples)). The status endpoint is never authenticated; it shows
only counts and the last exit code.

### Signatures

With `--secret S` (or `HOOK_SECRET=S` in the environment) every POST must be
signed with HMAC-SHA256 over the raw body, or it is rejected with `401`
before anything runs:

- `X-Hub-Signature-256: sha256=<hex>`, as sent by GitHub, or
- `X-Signature: <hex>` (with or without the `sha256=` prefix) for other senders.

If both are present, `X-Hub-Signature-256` is the one checked. Comparison is
constant-time. Prefer `HOOK_SECRET` over `--secret` so the secret doesn't show
up in `ps`.

Signing a test request by hand:

```sh
body='{"ref":"refs/heads/main"}'
sig=$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$HOOK_SECRET" | sed 's/^.* //')
curl -X POST -H "X-Hub-Signature-256: sha256=$sig" -d "$body" localhost:9000/
```

## Queueing

Runs go through a queue and execute one at a time, in the order the requests
arrived, so two pushes in quick succession never deploy on top of each other.
`--concurrent` lets runs overlap instead.

`--max-queue N` (default 100) caps how many runs may be pending at once,
counting the running ones; requests beyond that get `503`. With
`--max-queue 1`, a push that arrives while a deploy is running is refused
rather than queued; with `--max-queue 2`, at most one waits behind it. With
`--concurrent`, it limits how many runs execute in parallel.

## Timeouts and limits

`--timeout 5m` kills a run that takes longer than that. The command runs in
its own process group and the whole group is killed (`SIGKILL`), so
`sh -c 'make deploy'` doesn't leave `make` and its children behind. A run
killed this way reports exit code `124`.

`--max-body` (default `10MB`) rejects larger request bodies with `413` before
anything runs. Sizes take an optional `B`, `KB`, `MB` or `GB` suffix (binary
units); `0` turns the limit off. A value greater than 0 that rounds down to
nothing, such as `0.5`, is rejected so it can't quietly turn the limit off.

If the command starts a background process that keeps its stdout or stderr
open, hook stops reading 2 seconds after the command itself exits, so the
queue isn't held up. Redirect the output of anything you start in the
background (`nohup ./server >server.log 2>&1 &`).

## Logging

hook logs to stderr: one access line per request (time, client, method, path,
status, and for runs the run number, duration and exit code), and the
command's output with each line prefixed by its run number. `-q` turns off
the command's output; the access log stays.

```
2026-06-25T18:40:03Z listening on http://127.0.0.1:9000/deploy, running ["./deploy.sh"]
[#1] Fetching origin
[#1] Deployed 3f2a9c1
2026-06-25T18:40:05Z 127.0.0.1:52144 POST /deploy 202 #1 1.874s exit=0
2026-06-25T18:40:09Z 127.0.0.1:52150 GET /deploy 200
2026-06-25T18:40:11Z 127.0.0.1:52158 POST /deploy 401
```

The access line for a run is written when the run finishes.

## Shutting down

On `SIGINT` or `SIGTERM`, hook stops accepting requests, drops runs that are
still waiting in the queue (with `--sync`, their requests get `503`) and waits
for the running command to finish, up to `--shutdown-timeout` (default
`30s`). After that, or on a second signal, the command's process group is
killed.

Because runs have their own process group (and session, so no terminal),
Ctrl-C in the terminal reaches only hook, not the command, so a deploy in
progress isn't cut off halfway.

## Exit codes

hook itself exits with:

| Code | Meaning                                                      |
|------|--------------------------------------------------------------|
| `0`  | clean shutdown, or `--help` / `--version`                    |
| `1`  | could not listen, or running commands had to be killed on exit |
| `2`  | bad flags or no command given                                |

`X-Hook-Exit-Code`, `last_exit_code` and the access log report the command's
exit status, or:

| Code    | Meaning                                          |
|---------|--------------------------------------------------|
| `124`   | killed by `--timeout`                            |
| `127`   | the command could not be started                 |
| `128+n` | killed by signal `n` (e.g. `137` for `SIGKILL`)  |

## Flags

| Flag                 | Default        | Description                                        |
|----------------------|----------------|----------------------------------------------------|
| `-p`, `--port`       | `9000`         | port to listen on                                  |
| `--bind`             | `127.0.0.1`    | address to listen on; `0.0.0.0` exposes hook        |
| `--path`             |                | only accept this URL path, `404` for others        |
| `--secret`           | `$HOOK_SECRET` | require a valid HMAC-SHA256 signature              |
| `--sync`             |                | wait and respond with the command's output         |
| `--concurrent`       |                | let runs overlap instead of queueing               |
| `--max-queue`        | `100`          | `503` once this many runs are pending (0 = no limit) |
| `--timeout`          | none           | kill a run after this long, e.g. `30s`, `5m`       |
| `--max-body`         | `10MB`         | `413` for larger bodies (0 = no limit)             |
| `--shutdown-timeout` | `30s`          | how long to wait for a running command on exit     |
| `-q`, `--quiet`      |                | don't forward the command's output                 |
| `-h`, `--help`       |                | show usage                                         |
| `--version`          |                | print version and exit                             |

## Examples

### Deploy on push to main

Create the GitHub webhook with content type `application/json`, a secret, and
the push event. The payload arrives on stdin:

```sh
#!/bin/sh
# deploy.sh
set -eu
[ "$HOOK_HEADER_X_GITHUB_EVENT" = push ] || { echo "ignoring $HOOK_HEADER_X_GITHUB_EVENT"; exit 0; }
ref=$(jq -r .ref)
[ "$ref" = refs/heads/main ] || { echo "ignoring $ref"; exit 0; }

git -C /srv/app pull --ff-only
make -C /srv/app deploy
```

```sh
HOOK_SECRET=change-me hook --path /deploy --timeout 10m --max-queue 2 -- ./deploy.sh
```

With `--max-queue 2`, a burst of pushes queues at most one deploy behind the
running one and the rest get `503`. Since that deploy pulls the latest
`main`, it picks up all of them.

### Behind a reverse proxy

Keep hook on localhost and let the proxy handle TLS. With Caddy:

```
example.com {
	reverse_proxy /deploy 127.0.0.1:9000
}
```

Behind a proxy, `HOOK_REMOTE_ADDR` is the proxy's address; the original
client is usually in `HOOK_HEADER_X_FORWARDED_FOR`.

### As a systemd service

```ini
[Unit]
Description=deploy webhook
After=network-online.target

[Service]
User=deploy
WorkingDirectory=/srv/app
# HOOK_SECRET=...
EnvironmentFile=/etc/hook.env
ExecStart=/usr/local/bin/hook --path /deploy --timeout 10m -- /srv/app/deploy.sh
# Signal only hook on stop, so it can finish a running deploy.
KillMode=mixed
TimeoutStopSec=45

[Install]
WantedBy=multi-user.target
```

### Quick notifications

```sh
# a desktop notification for every delivery (macOS)
hook 'osascript -e "display notification \"$HOOK_HEADER_X_GITHUB_EVENT\" with title \"webhook\""'
```

## Platform notes

hook is built and tested on Linux and macOS. Process groups, the timeout kill
and graceful shutdown work the same on both. The single-string form needs
`sh` on `PATH`; use `--` to run a program without a shell. On other systems a
timeout kills only the direct child, not the processes it started.

## License

MIT
