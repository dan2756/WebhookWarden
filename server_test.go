package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
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

// waitUntil polls cond until it is true, failing the test after a few seconds.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
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

func TestSyncOutput(t *testing.T) {
	s, _ := newTestServer(t, config{sync: true, argv: []string{"sh", "-c", "echo out; echo err >&2; echo again"}})
	w := do(s, "POST", "/", "", nil)
	if w.Code != 200 {
		t.Errorf("status %d, want 200", w.Code)
	}
	if got := w.Header().Get("X-Hook-Exit-Code"); got != "0" {
		t.Errorf("X-Hook-Exit-Code = %q, want 0", got)
	}
	if got := w.Header().Get("X-Hook-Run"); got != "1" {
		t.Errorf("X-Hook-Run = %q, want 1", got)
	}
	if got, want := w.Body.String(), "out\nerr\nagain\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestSyncFailure(t *testing.T) {
	s, _ := newTestServer(t, config{sync: true, argv: []string{"sh", "-c", "echo boom >&2; exit 3"}})
	w := do(s, "POST", "/", "", nil)
	if w.Code != 500 {
		t.Errorf("status %d, want 500", w.Code)
	}
	if got := w.Header().Get("X-Hook-Exit-Code"); got != "3" {
		t.Errorf("X-Hook-Exit-Code = %q, want 3", got)
	}
	if got := w.Body.String(); got != "boom\n" {
		t.Errorf("body = %q, want %q", got, "boom\n")
	}
}

func TestSyncCommandNotFound(t *testing.T) {
	s, _ := newTestServer(t, config{sync: true, argv: []string{"./does-not-exist"}})
	w := do(s, "POST", "/", "", nil)
	if w.Code != 500 || w.Header().Get("X-Hook-Exit-Code") != "127" {
		t.Errorf("status %d, exit %q; want 500 and 127", w.Code, w.Header().Get("X-Hook-Exit-Code"))
	}
	if !strings.Contains(w.Body.String(), "does-not-exist") {
		t.Errorf("body should explain the failure, got %q", w.Body.String())
	}
}

