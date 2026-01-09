package main

import (
	"net/http"
	"strings"
)

// buildEnv returns the environment for a run: hook's own environment plus
// HOOK_* variables describing the request. HOOK_* variables inherited from
// hook's environment (HOOK_SECRET in particular) are dropped, so everything
// in that namespace comes from the request.
func buildEnv(base []string, r *http.Request) []string {
	env := make([]string, 0, len(base)+5+len(r.Header))
	for _, kv := range base {
		if !strings.HasPrefix(kv, "HOOK_") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"HOOK_METHOD="+r.Method,
		"HOOK_PATH="+r.URL.Path,
		"HOOK_QUERY="+r.URL.RawQuery,
		"HOOK_REMOTE_ADDR="+r.RemoteAddr,
		"HOOK_CONTENT_TYPE="+r.Header.Get("Content-Type"),
	)
	for name, values := range r.Header {
		env = append(env, "HOOK_HEADER_"+envName(name)+"="+values[0])
	}
	return env
}

// envName turns a header name into an environment variable suffix:
// upper case, with anything that isn't a letter or digit replaced by '_'.
// "X-GitHub-Event" becomes "X_GITHUB_EVENT".
func envName(header string) string {
	var b strings.Builder
	b.Grow(len(header))
	for i := 0; i < len(header); i++ {
		c := header[i]
		switch {
		case 'a' <= c && c <= 'z':
			c -= 'a' - 'A'
		case 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		default:
			c = '_'
		}
		b.WriteByte(c)
	}
	return b.String()
}
