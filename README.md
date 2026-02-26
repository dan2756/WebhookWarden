# hook

A local webhook receiver that runs a command on each POST.

Point a GitHub (or any other) webhook at it, and every delivery runs your
deploy script with the request body on stdin and the request details in
environment variables. No config files, no framework.

## Install

```sh
go install github.com/dan2756/hook@latest
```

## Usage

```sh
# run a program directly (everything after -- is the command)
hook -p 9000 -- ./deploy.sh production

# or give a single string, which is run with sh -c
hook -p 9000 'make deploy && say done'
```

Each POST is answered with `202 Accepted` right away and the command runs in
the background.

## What the command gets

The request body is piped to the command's stdin, and the request details
are in these environment variables:

| Variable              | Value                                          |
|-----------------------|------------------------------------------------|
| `HOOK_METHOD`         | request method                                 |
| `HOOK_PATH`           | URL path, e.g. `/deploy`                       |
| `HOOK_QUERY`          | raw query string, without the `?`              |
| `HOOK_REMOTE_ADDR`    | client address, `ip:port`                      |
| `HOOK_CONTENT_TYPE`   | `Content-Type` of the request                  |
| `HOOK_HEADER_<NAME>`  | every request header, e.g. `HOOK_HEADER_X_GITHUB_EVENT` |

## Paths and status

By default any path is accepted. `--path /deploy` restricts hook to that one
path; everything else gets `404`. Methods other than POST get `405`, except
`GET /` (or `GET /deploy` with `--path`), which returns a small status
document:

```console
$ curl -s localhost:9000/
{"runs":12,"running":1,"queued":0,"last_exit_code":0,"last_run":"2026-01-11T21:04:51Z"}
```

`last_exit_code` and `last_run` are `null` until the first run has finished.

## Queueing

Runs go through a queue and execute one at a time, in the order the requests
arrived, so two pushes in quick succession never deploy on top of each other.
`--concurrent` lets runs overlap instead.

`--max-queue N` (default 100) caps how many runs may be pending at once,
counting the running ones. Requests beyond that get `503`. With
`--max-queue 1`, a push that arrives while a deploy is running is refused
rather than queued; with `--max-queue 2`, at most one waits behind it.

## Sync mode

With `--sync`, hook waits for the command and answers with its combined
stdout and stderr: `200` if it exited 0, `500` otherwise. The exit code is in
the `X-Hook-Exit-Code` header either way (`127` if the command could not be
started). Every response also carries the run number in `X-Hook-Run`.

```console
$ hook --sync 'git -C /srv/site pull --ff-only'
$ curl -si -X POST localhost:9000/
HTTP/1.1 200 OK
X-Hook-Exit-Code: 0
X-Hook-Run: 1

Already up to date.
```

## Logging

hook logs to stderr: one access line per request, and the command's output
with each line prefixed by its run number (`-q` turns the latter off).

```
2026-01-14T19:20:03Z listening on http://127.0.0.1:9000/deploy, running ["./deploy.sh"]
[#1] Fetching origin
[#1] Deployed 3f2a9c1
2026-01-14T19:20:05Z 127.0.0.1:52144 POST /deploy 202 #1 1.874s exit=0
2026-01-14T19:20:09Z 127.0.0.1:52150 GET /deploy 200
```

For runs, the access line is written when the run finishes and includes its
duration and exit code.

## Signatures

With `--secret S` (or `HOOK_SECRET=S` in the environment) every POST must be
signed with HMAC-SHA256 over the raw body, or it is rejected with `401`:

- `X-Hub-Signature-256: sha256=<hex>`, as sent by GitHub, or
- `X-Signature: <hex>` (with or without the `sha256=` prefix) for other senders.

Comparison is constant-time. Prefer `HOOK_SECRET` over `--secret` so the secret
doesn't show up in `ps`; it is not passed on to the command.

Signing a test request by hand:

```sh
body='{"ref":"refs/heads/main"}'
sig=$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$HOOK_SECRET" | sed 's/^.* //')
curl -X POST -H "X-Hub-Signature-256: sha256=$sig" -d "$body" localhost:9000/
```

## Flags

| Flag            | Default     | Description                              |
|-----------------|-------------|------------------------------------------|
| `-p`, `--port`  | `9000`      | port to listen on                        |
| `--bind`        | `127.0.0.1` | address to listen on                     |
| `--path`        |             | only accept this path, `404` for others  |
| `--secret`      | `$HOOK_SECRET` | require a valid HMAC-SHA256 signature |
| `--sync`        |             | wait and respond with the command's output |
| `--concurrent`  |             | let runs overlap instead of queueing     |
| `--max-queue`   | `100`       | `503` once this many runs are pending (0 = no limit) |
| `-q`, `--quiet` |             | don't forward the command's output       |
| `--version`     |             | print version and exit                   |

## License

MIT
