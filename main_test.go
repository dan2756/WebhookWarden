package main

import (
	"errors"
	"flag"
	"net"
	"slices"
	"strconv"
	"testing"
)

func noenv(string) string { return "" }

func TestParseArgsCommand(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{[]string{"make deploy && say done"}, []string{"sh", "-c", "make deploy && say done"}},
		{[]string{"-p", "9001", "make deploy"}, []string{"sh", "-c", "make deploy"}},
		{[]string{"--", "./deploy.sh", "arg"}, []string{"./deploy.sh", "arg"}},
		{[]string{"-p", "9001", "--", "./deploy.sh"}, []string{"./deploy.sh"}},
		{[]string{"./deploy.sh", "-x"}, []string{"./deploy.sh", "-x"}},
	}
	for _, tt := range tests {
		cfg, err := parseArgs(tt.args, noenv)
		if err != nil {
			t.Errorf("parseArgs(%q): %v", tt.args, err)
			continue
		}
		if !slices.Equal(cfg.argv, tt.want) {
			t.Errorf("parseArgs(%q) argv = %q, want %q", tt.args, cfg.argv, tt.want)
		}
	}
}

func TestParseArgsFlags(t *testing.T) {
	cfg, err := parseArgs([]string{"--port", "8080", "--bind", "0.0.0.0", "true"}, noenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.port != 8080 || cfg.bind != "0.0.0.0" {
		t.Errorf("got port %d bind %q", cfg.port, cfg.bind)
	}

	cfg, err = parseArgs([]string{"true"}, noenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.port != 9000 || cfg.bind != "127.0.0.1" {
		t.Errorf("defaults: got port %d bind %q", cfg.port, cfg.bind)
	}

	cfg, err = parseArgs([]string{"--path", "/deploy", "true"}, noenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.path != "/deploy" {
		t.Errorf("path = %q, want /deploy", cfg.path)
	}
}

func TestParseArgsBind(t *testing.T) {
	cfg, err := parseArgs([]string{"--bind", "[::1]", "--port", "0", "true"}, noenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.bind != "::1" || !isLoopback(cfg.bind) {
		t.Fatalf("bind = %q, loopback %v; want ::1 and loopback", cfg.bind, isLoopback(cfg.bind))
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(cfg.bind, strconv.Itoa(cfg.port)))
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()

	cfg, err = parseArgs([]string{"--bind", "[127.0.0.1]", "true"}, noenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.bind != "127.0.0.1" || !isLoopback(cfg.bind) {
		t.Errorf("bind = %q, loopback %v", cfg.bind, isLoopback(cfg.bind))
	}

	cfg, err = parseArgs([]string{"--bind", "[::]", "true"}, noenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.bind != "::" || isLoopback(cfg.bind) {
		t.Errorf("bind = %q, loopback %v; want :: and not loopback", cfg.bind, isLoopback(cfg.bind))
	}

	for _, host := range []string{"127.0.0.1", "localhost", "::1", "::ffff:127.0.0.1"} {
		if !isLoopback(host) {
			t.Errorf("%s should be loopback", host)
		}
	}
	for _, host := range []string{"0.0.0.0", "::", "192.0.2.1", "[::1]"} {
		if isLoopback(host) {
			t.Errorf("%s should not be loopback before brackets are stripped", host)
		}
	}
}

func TestParseArgsMaxBody(t *testing.T) {
	cfg, err := parseArgs([]string{"--max-body", "0", "true"}, noenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.maxBody != 0 {
		t.Errorf("maxBody = %d, want 0 (no limit)", cfg.maxBody)
	}

	cfg, err = parseArgs([]string{"--max-body", "1.5MB", "true"}, noenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.maxBody != 3<<19 {
		t.Errorf("maxBody = %d, want %d", cfg.maxBody, 3<<19)
	}
}

func TestParseArgsSecret(t *testing.T) {
	env := func(k string) string {
		if k == "HOOK_SECRET" {
			return "from-env"
		}
		return ""
	}
	cfg, err := parseArgs([]string{"true"}, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.secret != "from-env" {
		t.Errorf("secret = %q, want HOOK_SECRET value", cfg.secret)
	}
	cfg, err = parseArgs([]string{"--secret", "from-flag", "true"}, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.secret != "from-flag" {
		t.Errorf("secret = %q, want --secret to win over HOOK_SECRET", cfg.secret)
	}
}

func TestParseArgsErrors(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"-p", "9000"},
		{"--"},
		{"-p", "70000", "true"},
		{"--nope", "true"},
		{"--path", "deploy", "true"},
		{"--max-queue", "-1", "true"},
		{"--max-body", "0.5", "true"},
		{"--max-body", "0.9B", "true"},
		{"--path", "/deploy\n", "true"},
		{"--path", "/deploy\x00", "true"},
		{"--path", "/deploy?x=1", "true"},
		{"--path", "/deploy#frag", "true"},
		{"--bind", "", "true"},
		{"--bind", "[]", "true"},
	} {
		if _, err := parseArgs(args, noenv); err == nil {
			t.Errorf("parseArgs(%q) succeeded, want error", args)
		}
	}
	if _, err := parseArgs([]string{"-h"}, noenv); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h: got %v, want flag.ErrHelp", err)
	}
}
