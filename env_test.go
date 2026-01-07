package main

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestEnvName(t *testing.T) {
	tests := map[string]string{
		"X-GitHub-Event":      "X_GITHUB_EVENT",
		"Content-Type":        "CONTENT_TYPE",
		"X-Hub-Signature-256": "X_HUB_SIGNATURE_256",
		"x.custom header":     "X_CUSTOM_HEADER",
		"":                    "",
	}
	for in, want := range tests {
		if got := envName(in); got != want {
			t.Errorf("envName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildEnv(t *testing.T) {
	r := httptest.NewRequest("POST", "/deploy?ref=main&force=1", strings.NewReader("{}"))
	r.RemoteAddr = "192.0.2.10:51234"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-GitHub-Event", "push")

	env := buildEnv([]string{"PATH=/usr/bin"}, r)
	want := []string{
		"PATH=/usr/bin",
		"HOOK_METHOD=POST",
		"HOOK_PATH=/deploy",
		"HOOK_QUERY=ref=main&force=1",
		"HOOK_REMOTE_ADDR=192.0.2.10:51234",
		"HOOK_CONTENT_TYPE=application/json",
		"HOOK_HEADER_CONTENT_TYPE=application/json",
		"HOOK_HEADER_X_GITHUB_EVENT=push",
	}
	for _, kv := range want {
		if !slices.Contains(env, kv) {
			t.Errorf("env is missing %q\ngot: %q", kv, env)
		}
	}
}
