//go:build unix

package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// killPidfileOnCleanup kills the process whose pid a test command wrote to
// pidfile, so a background process it started doesn't outlive the test.
func killPidfileOnCleanup(t *testing.T, pidfile string) {
	t.Cleanup(func() {
		if b, err := os.ReadFile(pidfile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
}

func TestTimeoutKillsProcessGroup(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	// The background sleep keeps the output pipe open, so the run can only
	// end quickly if it is killed along with sh.
	script := `sleep 30 & echo $! > ` + pidfile + `; echo started; wait`
	killPidfileOnCleanup(t, pidfile)

	s, _ := newTestServer(t, config{sync: true, timeout: 200 * time.Millisecond, argv: []string{"sh", "-c", script}})
	start := time.Now()
	w := do(s, "POST", "/", "", nil)
	elapsed := time.Since(start)

	if w.Code != 500 {
		t.Errorf("status %d, want 500", w.Code)
	}
	if got := w.Header().Get("X-Hook-Exit-Code"); got != "124" {
		t.Errorf("X-Hook-Exit-Code = %q, want 124", got)
	}
	if body := w.Body.String(); !strings.HasPrefix(body, "started\n") || !strings.Contains(body, "timed out after 200ms") {
		t.Errorf("body = %q", body)
	}
	if elapsed >= waitDelay {
		t.Errorf("run took %s; the background sleep was not killed with its group", elapsed)
	}
}

func TestSignalExitCode(t *testing.T) {
	s, _ := newTestServer(t, config{sync: true, argv: []string{"sh", "-c", "kill -TERM $$"}})
	w := do(s, "POST", "/", "", nil)
	if got := w.Header().Get("X-Hook-Exit-Code"); got != "143" {
		t.Errorf("X-Hook-Exit-Code = %q, want 143 (128+SIGTERM)", got)
	}
}

func TestShutdownKillsAfterTimeout(t *testing.T) {
	s, logs := newTestServer(t, config{argv: []string{"sh", "-c", "echo started; sleep 30"}})
	do(s, "POST", "/", "", nil)
	waitUntil(t, "command to start", func() bool { return strings.Contains(logs.String(), "[#1] started") })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := s.shutdown(ctx, nil); err == nil {
		t.Error("shutdown returned nil, want an error about killed commands")
	}
	if elapsed := time.Since(start); elapsed >= waitDelay {
		t.Errorf("shutdown took %s", elapsed)
	}
	if !strings.Contains(logs.String(), "POST / 202 #1") || !strings.Contains(logs.String(), "exit=137") {
		t.Errorf("want the run logged with exit=137 (SIGKILL):\n%s", logs.String())
	}
}

func TestTimeoutOnlyReportedWhenKilled(t *testing.T) {
	// The command exits at once, but the background sleep holds its output
	// open, so the deadline passes while hook is still reading it. Nothing
	// is killed, so the run keeps the command's own exit code.
	for _, code := range []string{"0", "3"} {
		t.Run("exit "+code, func(t *testing.T) {
			t.Parallel()
			pidfile := filepath.Join(t.TempDir(), "pid")
			script := `sleep 10 & echo $! > ` + pidfile + `; echo hi; exit ` + code
			killPidfileOnCleanup(t, pidfile)

			s, _ := newTestServer(t, config{sync: true, timeout: 500 * time.Millisecond, argv: []string{"sh", "-c", script}})
			w := do(s, "POST", "/", "", nil)
			if got := w.Header().Get("X-Hook-Exit-Code"); got != code {
				t.Errorf("X-Hook-Exit-Code = %q, want %s", got, code)
			}
			if body := w.Body.String(); strings.Contains(body, "timed out") {
				t.Errorf("body = %q, want no timeout", body)
			}
		})
	}
}

func TestShutdownAnswersKilledSyncRequest(t *testing.T) {
	s, logs := newTestServer(t, config{sync: true, argv: []string{"sh", "-c", "echo started; sleep 30"}})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: s}
	go hs.Serve(ln)

	type reply struct {
		code int
		exit string
		body string
		err  error
	}
	replies := make(chan reply, 1)
	go func() {
		resp, err := http.Post("http://"+ln.Addr().String()+"/", "text/plain", nil)
		if err != nil {
			replies <- reply{err: err}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		replies <- reply{resp.StatusCode, resp.Header.Get("X-Hook-Exit-Code"), string(body), err}
	}()
	waitUntil(t, "command to start", func() bool { return strings.Contains(logs.String(), "[#1] started") })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := s.shutdown(ctx, hs); err == nil {
		t.Error("shutdown returned nil, want an error about killed commands")
	}
	r := <-replies
	if r.err != nil {
		t.Fatalf("the killed run's request got no answer: %v", r.err)
	}
	if r.code != 500 || r.exit != "137" || !strings.HasPrefix(r.body, "started\n") {
		t.Errorf("got status %d, exit %q, body %q; want 500, 137 and the output so far", r.code, r.exit, r.body)
	}
}

func TestRunHasItsOwnSession(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("needs /proc")
	}
	// A session leader has no controlling terminal, so a command that
	// prompts on /dev/tty fails instead of being stopped by SIGTTIN.
	script := `read -r pid comm state ppid pgrp sid rest </proc/$$/stat; echo "$$ $sid"`
	s, _ := newTestServer(t, config{sync: true, argv: []string{"sh", "-c", script}})
	w := do(s, "POST", "/", "", nil)
	if f := strings.Fields(w.Body.String()); len(f) != 2 || f[0] != f[1] {
		t.Errorf("pid and session id = %q, want the run to lead its own session", w.Body.String())
	}
}
