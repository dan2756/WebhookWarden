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
	r.Header.Add("X-Tag", "blue")
	r.Header.Add("X-Tag", "green")
	r.Header["X_tag"] = []string{"red"}

	env := buildEnv([]string{"PATH=/usr/bin", "HOOK_SECRET=s3cret", "HOOK_HEADER_STALE=1"}, r)
	want := []string{
		"PATH=/usr/bin",
		"HOOK_METHOD=POST",
		"HOOK_PATH=/deploy",
		"HOOK_QUERY=ref=main&force=1",
		"HOOK_REMOTE_ADDR=192.0.2.10:51234",
		"HOOK_CONTENT_TYPE=application/json",
		"HOOK_HEADER_CONTENT_TYPE=application/json",
		"HOOK_HEADER_X_GITHUB_EVENT=push",
		"HOOK_HEADER_HOST=example.com",
		"HOOK_HEADER_X_TAG=blue, green, red",
	}
	for _, kv := range want {
		if !slices.Contains(env, kv) {
			t.Errorf("env is missing %q\ngot: %q", kv, env)
		}
	}
	var headers []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOOK_SECRET=") || strings.HasPrefix(kv, "HOOK_HEADER_STALE=") {
			t.Errorf("inherited %q should not be passed to the command", kv)
		}
		if strings.HasPrefix(kv, "HOOK_HEADER_") {
			headers = append(headers, kv)
		}
	}
	if !slices.IsSorted(headers) {
		t.Errorf("HOOK_HEADER_ variables are not sorted: %q", headers)
	}
	if n := len(headers); n != 4 {
		t.Errorf("got %d HOOK_HEADER_ variables, want 4 (one per name): %q", n, headers)
	}
}
