package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestPrefixWriter(t *testing.T) {
	var out syncBuffer
	w := &prefixWriter{log: newLogger(&out), prefix: "[#7] "}
	for _, chunk := range []string{"hel", "lo\nwor", "ld\n", "\n", "no newline"} {
		w.Write([]byte(chunk))
	}
	w.Flush()
	want := "[#7] hello\n[#7] world\n[#7] \n[#7] no newline\n"
	if got := out.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPrefixWriterCarriageReturn(t *testing.T) {
	var out syncBuffer
	w := &prefixWriter{log: newLogger(&out), prefix: "[#1] "}
	// Windows newlines, a progress rewrite, and a payload that tries to
	// paint over the prefix all have to stay one prefixed line.
	for _, chunk := range []string{"hello\r\n", "10%\r20%\r", "30%\n", "\rforged access line\n"} {
		if n, err := w.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	w.Flush()
	want := "[#1] hello\n[#1] 10%20%30%\n[#1] forged access line\n"
	if got := out.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if strings.Contains(out.String(), "\r") {
		t.Errorf("carriage return survived into the log: %q", out.String())
	}
}

func TestPrefixWriterTerminalControls(t *testing.T) {
	var out syncBuffer
	w := &prefixWriter{log: newLogger(&out), prefix: "[#1] "}
	chunks := []string{
		"ok\n",
		"\x1b[1G\x1b[2Kforged\b\b\b\n", // cursor home, clear line, backspaces
		"\x1b]0;title\a\n",             // OSC window title
		"a\x1b",                        // escape split across writes
		"[31mred\x1b[0m\n",
		"a\tb café ©\n",
		"\xc2\x9b2Jcleared\n", // U+009B, the 8-bit form of CSI
		"\xc2", "\x9b31mhidden", "\n",
		"up\n\x1bMdown\n", // ESC M would move the cursor up a line
		"\x1b[2Aearlier\n",
		"a\fb\n",
		"pre\x1b[31", // unfinished sequence must not leak on Flush
	}
	for _, chunk := range chunks {
		if n, err := w.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	w.Flush()
	want := strings.Join([]string{
		"[#1] ok",
		"[#1] forged",
		"[#1] ",
		"[#1] ared",
		"[#1] a\tb café ©",
		"[#1] cleared",
		"[#1] hidden",
		"[#1] up",
		"[#1] down",
		"[#1] earlier",
		"[#1] ab",
		"[#1] pre",
		"",
	}, "\n")
	if got := out.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	for _, bad := range []string{"\x1b", "\r", "\b", "\xc2\x9b"} {
		if strings.Contains(out.String(), bad) {
			t.Errorf("control %q survived into the log: %q", bad, out.String())
		}
	}
}

func TestPrefixWriterLongLine(t *testing.T) {
	var out syncBuffer
	w := &prefixWriter{log: newLogger(&out), prefix: "> "}
	chunk := []byte(strings.Repeat("x", 1024))
	for range maxLine / len(chunk) {
		w.Write(chunk)
	}
	w.Write([]byte("tail"))
	w.Flush()
	want := "> " + strings.Repeat("x", maxLine) + "\n> tail\n"
	if got := out.String(); got != want {
		t.Errorf("got %d bytes, want a %d byte line and then \"tail\"", len(got), maxLine)
	}
}

func TestTimeoutOverride(t *testing.T) {
	exited := exec.Command("true")
	if err := exited.Run(); err != nil {
		t.Fatal(err)
	}
	failed := exec.Command("sh", "-c", "exit 3")
	if err := failed.Run(); err == nil {
		t.Fatal("exit 3 should fail")
	}
	killed := exec.Command("sh", "-c", "kill -KILL $$")
	_ = killed.Run()
	if !signaled(killed.ProcessState) {
		t.Fatal("kill -KILL should leave a signaled process")
	}

	deadline := context.DeadlineExceeded
	for _, ps := range []*exec.Cmd{exited, failed} {
		if timeoutOverride(ps.ProcessState, true, deadline) {
			t.Errorf("exit %d: a finished command is not a timeout kill", ps.ProcessState.ExitCode())
		}
	}
	if !timeoutOverride(killed.ProcessState, true, deadline) {
		t.Error("a signaled command at the deadline should count as a timeout")
	}
	if timeoutOverride(killed.ProcessState, true, context.Canceled) {
		t.Error("a shutdown kill is not a timeout")
	}
	if timeoutOverride(killed.ProcessState, false, deadline) {
		t.Error("a deadline without a kill is not a timeout")
	}
}

func TestCapBuffer(t *testing.T) {
	b := &capBuffer{max: 5}
	for _, s := range []string{"ab", "cd", "efg", "h"} {
		if n, err := b.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", s, n, err)
		}
	}
	if got := b.buf.String(); got != "abcde" || !b.truncated {
		t.Errorf("got %q truncated=%v, want %q truncated=true", got, b.truncated, "abcde")
	}
}
