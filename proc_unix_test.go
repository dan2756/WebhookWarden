//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTimeoutKillsProcessGroup(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	// The background sleep keeps the output pipe open, so the run can only
	// end quickly if it is killed along with sh.
	script := `sleep 30 & echo $! > ` + pidfile + `; echo started; wait`
	t.Cleanup(func() {
		if b, err := os.ReadFile(pidfile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})

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
