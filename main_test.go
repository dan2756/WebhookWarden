package main

import (
	"errors"
	"flag"
	"slices"
	"testing"
)

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
		cfg, err := parseArgs(tt.args)
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
	cfg, err := parseArgs([]string{"--port", "8080", "--bind", "0.0.0.0", "true"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.port != 8080 || cfg.bind != "0.0.0.0" {
		t.Errorf("got port %d bind %q", cfg.port, cfg.bind)
	}

	cfg, err = parseArgs([]string{"true"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.port != 9000 || cfg.bind != "127.0.0.1" {
		t.Errorf("defaults: got port %d bind %q", cfg.port, cfg.bind)
	}
}

func TestParseArgsErrors(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"-p", "9000"},
		{"--"},
		{"-p", "70000", "true"},
		{"--nope", "true"},
	} {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%q) succeeded, want error", args)
		}
	}
	if _, err := parseArgs([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h: got %v, want flag.ErrHelp", err)
	}
}
