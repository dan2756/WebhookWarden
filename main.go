// Command hook is a small local webhook receiver that runs a command on
// every POST it receives.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

const usage = `hook - run a command on every webhook POST

Usage:
  hook [flags] -- <program> [args...]   run program directly
  hook [flags] '<shell command>'        run a single string with sh -c

Each POST runs the command once, with the request body on stdin and the
request details in HOOK_* environment variables. GET on / (or on --path)
returns a small JSON status document; other methods get 405.

Flags:
  -p, --port N      port to listen on (default 9000)
      --bind ADDR   address to bind (default 127.0.0.1)
      --path PATH   only serve PATH; any other path is 404
      --secret S    require a valid HMAC-SHA256 signature (or set HOOK_SECRET)
      --sync        wait for the command and respond with its output
                    (200 on success, 500 on a non-zero exit)
  -q, --quiet       don't forward the command's output to stderr
  -h, --help        show this help
      --version     print the version and exit

Environment passed to the command:
  HOOK_METHOD, HOOK_PATH, HOOK_QUERY, HOOK_REMOTE_ADDR, HOOK_CONTENT_TYPE
  HOOK_HEADER_<NAME>  one per request header, e.g. HOOK_HEADER_X_GITHUB_EVENT
`

type config struct {
	port        int
	bind        string
	path        string
	secret      string
	sync        bool
	quiet       bool
	argv        []string
	showVersion bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseArgs(args, os.Getenv)
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, usage)
		return 0
	case err != nil:
		fmt.Fprintf(stderr, "hook: %v\nRun 'hook --help' for usage.\n", err)
		return 2
	case cfg.showVersion:
		fmt.Fprintln(stdout, "hook", version())
		return 0
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(cfg.bind, strconv.Itoa(cfg.port)))
	if err != nil {
		fmt.Fprintf(stderr, "hook: %v\n", err)
		return 1
	}
	log := newLogger(stderr)
	srv := newServer(cfg, log)
	log.Printf("listening on http://%s%s, running %q", ln.Addr(), cfg.path, cfg.argv)
	if cfg.secret == "" && !isLoopback(cfg.bind) {
		log.Printf("warning: listening on %s without --secret, anyone who can reach it can run the command", cfg.bind)
	}

	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	if err := hs.Serve(ln); err != nil {
		log.Printf("%v", err)
		return 1
	}
	return 0
}

func parseArgs(args []string, getenv func(string) string) (*config, error) {
	cfg := &config{}
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.IntVar(&cfg.port, "p", 9000, "")
	fs.IntVar(&cfg.port, "port", 9000, "")
	fs.StringVar(&cfg.bind, "bind", "127.0.0.1", "")
	fs.StringVar(&cfg.path, "path", "", "")
	fs.StringVar(&cfg.secret, "secret", getenv("HOOK_SECRET"), "")
	fs.BoolVar(&cfg.sync, "sync", false, "")
	fs.BoolVar(&cfg.quiet, "q", false, "")
	fs.BoolVar(&cfg.quiet, "quiet", false, "")
	fs.BoolVar(&cfg.showVersion, "version", false, "")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if cfg.showVersion {
		return cfg, nil
	}
	if cfg.port < 0 || cfg.port > 65535 {
		return nil, fmt.Errorf("invalid port %d", cfg.port)
	}
	if cfg.path != "" && !strings.HasPrefix(cfg.path, "/") {
		return nil, fmt.Errorf("--path must start with /, got %q", cfg.path)
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return nil, errors.New("no command given")
	}
	// A single argument without "--" is a shell command line, so that
	// `hook 'make deploy && say done'` does what it looks like. Anything
	// else is executed directly, without a shell.
	n := len(args) - len(rest)
	dashdash := n > 0 && args[n-1] == "--"
	if len(rest) == 1 && !dashdash {
		cfg.argv = []string{"sh", "-c", rest[0]}
	} else {
		cfg.argv = rest
	}
	return cfg, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
