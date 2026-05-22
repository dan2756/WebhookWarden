package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
			fmt.Fprintf(capture, "hook: %s\n", msg)
		}
	}
	switch {
	case cmd.ProcessState == nil:
		note("%v", err)
	case err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.exitCode = exitTimeout
		note("timed out after %s, killed", s.cfg.timeout)
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

// prefixWriter forwards complete lines to the log, each prefixed with the
// run number so that output from different runs can be told apart.
type prefixWriter struct {
	log    *logger
	prefix string
	buf    []byte
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
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
	return len(p), nil
}

// Flush writes out a last line that had no trailing newline.
func (w *prefixWriter) Flush() {
	if len(w.buf) > 0 {
		w.log.Line(w.prefix, w.buf)
		w.buf = nil
	}
}
