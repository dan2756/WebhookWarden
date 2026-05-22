//go:build unix

package main

import (
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