func TestEnvAndStdin(t *testing.T) {
	script := `printf '%s\n' "$HOOK_METHOD" "$HOOK_PATH" "$HOOK_QUERY" "$HOOK_CONTENT_TYPE" ` +
		`"$HOOK_HEADER_X_GITHUB_EVENT" "$HOOK_HEADER_X_GITHUB_DELIVERY" "${HOOK_SECRET-unset}"; cat`
	s, _ := newTestServer(t, config{sync: true, secret: "s3cret", argv: []string{"sh", "-c", script}})

	body := `{"ref":"refs/heads/main"}`
	h := http.Header{
		"Content-Type":        {"application/json"},
		"X-Github-Event":      {"push"},
		"X-Github-Delivery":   {"72d3162e-cc78-11e3-81ab-4c9367dc0958"},
		"X-Hub-Signature-256": {"sha256=" + sign("s3cret", body)},
	}
	w := do(s, "POST", "/deploy?env=prod", body, h)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	want := strings.Join([]string{
		"POST",
		"/deploy",
		"env=prod",
		"application/json",
		"push",
		"72d3162e-cc78-11e3-81ab-4c9367dc0958",
		"unset",
		body,
	}, "\n")
	if got := w.Body.String(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRemoteAddrOverHTTP(t *testing.T) {
	s, _ := newTestServer(t, config{sync: true, argv: []string{"sh", "-c", `printf %s "$HOOK_REMOTE_ADDR"`}})
	ts := httptest.NewServer(s)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/", "text/plain", strings.NewReader("hi"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got bytes.Buffer
	got.ReadFrom(resp.Body)
	if !strings.HasPrefix(got.String(), "127.0.0.1:") {
		t.Errorf("HOOK_REMOTE_ADDR = %q, want 127.0.0.1:<port>", got.String())
	}
}

func TestOutputForwarding(t *testing.T) {
	s, logs := newTestServer(t, config{argv: []string{"sh", "-c", "echo one; echo two >&2; printf three"}})
	do(s, "POST", "/", "", nil)
	s.wait()
	if !strings.Contains(logs.String(), "[#1] one\n[#1] two\n[#1] three\n") {
		t.Errorf("output not forwarded with prefix:\n%s", logs.String())
	}

	s, logs = newTestServer(t, config{quiet: true, sync: true, argv: []string{"echo", "secret output"}})
	w := do(s, "POST", "/", "", nil)
	if strings.Contains(logs.String(), "secret output") {
		t.Errorf("-q should not forward output:\n%s", logs.String())
	}
	if w.Body.String() != "secret output\n" {
		t.Errorf("-q --sync should still return output, got %q", w.Body.String())
	}
}

func TestQueueFullReturns503(t *testing.T) {
	s, _ := newTestServer(t, config{maxQueue: 1})
	release := make(chan struct{})
	s.exec = func(context.Context, *job) result {
		<-release
		return result{}
	}

	if w := do(s, "POST", "/", "", nil); w.Code != 202 {
		t.Fatalf("first POST: status %d, want 202", w.Code)
	}
	if w := do(s, "POST", "/", "", nil); w.Code != 503 {
		t.Errorf("second POST: status %d, want 503", w.Code)
	}
	if st := getStatus(t, s, "/"); st.Running != 1 || st.Queued != 0 {
		t.Errorf("status: running %d queued %d, want 1 and 0", st.Running, st.Queued)
	}

	close(release)
	s.wait()
	if w := do(s, "POST", "/", "", nil); w.Code != 202 {
		t.Errorf("POST after the run finished: status %d, want 202", w.Code)
	}
}

func TestSyncWaitsForItsTurn(t *testing.T) {
	s, _ := newTestServer(t, config{sync: true})
	release := make(chan struct{})
	s.exec = func(_ context.Context, j *job) result {
		if j.id == 1 {
			<-release
		}
		return result{output: []byte(fmt.Sprintf("run %d\n", j.id))}
	}

	first := make(chan *httptest.ResponseRecorder)
	go func() { first <- do(s, "POST", "/", "", nil) }()
	waitUntil(t, "first run to start", func() bool { return s.status().Running == 1 })
	second := make(chan *httptest.ResponseRecorder)
	go func() { second <- do(s, "POST", "/", "", nil) }()
	waitUntil(t, "second run to queue", func() bool { return s.status().Queued == 1 })

	close(release)
	if w := <-first; w.Body.String() != "run 1\n" {
		t.Errorf("first response %q", w.Body.String())
	}
	if w := <-second; w.Body.String() != "run 2\n" {
		t.Errorf("second response %q", w.Body.String())
	}
}

func TestMaxBody(t *testing.T) {
	s, _ := newTestServer(t, config{maxBody: 16})

	if w := do(s, "POST", "/", strings.Repeat("x", 16), nil); w.Code != 202 {
		t.Errorf("16 byte body: status %d, want 202", w.Code)
	}
	if w := do(s, "POST", "/", strings.Repeat("x", 17), nil); w.Code != 413 {
		t.Errorf("17 byte body: status %d, want 413", w.Code)
	}

	// Without a Content-Length the limit is enforced while reading.
	r := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", 100)))
	r.ContentLength = -1
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Errorf("chunked 100 byte body: status %d, want 413", w.Code)
	}
	s.wait()
	if st := s.status(); st.Runs != 1 {
		t.Errorf("runs = %d, want 1", st.Runs)
	}
}

func TestShutdownFinishesRunningRun(t *testing.T) {
	s, logs := newTestServer(t, config{})
	release := make(chan struct{})
	var mu sync.Mutex
	var ran []int64
	s.exec = func(_ context.Context, j *job) result {
		if j.id == 1 {
			<-release
		}
		mu.Lock()
		ran = append(ran, j.id)
		mu.Unlock()
		return result{}
	}
	for range 3 {
		do(s, "POST", "/", "", nil)
	}
	waitUntil(t, "runs to queue", func() bool { return s.status().Queued == 2 })

	done := make(chan error)
	go func() { done <- s.shutdown(context.Background(), nil) }()
	waitUntil(t, "queue to close", func() bool { return s.status().Queued == 0 })

	if w := do(s, "POST", "/", "", nil); w.Code != 503 {
		t.Errorf("POST during shutdown: status %d, want 503", w.Code)
	}
	select {
	case err := <-done:
		t.Fatalf("shutdown returned (%v) while a run was in progress", err)
	default:
	}

	close(release)
	if err := <-done; err != nil {
		t.Errorf("shutdown: %v", err)
	}
	if len(ran) != 1 || ran[0] != 1 {
		t.Errorf("ran %v, want only #1", ran)
	}
	for _, want := range []string{"202 #2 dropped", "202 #3 dropped", "POST / 202 #1"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log is missing %q:\n%s", want, logs.String())
		}
	}
}

func TestShutdownAnswersQueuedSyncRequests(t *testing.T) {
	s, _ := newTestServer(t, config{sync: true})
	release := make(chan struct{})
	s.exec = func(context.Context, *job) result {
		<-release
		return result{output: []byte("done\n")}
	}

	first := make(chan *httptest.ResponseRecorder)
	go func() { first <- do(s, "POST", "/", "", nil) }()
	waitUntil(t, "first run to start", func() bool { return s.status().Running == 1 })
	second := make(chan *httptest.ResponseRecorder)
	go func() { second <- do(s, "POST", "/", "", nil) }()
	waitUntil(t, "second run to queue", func() bool { return s.status().Queued == 1 })

	done := make(chan error)
	go func() { done <- s.shutdown(context.Background(), nil) }()
	if w := <-second; w.Code != 503 {
		t.Errorf("queued sync request: status %d, want 503", w.Code)
	}
	close(release)
	if w := <-first; w.Code != 200 || w.Body.String() != "done\n" {
		t.Errorf("running sync request: status %d body %q", w.Code, w.Body.String())
	}
	if err := <-done; err != nil {
		t.Errorf("shutdown: %v", err)
	}
}
