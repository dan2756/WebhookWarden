package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"time"
)

const (
	// maxCapture bounds how much output --sync keeps for the response.
	maxCapture = 1 << 20
	// maxLine bounds how long a forwarded output line can get before it is
	// flushed anyway.
	maxLine = 64 << 10
	// waitDelay is how long to keep reading output after the command has
	// exited or been killed. A background process that inherited stdout
	// would otherwise hold up the run (and the queue) until it exits.
	waitDelay = 2 * time.Second
	// exitTimeout is reported when --timeout killed the command, like
	// timeout(1) does.
	exitTimeout = 124
)

type result struct {
	exitCode  int
	duration  time.Duration
	output    []byte // combined stdout and stderr, only with --sync
	truncated bool
}

// execute runs the configured command for j and waits for it to finish.
// Cancelling ctx kills the command's whole process group.
func (s *server) execute(ctx context.Context, j *job) result {
	if s.cfg.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, s.cfg.argv[0], s.cfg.argv[1:]...)
	setProcessGroup(cmd)
	// Remember whether cancelling actually killed anything: the deadline
	// can also pass while hook is still reading the output of a command
	// that has already exited, and then the command's own exit code stands.
	var killed atomic.Bool
	kill := cmd.Cancel
	cmd.Cancel = func() error {
		err := kill()
		killed.Store(err == nil)
		return err
	}
	cmd.WaitDelay = waitDelay
	cmd.Env = j.env
	cmd.Stdin = bytes.NewReader(j.body)

	var writers []io.Writer
	var capture *capBuffer
	if s.cfg.sync {
		capture = &capBuffer{max: maxCapture}
		writers = append(writers, capture)
	}
	var fwd *prefixWriter
	if !s.cfg.quiet {
		fwd = &prefixWriter{log: s.log, prefix: fmt.Sprintf("[#%d] ", j.id)}
		writers = append(writers, fwd)
	}
	switch len(writers) {
	case 1:
		cmd.Stdout = writers[0]
	case 2:
		cmd.Stdout = io.MultiWriter(writers...)
	}
	// Using the same writer for both makes exec share one pipe, which keeps
	// stdout and stderr in the order the command wrote them.
	cmd.Stderr = cmd.Stdout

	start := time.Now()
	err := cmd.Run()
	res := result{duration: time.Since(start), exitCode: exitCode(cmd.ProcessState)}
	if fwd != nil {
		fwd.Flush()
	}

	// Problems are logged, and with --sync also appended to the output so
	// the caller sees them.
	note := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		s.log.Printf("#%d: %s", j.id, msg)
		if capture != nil {
			capture.addNote(fmt.Sprintf("hook: %s\n", msg))
		}
	}
	// kill() returns success for a zombie, so a deadline that passes in the
	// moment after the command has already exited must not be reported as a
	// timeout. Only a process that actually died from the signal counts.
	switch {
	case cmd.ProcessState == nil:
		note("%v", err)
	case timeoutOverride(cmd.ProcessState, killed.Load(), ctx.Err()):
		res.exitCode = exitTimeout
		note("timed out after %s, killed", s.cfg.timeout)
	case killed.Load() && signaled(cmd.ProcessState):
		note("killed, hook is shutting down")
	case errors.Is(err, exec.ErrWaitDelay):
		note("command exited but left its output open; a background process should redirect its output")
	}

	if capture != nil {
		res.output, res.truncated = capture.buf.Bytes(), capture.truncated
	}
	return res
}

// exitCode reports how the command ended as a shell-style exit code: its
// exit status, 128+n if it was killed by signal n, or 127 if it never
// started.
func exitCode(ps *os.ProcessState) int {
	if ps == nil {
		return 127
	}
	if code := ps.ExitCode(); code >= 0 {
		return code
	}
	return signalExitCode(ps)
}

// signaled reports whether ps died from a signal. A process that has already
// exited on its own is not signaled, even though kill still returns success
// for the zombie it left behind.
func signaled(ps *os.ProcessState) bool {
	return ps != nil && ps.ExitCode() < 0
}

// timeoutOverride reports whether hook's timeout kill is what ended the
// process, as opposed to a deadline that merely raced a normal exit.
func timeoutOverride(ps *os.ProcessState, killed bool, ctxErr error) bool {
	return killed && signaled(ps) && errors.Is(ctxErr, context.DeadlineExceeded)
}

// capBuffer keeps the first max bytes written to it and drops the rest.
type capBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); len(p) > room {
		b.truncated = true
		b.buf.Write(p[:max(room, 0)])
		return len(p), nil
	}
	return b.buf.Write(p)
}

