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
the background. Runs never overlap.

By default any path is accepted. `--path /deploy` restricts hook to that one
path; everything else gets `404`. Methods other than POST get `405`, except
`GET /` (or `GET /deploy` with `--path`), which returns a small status
document:

```console
$ curl -s localhost:9000/
{"runs":12,"last_exit_code":0,"last_run":"2026-01-11T21:04:51Z"}
```

`last_exit_code` and `last_run` are `null` until the first run has finished.

The command gets the request body on stdin and these environment variables:

| Variable              | Value                                          |
|-----------------------|------------------------------------------------|
| `HOOK_METHOD`         | request method                                 |
| `HOOK_PATH`           | URL path, e.g. `/deploy`                       |
| `HOOK_QUERY`          | raw query string, without the `?`              |
| `HOOK_REMOTE_ADDR`    | client address, `ip:port`                      |
| `HOOK_CONTENT_TYPE`   | `Content-Type` of the request                  |
| `HOOK_HEADER_<NAME>`  | every request header, e.g. `HOOK_HEADER_X_GITHUB_EVENT` |

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
| `--version`     |             | print version and exit                   |

## License

MIT
