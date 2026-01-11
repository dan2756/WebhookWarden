package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// syncBuffer is a bytes.Buffer that is safe to write from the logger and
// from the command's output copier at the same time.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func newTestServer(t *testing.T, cfg config) (*server, *syncBuffer) {
	t.Helper()
	if cfg.argv == nil {
		cfg.argv = []string{"true"}
	}
	logs := &syncBuffer{}
	s := newServer(&cfg, newLogger(logs))
	t.Cleanup(s.wait)
	return s, logs
}

func do(s http.Handler, method, target, body string, header http.Header) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range header {
		r.Header[k] = v
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestMethodsAndPaths(t *testing.T) {
	tests := []struct {
		path   string // --path
		method string
		target string
		want   int
		allow  string
	}{
		{"", "POST", "/", 202, ""},
		{"", "POST", "/anything/at/all?x=1", 202, ""},
		{"", "GET", "/", 200, ""},
		{"", "HEAD", "/", 200, ""},
		{"", "GET", "/other", 405, "POST"},
		{"", "PUT", "/", 405, "GET, HEAD, POST"},
		{"", "DELETE", "/x", 405, "POST"},

		{"/deploy", "POST", "/deploy", 202, ""},
		{"/deploy", "POST", "/deploy?ref=main", 202, ""},
		{"/deploy", "GET", "/deploy", 200, ""},
		{"/deploy", "PATCH", "/deploy", 405, "GET, HEAD, POST"},
		{"/deploy", "POST", "/", 404, ""},
		{"/deploy", "GET", "/", 404, ""},
		{"/deploy", "POST", "/deploy/", 404, ""},
		{"/deploy", "POST", "/deployx", 404, ""},
	}
	for _, tt := range tests {
		s, _ := newTestServer(t, config{path: tt.path})
		w := do(s, tt.method, tt.target, "", nil)
		if w.Code != tt.want {
			t.Errorf("--path=%q %s %s: status %d, want %d", tt.path, tt.method, tt.target, w.Code, tt.want)
		}
		if got := w.Header().Get("Allow"); got != tt.allow {
			t.Errorf("--path=%q %s %s: Allow %q, want %q", tt.path, tt.method, tt.target, got, tt.allow)
		}
	}
}

func getStatus(t *testing.T, s http.Handler, target string) statusReport {
	t.Helper()
	w := do(s, "GET", target, "", nil)
	if w.Code != 200 {
		t.Fatalf("GET %s: status %d", target, w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("GET %s: Content-Type %q", target, ct)
	}
	var st statusReport
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("decoding status %q: %v", w.Body.String(), err)
	}
	return st
}

func TestStatus(t *testing.T) {
	s, _ := newTestServer(t, config{argv: []string{"sh", "-c", "exit 3"}})

	st := getStatus(t, s, "/")
	if st.Runs != 0 || st.LastExitCode != nil || st.LastRun != nil {
		t.Errorf("before any run: %+v", st)
	}

	if w := do(s, "POST", "/", "", nil); w.Code != 202 {
		t.Fatalf("POST: status %d", w.Code)
	}
	s.wait()

	st = getStatus(t, s, "/")
	if st.Runs != 1 {
		t.Errorf("runs = %d, want 1", st.Runs)
	}
	if st.LastExitCode == nil || *st.LastExitCode != 3 {
		t.Errorf("last_exit_code = %v, want 3", st.LastExitCode)
	}
	if st.LastRun == nil || st.LastRun.IsZero() {
		t.Errorf("last_run not set")
	}
}

func TestSecretRequired(t *testing.T) {
	s, _ := newTestServer(t, config{secret: "s3cret"})
	body := `{"ref":"refs/heads/main"}`

	if w := do(s, "POST", "/", body, nil); w.Code != 401 {
		t.Errorf("unsigned: status %d, want 401", w.Code)
	}
	bad := http.Header{"X-Hub-Signature-256": {"sha256=" + sign("nope", body)}}
	if w := do(s, "POST", "/", body, bad); w.Code != 401 {
		t.Errorf("wrong secret: status %d, want 401", w.Code)
	}
	good := http.Header{"X-Hub-Signature-256": {"sha256=" + sign("s3cret", body)}}
	if w := do(s, "POST", "/", body, good); w.Code != 202 {
		t.Errorf("signed: status %d, want 202", w.Code)
	}
	fallback := http.Header{"X-Signature": {sign("s3cret", body)}}
	if w := do(s, "POST", "/", body, fallback); w.Code != 202 {
		t.Errorf("X-Signature: status %d, want 202", w.Code)
	}
	s.wait()

	if st := getStatus(t, s, "/"); st.Runs != 2 {
		t.Errorf("runs = %d, want 2 (rejected requests must not run)", st.Runs)
	}
}

func TestAccessLog(t *testing.T) {
	s, logs := newTestServer(t, config{path: "/hook"})
	do(s, "POST", "/hook", "", nil)
	do(s, "GET", "/nope", "", nil)
	s.wait()

	out := logs.String()
	for _, want := range []string{"POST /hook 202 #1 ", " exit=0", "GET /nope 404"} {
		if !strings.Contains(out, want) {
			t.Errorf("access log is missing %q:\n%s", want, out)
		}
	}
}