// addNote appends a short diagnostic from hook itself. It is not counted
// against the capture limit and does not by itself mark the output
// truncated: a command that already filled the buffer (a long build log
// that then times out, or one that exits but leaves the pipe open) must
// not swallow the reason.
func (b *capBuffer) addNote(s string) {
	b.buf.WriteString(s)
}

// prefixWriter forwards complete lines to the log, each prefixed with the
// run number so that output from different runs can be told apart.
//
// Bytes that can move the cursor are removed first. The prefix is added
// only when a line is emitted, so a carriage return, a backspace, or an
// escape sequence in the command's output (a progress bar, or a payload
// the command prints) cannot overwrite that prefix or an earlier line.
type prefixWriter struct {
	log    *logger
	prefix string
	buf    []byte
	esc    int // terminal-sequence state, carried across Write calls
}

// Terminal-sequence states for dropTerminalControls.
const (
	termNone = iota
	termESC
	termCSI
	termOSC
	termOSCEsc // ESC inside OSC, expecting '\' to end it
	termOne    // one more byte belongs to the sequence (charset, SS3)
	termC1     // saw 0xC2; the next byte may be a C1 control
)

func (w *prefixWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.buf = dropTerminalControls(w.buf, p, &w.esc)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.log.Line(w.prefix, w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) >= maxLine {
		w.log.Line(w.prefix, w.buf)
		w.buf = w.buf[:0]
	}
	return n, nil
}

// dropTerminalControls appends src to dst with cursor-moving bytes removed.
// Tabs and newlines are kept. Carriage returns, backspaces, other ASCII
// controls and DEL are dropped. ANSI escapes (ESC ...) and C1 controls
// (U+0080-U+009F, the UTF-8 bytes C2 80-C2 9F, including the 8-bit CSI)
// are removed, even when a sequence is split across chunks. esc is the
// state from the previous chunk.
func dropTerminalControls(dst, src []byte, esc *int) []byte {
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch *esc {
		case termNone:
			switch {
			case c == '\n' || c == '\t':
				dst = append(dst, c)
			case c == 0x1b:
				*esc = termESC
			case c == 0xc2:
				*esc = termC1
			case c < 0x20 || c == 0x7f:
				// CR, backspace, form feed, bell, and the rest of C0.
			default:
				dst = append(dst, c)
			}
		case termESC:
			switch c {
			case '[':
				*esc = termCSI
			case ']':
				*esc = termOSC
			case '(', ')', '*', '+', '-', '.', '/', 'O':
				*esc = termOne
			case 0x1b:
				*esc = termESC
			case '\n':
				*esc = termNone
				dst = append(dst, c)
			default:
				// ESC plus one byte: ESC c, ESC D, ESC M, ESC 7, ...
				*esc = termNone
			}
		case termCSI:
			switch {
			case c >= 0x40 && c <= 0x7e:
				*esc = termNone
			case c == '\n':
				*esc = termNone
				dst = append(dst, c)
			case c == 0x1b:
				*esc = termESC
			case c < 0x20 || c >= 0x7f:
				// Not a CSI parameter. End the sequence and handle c.
				*esc = termNone
				i--
			}
		case termOSC:
			switch c {
			case 0x07: // BEL ends OSC
				*esc = termNone
			case 0x1b: // possible ST (ESC \)
				*esc = termOSCEsc
			case '\n':
				*esc = termNone
				dst = append(dst, c)
			}
		case termOSCEsc:
			if c == '\\' {
				*esc = termNone
				continue
			}
			*esc = termESC
			i--
		case termOne:
			*esc = termNone
			if c == 0x1b || c == '\n' {
				i--
			}
		case termC1:
			*esc = termNone
			if c >= 0x80 && c <= 0x9f {
				switch c {
				case 0x9b: // CSI
					*esc = termCSI
				case 0x9d: // OSC
					*esc = termOSC
				}
				continue
			}
			// 0xC2 started a character that is not a C1 control (for
			// example U+00A9, ©). Keep it.
			dst = append(dst, 0xc2)
			i--
		}
	}
	return dst
}

// Flush writes out a last line that had no trailing newline.
func (w *prefixWriter) Flush() {
	// A trailing 0xC2 with no second byte is not a control sequence.
	if w.esc == termC1 {
		w.buf = append(w.buf, 0xc2)
		w.esc = termNone
	}
	if len(w.buf) > 0 {
		w.log.Line(w.prefix, w.buf)
		w.buf = nil
	}
}
