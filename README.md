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
the background. Runs never overlap. Other methods get `405`.

The command gets the request body on stdin and these environment variables:

| Variable              | Value                                          |
|-----------------------|------------------------------------------------|
| `HOOK_METHOD`         | request method                                 |
| `HOOK_PATH`           | URL path, e.g. `/deploy`                       |
| `HOOK_QUERY`          | raw query string, without the `?`              |
| `HOOK_REMOTE_ADDR`    | client address, `ip:port`                      |
| `HOOK_CONTENT_TYPE`   | `Content-Type` of the request                  |
| `HOOK_HEADER_<NAME>`  | every request header, e.g. `HOOK_HEADER_X_GITHUB_EVENT` |

## Flags

| Flag            | Default     | Description            |
|-----------------|-------------|------------------------|
| `-p`, `--port`  | `9000`      | port to listen on      |
| `--bind`        | `127.0.0.1` | address to listen on   |
| `--version`     |             | print version and exit |

## License

MIT
